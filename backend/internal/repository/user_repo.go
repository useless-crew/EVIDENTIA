package repository

import (
	"context"

	"github.com/google/uuid"

	"evidentia/backend/db/generated"
)

// UserRepo is a thin wrapper around the generated user/role-assignment
// queries. It has no RLS concerns of its own (users/roles/permissions are
// not RLS-protected — see the migration) but still requires q to come from
// a WithTx call, since q is only ever produced that way.
type UserRepo struct {
	q *generated.Queries
}

func NewUserRepo(q *generated.Queries) *UserRepo {
	return &UserRepo{q: q}
}

func (r *UserRepo) Create(ctx context.Context, arg generated.CreateUserParams) (generated.CreateUserRow, error) {
	return r.q.CreateUser(ctx, arg)
}

func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (generated.GetUserByIDRow, error) {
	return r.q.GetUserByID(ctx, id)
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (generated.GetUserByEmailRow, error) {
	return r.q.GetUserByEmail(ctx, email)
}

// GetByEmailForAuth returns password_hash — see users.sql for why this is
// a separate, explicitly named query from GetByEmail.
func (r *UserRepo) GetByEmailForAuth(ctx context.Context, email string) (generated.GetUserByEmailForAuthRow, error) {
	return r.q.GetUserByEmailForAuth(ctx, email)
}

func (r *UserRepo) List(ctx context.Context, limit, offset int32) ([]generated.ListUsersRow, error) {
	return r.q.ListUsers(ctx, generated.ListUsersParams{Limit: limit, Offset: offset})
}

func (r *UserRepo) Count(ctx context.Context) (int64, error) {
	return r.q.CountUsers(ctx)
}

func (r *UserRepo) ListFiltered(ctx context.Context, arg generated.ListUsersFilteredParams) ([]generated.ListUsersFilteredRow, error) {
	return r.q.ListUsersFiltered(ctx, arg)
}

func (r *UserRepo) CountFiltered(ctx context.Context, arg generated.CountUsersFilteredParams) (int64, error) {
	return r.q.CountUsersFiltered(ctx, arg)
}

func (r *UserRepo) UpdateProfile(ctx context.Context, arg generated.UpdateUserProfileParams) (generated.UpdateUserProfileRow, error) {
	return r.q.UpdateUserProfile(ctx, arg)
}

func (r *UserRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) (generated.UpdateUserStatusRow, error) {
	return r.q.UpdateUserStatus(ctx, generated.UpdateUserStatusParams{ID: id, Status: status})
}

func (r *UserRepo) UpdatePasswordHash(ctx context.Context, id uuid.UUID, passwordHash string) error {
	return r.q.UpdateUserPasswordHash(ctx, generated.UpdateUserPasswordHashParams{ID: id, PasswordHash: passwordHash})
}

func (r *UserRepo) UpdateLastLogin(ctx context.Context, id uuid.UUID) error {
	return r.q.UpdateUserLastLogin(ctx, id)
}

// ---- Roles / permissions (kept here rather than a separate repo file:
// System 1 scaffolded only user_repo/case_repo/document_repo/audit_repo/
// certificate_repo — role/permission management is small enough, and
// closely enough tied to user identity, not to warrant a sixth file). ----

func (r *UserRepo) ListRoles(ctx context.Context) ([]generated.Role, error) {
	return r.q.ListRoles(ctx)
}

func (r *UserRepo) GetRoleByName(ctx context.Context, name string) (generated.Role, error) {
	return r.q.GetRoleByName(ctx, name)
}

func (r *UserRepo) AssignRole(ctx context.Context, userID, roleID uuid.UUID) error {
	return r.q.AssignRoleToUser(ctx, generated.AssignRoleToUserParams{UserID: userID, RoleID: roleID})
}

func (r *UserRepo) RemoveRole(ctx context.Context, userID, roleID uuid.UUID) error {
	return r.q.RemoveRoleFromUser(ctx, generated.RemoveRoleFromUserParams{UserID: userID, RoleID: roleID})
}

func (r *UserRepo) ListRolesForUser(ctx context.Context, userID uuid.UUID) ([]generated.Role, error) {
	return r.q.ListRolesForUser(ctx, userID)
}

func (r *UserRepo) ListPermissionsForRole(ctx context.Context, roleID uuid.UUID) ([]generated.Permission, error) {
	return r.q.ListPermissionsForRole(ctx, roleID)
}
