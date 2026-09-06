package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/events"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
	"evidentia/backend/internal/utils"
)

// genericUserForbiddenMessage matches internal/middleware's
// genericForbiddenMessage verbatim, for the same reason
// genericCaseForbiddenMessage does in case_service.go: a caller must never
// be able to distinguish "denied by the middleware" from "denied by this
// service's own re-check."
const genericUserForbiddenMessage = "You do not have permission to perform this action"

const (
	minPasswordLen   = 8
	maxPasswordLen   = 128
	maxEmailLen      = 254
	maxNameLen       = 255
	maxPhoneLen      = 32
	maxUserSearchLen = 255
)

// adminGuardLockKey is the fixed PostgreSQL advisory-lock key
// ensureNotLastActiveAdmin acquires — a distinct, arbitrary constant from
// internal/audit's own auditChainLockKey (891273465019), reserved solely
// for this purpose. See db/queries/roles.sql's AcquireAdminGuardLock for
// why this must be a real database-level lock, not an application-level
// mutex (which would do nothing across pooled connections or multiple
// backend processes).
const adminGuardLockKey int64 = 275108340461

// userRoles/userStatuses mirror the fixed catalogs this project already
// defines elsewhere (backend/db/seed/001_reference_data.sql's role rows;
// users_status_check in the schema) — validation here can never drift from
// what the database itself accepts, same discipline as case_service.go's
// caseStatuses.
var userRoles = map[string]bool{
	models.RoleAdmin:     true,
	models.RolePolice:    true,
	models.RoleForensics: true,
	models.RoleLawyer:    true,
	models.RoleJudge:     true,
}

var userStatuses = map[string]bool{
	models.UserStatusActive:    true,
	models.UserStatusInactive:  true,
	models.UserStatusSuspended: true,
}

