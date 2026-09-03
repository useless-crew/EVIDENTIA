//go:build integration

// Run with: go test -tags=integration ./internal/service/...
// Requires the docker-compose postgres service up, migrated, seeded. Add
// -p 1 when running alongside other packages' integration tests — see
// auth_service_integration_test.go's doc comment; this file shares that
// same "truncates the shared users/roles tables" concern.
package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/utils"
)

func newUserServiceForTest(t *testing.T, pool *pgxpool.Pool, recorder audit.Recorder) *UserService {
	t.Helper()
	authzService := authz.NewService(pool, recorder)
	return NewUserService(pool, authzService, recorder, 4) // bcrypt cost 4: test speed only
}

const testInitialPassword = "initial-password-1"

func adminActor(t *testing.T, migrator *pgxpool.Pool) (uuid.UUID, func() auth.AuthenticatedUser) {
	t.Helper()
	id := newUserWithRole(t, migrator, "admin-actor-"+uuid.NewString()+"@example.com", models.RoleAdmin)
	return id, func() auth.AuthenticatedUser { return authUser(id, models.RoleAdmin) }
}

func TestUserService_CreateUser_AdminCanCreateEveryRole(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	recorder := &spyRecorder{}
	svc := newUserServiceForTest(t, app, recorder)

	_, actor := adminActor(t, migrator)

	for _, role := range []string{models.RoleAdmin, models.RolePolice, models.RoleForensics, models.RoleLawyer, models.RoleJudge} {
		summary, err := svc.CreateUser(context.Background(), actor(), CreateUserInput{
			Email: role + "-" + uuid.NewString() + "@example.com", Password: testInitialPassword,
			FirstName: "Test", LastName: "User", Role: role,
		})
		require.NoError(t, err, "ADMIN must be able to create a %s user", role)
		assert.Equal(t, []string{role}, summary.Roles)
		assert.Equal(t, models.UserStatusActive, summary.Status, "status defaults to active")
	}
	assert.Contains(t, recorder.actions(), "USER_CREATED")
}

func TestUserService_CreateUser_DuplicateEmailConflicts(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	svc := newUserServiceForTest(t, app, &spyRecorder{})
	_, actor := adminActor(t, migrator)

	email := "dupe@example.com"
	_, err := svc.CreateUser(context.Background(), actor(), CreateUserInput{
		Email: email, Password: testInitialPassword, FirstName: "A", LastName: "One", Role: models.RolePolice,
	})
	require.NoError(t, err)

	_, err = svc.CreateUser(context.Background(), actor(), CreateUserInput{
		Email: email, Password: testInitialPassword, FirstName: "B", LastName: "Two", Role: models.RoleJudge,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, utils.CodeConflict, appErr.Code)
}

func TestUserService_CreateUser_InvalidRoleRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	svc := newUserServiceForTest(t, app, &spyRecorder{})
	_, actor := adminActor(t, migrator)

	_, err := svc.CreateUser(context.Background(), actor(), CreateUserInput{
		Email: "bad-role@example.com", Password: testInitialPassword, FirstName: "A", LastName: "One", Role: "SUPERUSER",
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, utils.CodeBadRequest, appErr.Code)
}

func TestUserService_CreateUser_ShortPasswordRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	svc := newUserServiceForTest(t, app, &spyRecorder{})
	_, actor := adminActor(t, migrator)

	_, err := svc.CreateUser(context.Background(), actor(), CreateUserInput{
		Email: "shortpw@example.com", Password: "short", FirstName: "A", LastName: "One", Role: models.RolePolice,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, utils.CodeBadRequest, appErr.Code)
}

func TestUserService_CreateUser_NonAdminDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	recorder := &spyRecorder{}
	svc := newUserServiceForTest(t, app, recorder)

	for _, role := range []string{models.RolePolice, models.RoleForensics, models.RoleLawyer, models.RoleJudge} {
		nonAdminID := newUserWithRole(t, migrator, "non-admin-"+role+"@example.com", role)
		_, err := svc.CreateUser(context.Background(), authUser(nonAdminID, role), CreateUserInput{
			Email: "escapee-" + role + "@example.com", Password: testInitialPassword, FirstName: "A", LastName: "One", Role: models.RolePolice,
		})
		require.Error(t, err, "%s must not be able to create users", role)
		appErr, ok := utilsAsAppError(err)
		require.True(t, ok)
		assert.Equal(t, utils.CodeForbidden, appErr.Code)
	}
	assert.NotContains(t, recorder.actions(), "USER_CREATED", "a denied create must never record success")
}

