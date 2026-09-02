// Package service holds business logic that orchestrates repositories,
// internal/auth primitives, and audit recording. Handlers never query the
// database, hash a password, or mint a token directly — they call into a
// service, which is the only layer permitted to do those things.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
	"evidentia/backend/internal/utils"
)

// genericAuthError is returned for every credential/token failure: unknown
// email, wrong password, inactive account, expired/revoked/reused refresh
// token all produce this exact same message. Distinguishing them to a
// client would enable user enumeration or reveal validation internals
// (master prompt §8/§12/§30) — the specific reason is recorded via the
// audit Recorder instead, for legitimate server-side diagnosis.
const genericAuthError = "Invalid email or password"

// genericRefreshError is genericAuthError's counterpart for /auth/refresh,
// worded for a token rather than credentials but equally non-specific
// about which check failed.
const genericRefreshError = "Invalid or expired refresh token"

// UserSummary is the public-safe subset of a user profile returned by
// login/refresh — never password_hash, never internal DB fields. Role is
// a single, "primary" role for response-shaping convenience (the first of
// the user's assigned roles, order per ListRolesForUser); the full set is
// carried internally via auth.AuthenticatedUser.Roles once System 4 needs
// it.
type UserSummary struct {
	ID        uuid.UUID
	Email     string
	FirstName string
	LastName  string
	Role      string
}

// AuthResult is what Login and Refresh return on success.
type AuthResult struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
	User             UserSummary
}

// AuthService implements authentication: password verification, access-
// token issuance, and refresh-token session lifecycle (create, rotate,
// revoke, reuse detection). It establishes WHO is making a request — RBAC/
// ABAC authorization (WHAT they may do) is System 4's responsibility, not
// this type's (master prompt §5).
type AuthService struct {
	pool       *pgxpool.Pool
	jwt        *auth.JWTManager
	bcryptCost int
	refreshTTL time.Duration
	recorder   audit.Recorder
}

func NewAuthService(pool *pgxpool.Pool, jwtManager *auth.JWTManager, bcryptCost int, refreshTTL time.Duration, recorder audit.Recorder) *AuthService {
	return &AuthService{
		pool:       pool,
		jwt:        jwtManager,
		bcryptCost: bcryptCost,
		refreshTTL: refreshTTL,
		recorder:   recorder,
	}
}

// Login validates email/password and, on success, issues a new access
// token and a new refresh-token session. ipAddress/userAgent are stored
// only as session diagnostic metadata (auth_sessions.ip_address/
// user_agent).
func (s *AuthService) Login(ctx context.Context, email, password, ipAddress, userAgent string) (*AuthResult, error) {
	var authRow generated.GetUserByEmailForAuthRow
	found, err := s.fetchAuthRow(ctx, email, &authRow)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	if !found {
		s.recordAuthFailure(ctx, "AUTH_LOGIN_FAILED", nil, "unknown_email")
		return nil, utils.ErrUnauthorized(genericAuthError)
	}

	if authRow.Status != models.UserStatusActive {
		s.recordAuthFailure(ctx, "AUTH_LOGIN_FAILED", &authRow.ID, "inactive_account")
		return nil, utils.ErrUnauthorized(genericAuthError)
	}

	// bcrypt comparison is deliberately slow and runs with no database
	// transaction held open (see fetchAuthRow above, which already
	// returned).
	if err := auth.VerifyPassword(authRow.PasswordHash, password); err != nil {
		s.recordAuthFailure(ctx, "AUTH_LOGIN_FAILED", &authRow.ID, "wrong_password")
		return nil, utils.ErrUnauthorized(genericAuthError)
	}

	var profile generated.GetUserByIDRow
	var roles []generated.Role
	err = repository.WithTx(ctx, s.pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
		p, err := q.GetUserByID(ctx, authRow.ID)
		if err != nil {
			return fmt.Errorf("load profile: %w", err)
		}
		profile = p

		rs, err := q.ListRolesForUser(ctx, authRow.ID)
		if err != nil {
			return fmt.Errorf("load roles: %w", err)
		}
		roles = rs

		return q.UpdateUserLastLogin(ctx, authRow.ID)
	})
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	primaryRole := primaryRoleName(roles)

	result, err := s.issueSession(ctx, authRow.ID, primaryRole, uuid.New(), ipAddress, userAgent)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	result.User = UserSummary{ID: profile.ID, Email: profile.Email, FirstName: profile.FirstName, LastName: profile.LastName, Role: primaryRole}

	s.recorder.Record(ctx, audit.Event{Action: "AUTH_LOGIN_SUCCESS", ResourceType: "user", UserID: &authRow.ID, Role: primaryRole})
	return &result.AuthResult, nil
}