// ---- DTOs ----
//
// AdminUserSummary is the ONLY user-shaped value this service returns to a
// handler — never a bare generated.User/*Row, which would risk leaking
// password_hash (master prompt §8/§46). Roles is the user's full role set
// (never just a "primary" role), since an admin managing role assignment
// needs to see exactly what is currently assigned.
type AdminUserSummary struct {
	ID          uuid.UUID  `json:"id"`
	Email       string     `json:"email"`
	FirstName   string     `json:"first_name"`
	LastName    string     `json:"last_name"`
	DisplayName *string    `json:"display_name,omitempty"`
	Phone       *string    `json:"phone,omitempty"`
	Status      string     `json:"status"`
	Roles       []string   `json:"roles"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

type UserListResult struct {
	Users []AdminUserSummary `json:"users"`
	Meta  utils.Meta         `json:"meta"`
}

// UserListFilter is GET /admin/users's optional, server-side filter set —
// every field nil means "no constraint on this field" (mirrors
// CaseListFilter).
type UserListFilter struct {
	Role   *string
	Status *string
	Search *string
}

// CreateUserInput is POST /admin/users's request shape. There is
// deliberately no id/created_at/updated_at field — those are
// server-controlled and this type structurally cannot carry a
// client-supplied value for either (master prompt §5).
type CreateUserInput struct {
	Email       string
	Password    string
	FirstName   string
	LastName    string
	DisplayName *string
	Phone       *string
	Role        string
	Status      *string // nil => models.UserStatusActive
}

// UpdateUserInput is PUT /admin/users/:id's request shape — a full
// replacement of every mutable profile field, matching
// UpdateCaseInput/db/queries/users.sql's UpdateUserProfile contract.
// Deliberately excludes email/password/role/status — those each have
// their own dedicated, separately authorized operation.
type UpdateUserInput struct {
	FirstName   string
	LastName    string
	DisplayName *string
	Phone       *string
}

// RoleCatalogEntry is GET /admin/roles's response shape.
type RoleCatalogEntry struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
}

// UserService owns admin user-management business logic: input validation,
// bcrypt hashing, transactional persistence, session revocation on
// security-relevant changes, and audit integration. Like CaseService, it
// independently re-checks authorization via authz.Service rather than
// trusting that a caller already passed through
// middleware.RequirePermission — see that type's doc comment for the full
// rationale.
type UserService struct {
	pool       *pgxpool.Pool
	authz      *authz.Service
	recorder   audit.Recorder
	publisher  events.Publisher
	bcryptCost int
}

func NewUserService(pool *pgxpool.Pool, authzService *authz.Service, recorder audit.Recorder, publisher events.Publisher, bcryptCost int) *UserService {
	return &UserService{pool: pool, authz: authzService, recorder: recorder, publisher: publisher, bcryptCost: bcryptCost}
}

// adminUsersScopeID is the fixed, singleton events.ResourceID every
// admin-user-management event is published under — admin user management
// is inherently a global resource (there is no per-case/per-agency
// scoping for the users table itself — see this file's own ListUsers doc
// comment), so unlike a case or an audit verification (each with a real
// per-instance ID), there is exactly one scope to subscribe to:
// events.ScopeKey(events.ResourceTypeAdminUsers, adminUsersScopeID). Only
// a caller who already holds user:read (RBAC, ADMIN-only per the seed
// data) is ever authorized to register for it — see
// internal/handlers/user/events.go.
const adminUsersScopeID = "global"

// ---- Create ----

// CreateUser authorizes actor for user:create, validates req, hashes the
// supplied initial password (never a client-supplied hash, never a
// server-generated/emailed password — master prompt §7 leaves the initial-
// password workflow to the admin-supplied-password shape this project
// already has the pieces for), and creates the user plus its single role
// assignment in one transaction. A duplicate email is mapped to 409, never
// a raw constraint-violation error.
func (s *UserService) CreateUser(ctx context.Context, actor auth.AuthenticatedUser, req CreateUserInput) (*AdminUserSummary, error) {
	allowed, err := s.authz.HasPermission(ctx, actor, authz.ActionUserCreate)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericUserForbiddenMessage)
	}

	email := strings.TrimSpace(req.Email)
	firstName := strings.TrimSpace(req.FirstName)
	lastName := strings.TrimSpace(req.LastName)

	if err := validateEmail(email); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if err := validateUserName(firstName, "first_name"); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if err := validateUserName(lastName, "last_name"); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if err := validatePassword(req.Password); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if err := validateDisplayName(req.DisplayName); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if err := validatePhone(req.Phone); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if !userRoles[req.Role] {
		return nil, utils.ErrBadRequest("Invalid role")
	}
	status := models.UserStatusActive
	if req.Status != nil && *req.Status != "" {
		if !userStatuses[*req.Status] {
			return nil, utils.ErrBadRequest("Invalid status")
		}
		status = *req.Status
	}

	hash, err := auth.HashPassword(req.Password, s.bcryptCost)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	ident := repository.AppIdentity{UserID: actor.ID, Role: effectiveCaseRole(actor)}

	var summary AdminUserSummary
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		created, err := q.CreateUser(ctx, generated.CreateUserParams{
			Email:        email,
			PasswordHash: hash,
			FirstName:    firstName,
			LastName:     lastName,
			DisplayName:  trimmedOrNil(req.DisplayName),
			Phone:        trimmedOrNil(req.Phone),
		})
		if err != nil {
			return err
		}

		role, err := q.GetRoleByName(ctx, req.Role)
		if err != nil {
			return fmt.Errorf("load role: %w", err)
		}
		if err := q.AssignRoleToUser(ctx, generated.AssignRoleToUserParams{UserID: created.ID, RoleID: role.ID}); err != nil {
			return fmt.Errorf("assign role: %w", err)
		}

		summary = fromCreateUserRow(created, []string{req.Role})

		if status != models.UserStatusActive {
			updated, err := q.UpdateUserStatus(ctx, generated.UpdateUserStatusParams{ID: created.ID, Status: status})
			if err != nil {
				return fmt.Errorf("set initial status: %w", err)
			}
			summary = fromUpdateUserStatusRow(updated, []string{req.Role})
		}
		return nil
	})
	if err != nil {
		if isUniqueViolation(err, "users_email_unique") {
			return nil, utils.ErrConflict("A user with this email already exists")
		}
		return nil, utils.ErrInternal(err)
	}

	s.recorder.Record(ctx, audit.Event{
		Action: "USER_CREATED", ResourceType: "user", ResourceID: &summary.ID,
		UserID: &actor.ID, Role: effectiveCaseRole(actor),
		Metadata: map[string]any{"target_email": summary.Email, "role": req.Role, "status": summary.Status},
	})
	s.publisher.Publish(ctx, events.TypeUserCreated, events.ResourceTypeAdminUsers, adminUsersScopeID, events.AdminUserEventData{
		UserID: summary.ID.String(), Email: summary.Email, Roles: summary.Roles, Status: summary.Status,
	})

	return &summary, nil
}

// ---- List ----

// ListUsers authorizes actor for user:read and returns the filtered,
// paginated user catalog. Unlike ListCases, this has no RLS row-visibility
// layer to rely on (users/roles carry none — see user_repo.go's own doc
// comment); RBAC alone is the gate, which is correct here since only ADMIN
// holds user:read per the seed data (master prompt §6: this is a global
// administrative listing, not a per-caller-scoped one).
func (s *UserService) ListUsers(ctx context.Context, actor auth.AuthenticatedUser, filter UserListFilter, page utils.Pagination) (*UserListResult, error) {
	allowed, err := s.authz.HasPermission(ctx, actor, authz.ActionUserRead)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericUserForbiddenMessage)
	}

	if filter.Role != nil && !userRoles[*filter.Role] {
		return nil, utils.ErrBadRequest("Invalid role filter")
	}
	if filter.Status != nil && !userStatuses[*filter.Status] {
		return nil, utils.ErrBadRequest("Invalid status filter")
	}
	if filter.Search != nil && len(*filter.Search) > maxUserSearchLen {
		return nil, utils.ErrBadRequest("search filter is too long")
	}

	listArg := generated.ListUsersFilteredParams{
		Status: filter.Status, Role: filter.Role, Search: filter.Search,
		OffsetVal: page.Offset(), LimitVal: page.Limit(),
	}
	countArg := generated.CountUsersFilteredParams{Status: filter.Status, Role: filter.Role, Search: filter.Search}

	var summaries []AdminUserSummary
	var total int64
	err = repository.WithTx(ctx, s.pool, repository.AppIdentity{UserID: actor.ID, Role: effectiveCaseRole(actor)}, func(ctx context.Context, q *generated.Queries) error {
		rows, err := q.ListUsersFiltered(ctx, listArg)
		if err != nil {
			return fmt.Errorf("list users: %w", err)
		}

		summaries = make([]AdminUserSummary, len(rows))
		for i, r := range rows {
			roles, err := q.ListRolesForUser(ctx, r.ID)
			if err != nil {
				return fmt.Errorf("load roles for %s: %w", r.ID, err)
			}
			summaries[i] = fromListUsersFilteredRow(r, roleNames(roles))
		}

		total, err = q.CountUsersFiltered(ctx, countArg)
		if err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	return &UserListResult{Users: summaries, Meta: page.BuildMeta(total)}, nil
}

// ---- Get ----

func (s *UserService) GetUser(ctx context.Context, actor auth.AuthenticatedUser, id uuid.UUID) (*AdminUserSummary, error) {
	allowed, err := s.authz.HasPermission(ctx, actor, authz.ActionUserRead)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericUserForbiddenMessage)
	}
	return s.loadUserSummary(ctx, id)
}

// GetOwnProfile returns actor's own profile with no RBAC permission check
// beyond having a valid, already-authenticated identity — every role may
// view their own profile, regardless of whether they hold user:read
// (which per the seed data only ADMIN does). Used by GET /users/me; never
// used for viewing anyone else's record (see GetUser for that, which DOES
// require user:read).
func (s *UserService) GetOwnProfile(ctx context.Context, actor auth.AuthenticatedUser) (*AdminUserSummary, error) {
	return s.loadUserSummary(ctx, actor.ID)
}

// ---- Update profile ----

func (s *UserService) UpdateUser(ctx context.Context, actor auth.AuthenticatedUser, id uuid.UUID, req UpdateUserInput) (*AdminUserSummary, error) {
	allowed, err := s.authz.HasPermission(ctx, actor, authz.ActionUserUpdate)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericUserForbiddenMessage)
	}

	firstName := strings.TrimSpace(req.FirstName)
	lastName := strings.TrimSpace(req.LastName)
	if err := validateUserName(firstName, "first_name"); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if err := validateUserName(lastName, "last_name"); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if err := validateDisplayName(req.DisplayName); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if err := validatePhone(req.Phone); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}

	var updated generated.UpdateUserProfileRow
	err = repository.WithTx(ctx, s.pool, repository.AppIdentity{UserID: actor.ID, Role: effectiveCaseRole(actor)}, func(ctx context.Context, q *generated.Queries) error {
		u, err := q.UpdateUserProfile(ctx, generated.UpdateUserProfileParams{
			ID: id, FirstName: firstName, LastName: lastName,
			DisplayName: trimmedOrNil(req.DisplayName), Phone: trimmedOrNil(req.Phone),
		})
		updated = u
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, utils.ErrNotFound("User not found")
		}
		return nil, utils.ErrInternal(err)
	}

	roles, err := s.rolesForUser(ctx, id)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	summary := fromUpdateUserProfileRow(updated, roles)

	s.recorder.Record(ctx, audit.Event{
		Action: "USER_UPDATED", ResourceType: "user", ResourceID: &id,
		UserID: &actor.ID, Role: effectiveCaseRole(actor),
	})
	s.publisher.Publish(ctx, events.TypeUserUpdated, events.ResourceTypeAdminUsers, adminUsersScopeID, events.AdminUserEventData{
		UserID: summary.ID.String(), Email: summary.Email, Roles: summary.Roles, Status: summary.Status,
	})

	return &summary, nil
}

// ---- Role assignment ----

// UpdateRole authorizes via authz.Service.CanModifyUserRole — the ONE
// mechanism docs/API_ENDPOINTS.md documents for this exact route (RBAC
// user:role PLUS the hard, RBAC-independent block on an actor modifying
// their own role — see that function's own doc comment). This project
// treats a user as holding a single "acting" role at a time (see
// AuthService.primaryRoleName/effectiveCaseRole); UpdateRole enforces that
// same shape by replacing the user's entire role set with newRole, rather
// than adding a second role alongside an existing one.
func (s *UserService) UpdateRole(ctx context.Context, actor auth.AuthenticatedUser, targetID uuid.UUID, newRole string) (*AdminUserSummary, error) {
	decision, err := s.authz.CanModifyUserRole(ctx, actor, targetID)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericUserForbiddenMessage)
	}

	if !userRoles[newRole] {
		return nil, utils.ErrBadRequest("Invalid role")
	}

	ident := repository.AppIdentity{UserID: actor.ID, Role: effectiveCaseRole(actor)}

	var oldRoleNames []string
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		target, err := q.GetUserByID(ctx, targetID)
		if err != nil {
			return err
		}

		current, err := q.ListRolesForUser(ctx, targetID)
		if err != nil {
			return fmt.Errorf("load current roles: %w", err)
		}
		oldRoleNames = roleNames(current)

		targetRole, err := q.GetRoleByName(ctx, newRole)
		if err != nil {
			return fmt.Errorf("load role: %w", err)
		}

		// System 14's "last active Administrator" safeguard: only relevant
		// when this change would actually REMOVE the ADMIN role from a
		// currently-ACTIVE admin (newRole is something else, and ADMIN is
		// among current) — a no-op re-assignment of ADMIN, or a change to
		// an already-inactive/suspended user, can never newly cause a
		// lockout. See ensureNotLastActiveAdmin's own doc comment.
		if newRole != models.RoleAdmin && target.Status == models.UserStatusActive && containsRole(oldRoleNames, models.RoleAdmin) {
			if err := s.ensureNotLastActiveAdmin(ctx, q); err != nil {
				return err
			}
		}

		for _, r := range current {
			if r.ID == targetRole.ID {
				continue
			}
			if err := q.RemoveRoleFromUser(ctx, generated.RemoveRoleFromUserParams{UserID: targetID, RoleID: r.ID}); err != nil {
				return fmt.Errorf("remove role %s: %w", r.Name, err)
			}
		}
		if err := q.AssignRoleToUser(ctx, generated.AssignRoleToUserParams{UserID: targetID, RoleID: targetRole.ID}); err != nil {
			return fmt.Errorf("assign role: %w", err)
		}
		return nil
	})
	if err != nil {
		// ensureNotLastActiveAdmin's own utils.ErrConflict must pass through
		// unchanged, never re-wrapped as a generic 500 — see that method's
		// doc comment.
		if appErr, ok := utils.AsAppError(err); ok {
			return nil, appErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, utils.ErrNotFound("User not found")
		}
		return nil, utils.ErrInternal(err)
	}

	s.recorder.Record(ctx, audit.Event{
		Action: "USER_ROLE_CHANGED", ResourceType: "user", ResourceID: &targetID,
		UserID: &actor.ID, Role: effectiveCaseRole(actor),
		Metadata: map[string]any{"from": oldRoleNames, "to": newRole},
	})

	result, err := s.loadUserSummary(ctx, targetID)
	if err != nil {
		return nil, err
	}
	s.publisher.Publish(ctx, events.TypeUserRoleChanged, events.ResourceTypeAdminUsers, adminUsersScopeID, events.AdminUserEventData{
		UserID: targetID.String(), Email: result.Email, Roles: result.Roles, Status: result.Status,
	})
	return result, nil
}

// ---- Status (activate/deactivate/suspend) ----

// UpdateStatus authorizes actor for user:deactivate and additionally
// blocks an actor from changing their OWN status — same self-block shape
// as CanModifyUserRole, preventing an admin from locking themselves out or
// otherwise short-circuiting this operation on their own account. On any
// non-active status, every one of the target's refresh sessions is
// revoked (master prompt: token-theft mitigation) so an already-issued
// token cannot keep a now-deactivated account's session alive.
func (s *UserService) UpdateStatus(ctx context.Context, actor auth.AuthenticatedUser, targetID uuid.UUID, status string) (*AdminUserSummary, error) {
	allowed, err := s.authz.HasPermission(ctx, actor, authz.ActionUserDeactivate)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericUserForbiddenMessage)
	}
	if actor.ID == targetID {
		s.recorder.Record(ctx, audit.Event{
			Action: "AUTHZ_DENIED", ResourceType: "user", ResourceID: &targetID,
			UserID: &actor.ID, Role: effectiveCaseRole(actor),
			Metadata: map[string]any{"action": string(authz.ActionUserDeactivate), "reason": "self_status_change_forbidden"},
		})
		return nil, utils.ErrForbidden(genericUserForbiddenMessage)
	}
	if !userStatuses[status] {
		return nil, utils.ErrBadRequest("Invalid status")
	}

	ident := repository.AppIdentity{UserID: actor.ID, Role: effectiveCaseRole(actor)}

	var before generated.GetUserByIDRow
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		b, err := q.GetUserByID(ctx, targetID)
		if err != nil {
			return err
		}
		before = b

		// System 14's "last active Administrator" safeguard: only relevant
		// when this change would actually make a currently-ACTIVE admin
		// no longer active (status is moving away from ACTIVE, and the
		// target currently holds ADMIN) — reactivating someone, or
		// changing a non-admin's status, can never newly cause a lockout.
		if status != models.UserStatusActive && b.Status == models.UserStatusActive {
			targetRoles, err := q.ListRolesForUser(ctx, targetID)
			if err != nil {
				return fmt.Errorf("load target roles: %w", err)
			}
			if containsRole(roleNames(targetRoles), models.RoleAdmin) {
				if err := s.ensureNotLastActiveAdmin(ctx, q); err != nil {
					return err
				}
			}
		}

		if _, err := q.UpdateUserStatus(ctx, generated.UpdateUserStatusParams{ID: targetID, Status: status}); err != nil {
			return fmt.Errorf("update status: %w", err)
		}

		if status != models.UserStatusActive {
			if err := q.RevokeAllAuthSessionsForUser(ctx, targetID); err != nil {
				return fmt.Errorf("revoke sessions: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		if appErr, ok := utils.AsAppError(err); ok {
			return nil, appErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, utils.ErrNotFound("User not found")
		}
		return nil, utils.ErrInternal(err)
	}

	s.recorder.Record(ctx, audit.Event{
		Action: "USER_STATUS_CHANGED", ResourceType: "user", ResourceID: &targetID,
		UserID: &actor.ID, Role: effectiveCaseRole(actor),
		Metadata: map[string]any{"from": before.Status, "to": status},
	})

	result, err := s.loadUserSummary(ctx, targetID)
	if err != nil {
		return nil, err
	}
	s.publisher.Publish(ctx, statusChangeEventType(status), events.ResourceTypeAdminUsers, adminUsersScopeID, events.AdminUserEventData{
		UserID: targetID.String(), Email: result.Email, Roles: result.Roles, Status: result.Status,
	})
	return result, nil
}

// statusChangeEventType maps a target account-status value to the
// SCREAMING_SNAKE_CASE event type describing that transition — see
// internal/events/catalog.go's own doc comment for why ACTIVATED/
// DEACTIVATED/SUSPENDED are distinct event types rather than one generic
// "USER_STATUS_CHANGED" (matching this same distinction audit.Event's
// own "USER_STATUS_CHANGED" action already collapses into one action
// with a from/to metadata pair — the REAL-TIME event layer is more
// specific on purpose, since "activated" and "deactivated" are the kind
// of distinction a live dashboard update benefits from without decoding
// a from/to pair itself).
func statusChangeEventType(status string) string {
	switch status {
	case models.UserStatusActive:
		return events.TypeUserActivated
	case models.UserStatusSuspended:
		return events.TypeUserSuspended
	default:
		return events.TypeUserDeactivated
	}
}

// ---- Password reset (admin-initiated) ----

// ResetPassword lets an ADMIN set a new password for another user — the
// project's chosen "secure initial-password workflow" for both the initial
// credential (CreateUser) and any later reset (master prompt §13/§7): no
// email/token-based flow is implemented, since none is otherwise specified
// for this project (master prompt: "don't build an unnecessarily large
// email system"). Reuses user:update rather than introducing a new
// permission for one action. Every existing session is revoked afterward,
// same as UpdateStatus, so a stolen refresh token can't outlive the reset.
func (s *UserService) ResetPassword(ctx context.Context, actor auth.AuthenticatedUser, targetID uuid.UUID, newPassword string) error {
	allowed, err := s.authz.HasPermission(ctx, actor, authz.ActionUserUpdate)
	if err != nil {
		return utils.ErrInternal(err)
	}
	if !allowed {
		return utils.ErrForbidden(genericUserForbiddenMessage)
	}
	if err := validatePassword(newPassword); err != nil {
		return utils.ErrBadRequest(err.Error())
	}

	hash, err := auth.HashPassword(newPassword, s.bcryptCost)
	if err != nil {
		return utils.ErrInternal(err)
	}

	ident := repository.AppIdentity{UserID: actor.ID, Role: effectiveCaseRole(actor)}
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		if _, err := q.GetUserByID(ctx, targetID); err != nil {
			return err
		}
		if err := q.UpdateUserPasswordHash(ctx, generated.UpdateUserPasswordHashParams{ID: targetID, PasswordHash: hash}); err != nil {
			return fmt.Errorf("update password: %w", err)
		}
		return q.RevokeAllAuthSessionsForUser(ctx, targetID)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return utils.ErrNotFound("User not found")
		}
		return utils.ErrInternal(err)
	}

	s.recorder.Record(ctx, audit.Event{
		Action: "USER_PASSWORD_RESET", ResourceType: "user", ResourceID: &targetID,
		UserID: &actor.ID, Role: effectiveCaseRole(actor),
	})
	return nil
}

// ---- Role catalog ----

// ListRoles requires no permission beyond authentication (see
// docs/API_ENDPOINTS.md's Admin section) — it lists the fixed,
// non-sensitive role catalog, not any per-user data.
func (s *UserService) ListRoles(ctx context.Context) ([]RoleCatalogEntry, error) {
	var roles []generated.Role
	err := repository.WithTx(ctx, s.pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
		r, err := q.ListRoles(ctx)
		roles = r
		return err
	})
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	entries := make([]RoleCatalogEntry, len(roles))
	for i, r := range roles {
		entries[i] = RoleCatalogEntry{ID: r.ID, Name: r.Name, Description: r.Description}
	}
	return entries, nil
}

// ---- internal helpers ----

// loadUserSummary fetches userID's current profile + role set with no
// authorization check of its own — callers (GetUser, UpdateRole,
// UpdateStatus) MUST have already authorized the caller, exactly like
// CaseService.loadCaseDetail.
func (s *UserService) loadUserSummary(ctx context.Context, userID uuid.UUID) (*AdminUserSummary, error) {
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
			return nil, utils.ErrNotFound("User not found")
		}
		return nil, utils.ErrInternal(err)
	}

	summary := fromGetUserByIDRow(profile, roleNames(roles))
	return &summary, nil
}

func (s *UserService) rolesForUser(ctx context.Context, userID uuid.UUID) ([]string, error) {
	var roles []generated.Role
	err := repository.WithTx(ctx, s.pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
		r, err := q.ListRolesForUser(ctx, userID)
		roles = r
		return err
	})
	if err != nil {
		return nil, err
	}
	return roleNames(roles), nil
}

func roleNames(roles []generated.Role) []string {
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = r.Name
	}
	return names
}

func containsRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

// ensureNotLastActiveAdmin refuses (utils.ErrConflict) an in-progress
// role/status change if doing so would leave the system with ZERO active
// Administrator accounts — master prompt's "an Admin must not accidentally
// remove the last usable Admin account" / "prevent privilege-loss
// scenarios that lock the system out". Callers (UpdateRole/UpdateStatus)
// must call this ONLY when the change in question would actually make a
// currently-ACTIVE admin no longer an active admin — see each call site's
// own guard condition — and must call it from WITHIN the same transaction
// that performs the actual UPDATE, after which q's connection still holds
// the advisory lock this acquires until that transaction commits or rolls
// back (see db/queries/roles.sql's AcquireAdminGuardLock for the full
// concurrency argument: this is what prevents two concurrent operations,
// each targeting a DIFFERENT remaining admin, from both independently
// observing "safe to proceed" and jointly reaching zero).
func (s *UserService) ensureNotLastActiveAdmin(ctx context.Context, q *generated.Queries) error {
	if err := q.AcquireAdminGuardLock(ctx, adminGuardLockKey); err != nil {
		return fmt.Errorf("acquire admin guard lock: %w", err)
	}
	adminRole, err := q.GetRoleByName(ctx, models.RoleAdmin)
	if err != nil {
		return fmt.Errorf("load admin role: %w", err)
	}
	count, err := q.CountActiveUsersWithRole(ctx, adminRole.ID)
	if err != nil {
		return fmt.Errorf("count active admins: %w", err)
	}
	if count <= 1 {
		return utils.ErrConflict("Cannot remove the last active Administrator account")
	}
	return nil
}

func fromCreateUserRow(r generated.CreateUserRow, roles []string) AdminUserSummary {
	return AdminUserSummary{
		ID: r.ID, Email: r.Email, FirstName: r.FirstName, LastName: r.LastName,
		DisplayName: r.DisplayName, Phone: r.Phone, Status: r.Status, Roles: roles,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, LastLoginAt: timestamptzPtr(r.LastLoginAt),
	}
}

func fromGetUserByIDRow(r generated.GetUserByIDRow, roles []string) AdminUserSummary {
	return AdminUserSummary{
		ID: r.ID, Email: r.Email, FirstName: r.FirstName, LastName: r.LastName,
		DisplayName: r.DisplayName, Phone: r.Phone, Status: r.Status, Roles: roles,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, LastLoginAt: timestamptzPtr(r.LastLoginAt),
	}
}

func fromUpdateUserProfileRow(r generated.UpdateUserProfileRow, roles []string) AdminUserSummary {
	return AdminUserSummary{
		ID: r.ID, Email: r.Email, FirstName: r.FirstName, LastName: r.LastName,
		DisplayName: r.DisplayName, Phone: r.Phone, Status: r.Status, Roles: roles,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, LastLoginAt: timestamptzPtr(r.LastLoginAt),
	}
}

func fromUpdateUserStatusRow(r generated.UpdateUserStatusRow, roles []string) AdminUserSummary {
	return AdminUserSummary{
		ID: r.ID, Email: r.Email, FirstName: r.FirstName, LastName: r.LastName,
		DisplayName: r.DisplayName, Phone: r.Phone, Status: r.Status, Roles: roles,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, LastLoginAt: timestamptzPtr(r.LastLoginAt),
	}
}

func fromListUsersFilteredRow(r generated.ListUsersFilteredRow, roles []string) AdminUserSummary {
	return AdminUserSummary{
		ID: r.ID, Email: r.Email, FirstName: r.FirstName, LastName: r.LastName,
		DisplayName: r.DisplayName, Phone: r.Phone, Status: r.Status, Roles: roles,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, LastLoginAt: timestamptzPtr(r.LastLoginAt),
	}
}

func timestamptzPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	tt := t.Time
	return &tt
}

// trimmedOrNil trims v and returns nil for both a nil input and a
// now-empty result — an admin submitting "" for an optional field (e.g.
// phone) stores NULL, not an empty string.
func trimmedOrNil(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func validateEmail(v string) error {
	if v == "" {
		return errors.New("email is required")
	}
	if len(v) > maxEmailLen {
		return fmt.Errorf("email must be at most %d characters", maxEmailLen)
	}
	if _, err := mail.ParseAddress(v); err != nil {
		return errors.New("email must be a valid email address")
	}
	return nil
}

func validateUserName(v, field string) error {
	if v == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(v) > maxNameLen {
		return fmt.Errorf("%s must be at most %d characters", field, maxNameLen)
	}
	return nil
}

func validateDisplayName(v *string) error {
	if v == nil {
		return nil
	}
	if len(*v) > maxNameLen {
		return fmt.Errorf("display_name must be at most %d characters", maxNameLen)
	}
	return nil
}

func validatePhone(v *string) error {
	if v == nil {
		return nil
	}
	if len(*v) > maxPhoneLen {
		return fmt.Errorf("phone must be at most %d characters", maxPhoneLen)
	}
	return nil
}

func validatePassword(v string) error {
	if len(v) < minPasswordLen {
		return fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(v) > maxPasswordLen {
		return fmt.Errorf("password must be at most %d characters", maxPasswordLen)
	}
	return nil
}