func TestUserService_CreateUser_PasswordNeverInResponse(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	svc := newUserServiceForTest(t, app, &spyRecorder{})
	_, actor := adminActor(t, migrator)

	summary, err := svc.CreateUser(context.Background(), actor(), CreateUserInput{
		Email: "nopw@example.com", Password: testInitialPassword, FirstName: "A", LastName: "One", Role: models.RolePolice,
	})
	require.NoError(t, err)

	raw, err := json.Marshal(summary)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), testInitialPassword)
	assert.NotContains(t, string(raw), "password")
}

func TestUserService_UpdateRole_AdminCannotSelfEscalate(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	svc := newUserServiceForTest(t, app, &spyRecorder{})
	adminID, actor := adminActor(t, migrator)

	_, err := svc.UpdateRole(context.Background(), actor(), adminID, models.RolePolice)
	require.Error(t, err, "an ADMIN must never be able to change their OWN role through this operation")
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, utils.CodeForbidden, appErr.Code)
}

func TestUserService_UpdateRole_NonAdminDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	svc := newUserServiceForTest(t, app, &spyRecorder{})

	lawyerID := newUserWithRole(t, migrator, "lawyer-actor@example.com", models.RoleLawyer)
	targetID := newUserWithRole(t, migrator, "role-target@example.com", models.RolePolice)

	_, err := svc.UpdateRole(context.Background(), authUser(lawyerID, models.RoleLawyer), targetID, models.RoleJudge)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, utils.CodeForbidden, appErr.Code)
}

func TestUserService_UpdateRole_ReplacesRoleSet(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	recorder := &spyRecorder{}
	svc := newUserServiceForTest(t, app, recorder)
	_, actor := adminActor(t, migrator)

	targetID := newUserWithRole(t, migrator, "role-swap@example.com", models.RolePolice)

	summary, err := svc.UpdateRole(context.Background(), actor(), targetID, models.RoleJudge)
	require.NoError(t, err)
	assert.Equal(t, []string{models.RoleJudge}, summary.Roles, "the old role must be replaced, not added to")
	assert.Contains(t, recorder.actions(), "USER_ROLE_CHANGED")
}

func TestUserService_UpdateStatus_AdminCannotChangeOwnStatus(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	svc := newUserServiceForTest(t, app, &spyRecorder{})
	adminID, actor := adminActor(t, migrator)

	_, err := svc.UpdateStatus(context.Background(), actor(), adminID, models.UserStatusInactive)
	require.Error(t, err, "an admin must not be able to deactivate their own account")
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, utils.CodeForbidden, appErr.Code)
}

func TestUserService_UpdateStatus_NonAdminDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	svc := newUserServiceForTest(t, app, &spyRecorder{})

	judgeID := newUserWithRole(t, migrator, "judge-actor@example.com", models.RoleJudge)
	targetID := newUserWithRole(t, migrator, "status-target@example.com", models.RolePolice)

	_, err := svc.UpdateStatus(context.Background(), authUser(judgeID, models.RoleJudge), targetID, models.UserStatusInactive)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, utils.CodeForbidden, appErr.Code)
}

// TestUserService_UpdateStatus_DeactivationRevokesSessions is the token-
// theft-mitigation guarantee: an already-issued refresh session must not
// outlive its owner's deactivation.
func TestUserService_UpdateStatus_DeactivationRevokesSessions(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	recorder := &spyRecorder{}
	svc := newUserServiceForTest(t, app, recorder)
	authSvc := newTestAuthService(app)
	_, actor := adminActor(t, migrator)
	ctx := context.Background()

	targetID := seedUser(t, migrator, "deactivate-me@example.com", models.UserStatusActive, models.RolePolice)
	login, err := authSvc.Login(ctx, "deactivate-me@example.com", testPassword, "", "")
	require.NoError(t, err)

	_, err = svc.UpdateStatus(ctx, actor(), targetID, models.UserStatusInactive)
	require.NoError(t, err)
	assert.Contains(t, recorder.actions(), "USER_STATUS_CHANGED")

	_, err = authSvc.Refresh(ctx, login.RefreshToken, "", "")
	require.Error(t, err, "a session issued before deactivation must not still work afterward")

	_, err = authSvc.Login(ctx, "deactivate-me@example.com", testPassword, "", "")
	require.Error(t, err, "a deactivated user must not be able to log in at all")
}