// Refresh validates rawToken and, on success, rotates it: the presented
// token is revoked and a new access token + new refresh-token session
// (same family) are issued. Presenting an already-rotated (revoked) token
// is treated as reuse — see master prompt §25 — and revokes the entire
// token family, not just the one token presented.
func (s *AuthService) Refresh(ctx context.Context, rawToken, ipAddress, userAgent string) (*AuthResult, error) {
	tokenHash := auth.HashRefreshToken(rawToken)

	session, found, err := s.fetchSessionByHash(ctx, tokenHash)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !found {
		s.recordAuthFailure(ctx, "AUTH_REFRESH_FAILED", nil, "unknown_token")
		return nil, utils.ErrUnauthorized(genericRefreshError)
	}

	if session.RevokedAt.Valid {
		if revokeErr := repository.WithTx(ctx, s.pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
			return q.RevokeAuthSessionFamily(ctx, session.FamilyID)
		}); revokeErr != nil {
			return nil, utils.ErrInternal(revokeErr)
		}
		s.recorder.Record(ctx, audit.Event{
			Action: "AUTH_REFRESH_REUSE_DETECTED", ResourceType: "auth_session",
			UserID: &session.UserID, ResourceID: &session.ID,
		})
		return nil, utils.ErrUnauthorized(genericRefreshError)
	}

	if time.Now().UTC().After(session.ExpiresAt) {
		s.recordAuthFailure(ctx, "AUTH_REFRESH_FAILED", &session.UserID, "expired")
		return nil, utils.ErrUnauthorized(genericRefreshError)
	}

	var profile generated.GetUserByIDRow
	var roles []generated.Role
	err = repository.WithTx(ctx, s.pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
		p, err := q.GetUserByID(ctx, session.UserID)
		if err != nil {
			return fmt.Errorf("load profile: %w", err)
		}
		profile = p

		rs, err := q.ListRolesForUser(ctx, session.UserID)
		if err != nil {
			return fmt.Errorf("load roles: %w", err)
		}
		roles = rs
		return nil
	})
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	if profile.Status != models.UserStatusActive {
		s.recordAuthFailure(ctx, "AUTH_REFRESH_FAILED", &session.UserID, "inactive_account")
		return nil, utils.ErrUnauthorized(genericRefreshError)
	}

	primaryRole := primaryRoleName(roles)

	result, err := s.issueSession(ctx, session.UserID, primaryRole, session.FamilyID, ipAddress, userAgent)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	result.User = UserSummary{ID: profile.ID, Email: profile.Email, FirstName: profile.FirstName, LastName: profile.LastName, Role: primaryRole}

	// Rotation: revoke the presented session now that its replacement
	// exists, recording the link. A failure here does not un-issue the
	// tokens already minted above — but it does mean the old token might
	// remain briefly valid, which the next refresh attempt (finding it
	// not yet revoked) would simply treat as a normal, non-reuse refresh.
	// This is judged an acceptable, rare race over holding one long
	// transaction across token generation.
	newSessionID := result.newSessionID
	if err := repository.WithTx(ctx, s.pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
		return q.RevokeAuthSessionAndReplace(ctx, generated.RevokeAuthSessionAndReplaceParams{ID: session.ID, ReplacedBy: &newSessionID})
	}); err != nil {
		return nil, utils.ErrInternal(err)
	}

	s.recorder.Record(ctx, audit.Event{
		Action: "AUTH_REFRESH_SUCCESS", ResourceType: "auth_session",
		UserID: &session.UserID, ResourceID: &newSessionID,
	})
	return &result.AuthResult, nil
}

// Logout revokes the session identified by rawToken, if any. It is
// idempotent and intentionally quiet about whether a matching session
// existed — logging out with an already-invalid or empty token is not an
// error (access tokens are stateless and short-lived; there is nothing
// else to invalidate — master prompt §27/§28). currentUserID is the
// caller's own authenticated identity (from the access token that
// protects this endpoint — see master prompt §56); a token belonging to a
// different user is treated the same as an unknown token, never revoked
// and never distinguished in the response.
func (s *AuthService) Logout(ctx context.Context, rawToken string, currentUserID uuid.UUID) error {
	if rawToken == "" {
		return nil
	}

	session, found, err := s.fetchSessionByHash(ctx, auth.HashRefreshToken(rawToken))
	if err != nil {
		return utils.ErrInternal(err)
	}
	if !found || session.UserID != currentUserID {
		return nil
	}

	if err := repository.WithTx(ctx, s.pool, repository.AppIdentity{UserID: currentUserID}, func(ctx context.Context, q *generated.Queries) error {
		return q.RevokeAuthSession(ctx, session.ID)
	}); err != nil {
		return utils.ErrInternal(err)
	}

	s.recorder.Record(ctx, audit.Event{Action: "AUTH_LOGOUT", ResourceType: "auth_session", UserID: &currentUserID, ResourceID: &session.ID})
	return nil
}