func TestUserService_ResetPassword_NewPasswordWorksOldSessionsRevoked(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	recorder := &spyRecorder{}
	svc := newUserServiceForTest(t, app, recorder)
	authSvc := newTestAuthService(app)
	_, actor := adminActor(t, migrator)
	ctx := context.Background()

	targetID := seedUser(t, migrator, "reset-me@example.com", models.UserStatusActive, models.RolePolice)
	login, err := authSvc.Login(ctx, "reset-me@example.com", testPassword, "", "")
	require.NoError(t, err)

	const newPassword = "brand-new-password-1"
	err = svc.ResetPassword(ctx, actor(), targetID, newPassword)
	require.NoError(t, err)
	assert.Contains(t, recorder.actions(), "USER_PASSWORD_RESET")

	_, err = authSvc.Refresh(ctx, login.RefreshToken, "", "")
	require.Error(t, err, "the pre-reset session must be revoked")

	_, err = authSvc.Login(ctx, "reset-me@example.com", newPassword, "", "")
	require.NoError(t, err, "the new password must now work")

	_, err = authSvc.Login(ctx, "reset-me@example.com", testPassword, "", "")
	require.Error(t, err, "the OLD password must no longer work")
}

func TestUserService_GetUser_NotFound(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	svc := newUserServiceForTest(t, app, &spyRecorder{})
	_, actor := adminActor(t, migrator)

	_, err := svc.GetUser(context.Background(), actor(), uuid.New())
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, utils.CodeNotFound, appErr.Code)
}

func TestUserService_ListUsers_FiltersByRoleAndStatus(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	svc := newUserServiceForTest(t, app, &spyRecorder{})
	_, actor := adminActor(t, migrator)
	ctx := context.Background()

	_, err := svc.CreateUser(ctx, actor(), CreateUserInput{
		Email: "list-police@example.com", Password: testInitialPassword, FirstName: "A", LastName: "One", Role: models.RolePolice,
	})
	require.NoError(t, err)
	inactiveStatus := models.UserStatusInactive
	_, err = svc.CreateUser(ctx, actor(), CreateUserInput{
		Email: "list-judge@example.com", Password: testInitialPassword, FirstName: "B", LastName: "Two", Role: models.RoleJudge, Status: &inactiveStatus,
	})
	require.NoError(t, err)

	role := models.RolePolice
	result, err := svc.ListUsers(ctx, actor(), UserListFilter{Role: &role}, paginationForTest(1, 20))
	require.NoError(t, err)
	for _, u := range result.Users {
		assert.Contains(t, u.Roles, models.RolePolice)
	}

	status := models.UserStatusInactive
	result, err = svc.ListUsers(ctx, actor(), UserListFilter{Status: &status}, paginationForTest(1, 20))
	require.NoError(t, err)
	for _, u := range result.Users {
		assert.Equal(t, models.UserStatusInactive, u.Status)
	}
}

func TestUserService_ListUsers_NonAdminDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	svc := newUserServiceForTest(t, app, &spyRecorder{})

	forensicsID := newUserWithRole(t, migrator, "forensics-actor@example.com", models.RoleForensics)

	_, err := svc.ListUsers(context.Background(), authUser(forensicsID, models.RoleForensics), UserListFilter{}, paginationForTest(1, 20))
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, utils.CodeForbidden, appErr.Code)
}

func TestUserService_ListRoles_RequiresOnlyAuthentication(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	app := appPool(t)
	svc := newUserServiceForTest(t, app, &spyRecorder{})

	roles, err := svc.ListRoles(context.Background())
	require.NoError(t, err)
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = r.Name
	}
	assert.ElementsMatch(t, []string{models.RoleAdmin, models.RolePolice, models.RoleForensics, models.RoleLawyer, models.RoleJudge}, names)
}