// ResolveIdentity re-resolves userID's CURRENT status and roles from the
// database — never from JWT claims alone (master prompt §15) — for
// internal/middleware/auth_middleware.go to attach to the request context.
// An inactive/deactivated user is rejected here even if their access token
// has not yet expired (master prompt §62).
func (s *AuthService) ResolveIdentity(ctx context.Context, userID uuid.UUID) (auth.AuthenticatedUser, error) {
	var profile generated.GetUserByIDRow
	var roles []generated.Role
	err := repository.WithTx(ctx, s.pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
		p, err := q.GetUserByID(ctx, userID)
		if err != nil {
			return err
		}
		profile = p

		rs, err := q.ListRolesForUser(ctx, userID)
		if err != nil {
			return err
		}
		roles = rs
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.AuthenticatedUser{}, utils.ErrUnauthorized("Authentication required")
		}
		return auth.AuthenticatedUser{}, utils.ErrInternal(err)
	}

	if profile.Status != models.UserStatusActive {
		return auth.AuthenticatedUser{}, utils.ErrUnauthorized("Authentication required")
	}

	roleNames := make([]string, len(roles))
	for i, r := range roles {
		roleNames[i] = r.Name
	}

	return auth.AuthenticatedUser{ID: profile.ID, Email: profile.Email, Roles: roleNames}, nil
}

// AccessTTLSeconds exposes the configured access-token lifetime in whole
// seconds, for the login/refresh response's expires_in field.
func (s *AuthService) AccessTTLSeconds() int64 {
	return int64(s.jwt.AccessTTL().Seconds())
}

// ---- internal helpers ----

// sessionResult is AuthResult plus the new session's ID, needed internally
// to link rotation (replaced_by) without exposing a database ID through
// the public AuthResult type.
type sessionResult struct {
	AuthResult
	newSessionID uuid.UUID
}

func (s *AuthService) issueSession(ctx context.Context, userID uuid.UUID, role string, familyID uuid.UUID, ipAddress, userAgent string) (*sessionResult, error) {
	accessToken, accessExpiresAt, err := s.jwt.CreateAccessToken(userID, role)
	if err != nil {
		return nil, fmt.Errorf("create access token: %w", err)
	}

	rawRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}
	refreshExpiresAt := time.Now().UTC().Add(s.refreshTTL)

	var created generated.AuthSession
	err = repository.WithTx(ctx, s.pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
		c, err := q.CreateAuthSession(ctx, generated.CreateAuthSessionParams{
			UserID:    userID,
			FamilyID:  familyID,
			TokenHash: auth.HashRefreshToken(rawRefresh),
			ExpiresAt: refreshExpiresAt,
			IpAddress: stringPtrOrNil(ipAddress),
			UserAgent: stringPtrOrNil(userAgent),
		})
		created = c
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return &sessionResult{
		AuthResult: AuthResult{
			AccessToken:      accessToken,
			AccessExpiresAt:  accessExpiresAt,
			RefreshToken:     rawRefresh,
			RefreshExpiresAt: refreshExpiresAt,
		},
		newSessionID: created.ID,
	}, nil
}

func (s *AuthService) fetchAuthRow(ctx context.Context, email string, out *generated.GetUserByEmailForAuthRow) (found bool, err error) {
	err = repository.WithTx(ctx, s.pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
		row, err := q.GetUserByEmailForAuth(ctx, email)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		*out = row
		found = true
		return nil
	})
	return found, err
}

func (s *AuthService) fetchSessionByHash(ctx context.Context, tokenHash []byte) (generated.AuthSession, bool, error) {
	var session generated.AuthSession
	found := false
	err := repository.WithTx(ctx, s.pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
		sess, err := q.GetAuthSessionByTokenHash(ctx, tokenHash)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		session = sess
		found = true
		return nil
	})
	return session, found, err
}

// recordAuthFailure records a failed authentication attempt with a
// specific, non-public reason category — see genericAuthError/
// genericRefreshError for why the reason never reaches the client.
func (s *AuthService) recordAuthFailure(ctx context.Context, action string, userID *uuid.UUID, reason string) {
	s.recorder.Record(ctx, audit.Event{
		Action:       action,
		ResourceType: "user",
		UserID:       userID,
		Metadata:     map[string]any{"reason": reason},
	})
}

func primaryRoleName(roles []generated.Role) string {
	if len(roles) == 0 {
		return ""
	}
	return roles[0].Name
}

func stringPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
