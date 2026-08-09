package repo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ceyewan/genesis/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ceyewan/resonance/model"
)

type identityRepo struct {
	db db.DB
}

func NewIdentityRepo(database db.DB) (IdentityRepo, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	return &identityRepo{db: database}, nil
}

func (r *identityRepo) CreateIdentity(ctx context.Context, user *model.User, membership *model.TenantMembership, roles []string) error {
	if user == nil || membership == nil {
		return fmt.Errorf("user and membership cannot be nil")
	}
	if err := validateTenantUsername(membership.TenantID, membership.Username); err != nil {
		return err
	}
	if strings.TrimSpace(user.Username) == "" || user.Username != membership.Username {
		return fmt.Errorf("user and membership username must match")
	}
	if !validMembershipStatus(membership.Status) {
		return fmt.Errorf("invalid tenant membership status")
	}
	if membership.Version == 0 {
		membership.Version = 1
	}
	if membership.Version != 1 {
		return fmt.Errorf("new tenant membership version must be 1")
	}
	roles = normalizedRoles(roles)
	if len(roles) == 0 {
		return fmt.Errorf("at least one system role is required")
	}
	for _, role := range roles {
		if !validSystemRole(role) {
			return fmt.Errorf("%w: %s", ErrUnknownSystemRole, role)
		}
	}

	return r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		if err := tx.Create(membership).Error; err != nil {
			return fmt.Errorf("create tenant membership: %w", err)
		}
		bindings := make([]*model.SystemRoleBinding, 0, len(roles))
		for _, role := range roles {
			bindings = append(bindings, &model.SystemRoleBinding{
				TenantID: membership.TenantID,
				Username: membership.Username,
				Role:     role,
			})
		}
		if err := tx.Create(&bindings).Error; err != nil {
			return fmt.Errorf("create system role bindings: %w", err)
		}
		return nil
	})
}

func (r *identityRepo) CreateTenantMembership(ctx context.Context, membership *model.TenantMembership) error {
	if membership == nil {
		return fmt.Errorf("tenant membership cannot be nil")
	}
	if err := validateTenantUsername(membership.TenantID, membership.Username); err != nil {
		return err
	}
	if !validMembershipStatus(membership.Status) {
		return fmt.Errorf("invalid tenant membership status")
	}
	if membership.Version == 0 {
		membership.Version = 1
	}
	if membership.Version != 1 {
		return fmt.Errorf("new tenant membership version must be 1")
	}
	return r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.User{}).Where("username = ?", membership.Username).Count(&count).Error; err != nil {
			return fmt.Errorf("check tenant member user: %w", err)
		}
		if count != 1 {
			return fmt.Errorf("%w: %s", ErrUserNotFound, membership.Username)
		}
		if err := tx.Create(membership).Error; err != nil {
			return fmt.Errorf("create tenant membership: %w", err)
		}
		return nil
	})
}

func (r *identityRepo) GetTenantMembership(ctx context.Context, tenantID, username string) (*model.TenantMembership, error) {
	if err := validateTenantUsername(tenantID, username); err != nil {
		return nil, err
	}
	var membership model.TenantMembership
	if err := r.db.DB(ctx).Where("tenant_id = ? AND username = ?", tenantID, username).Take(&membership).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTenantMembershipNotFound
		}
		return nil, fmt.Errorf("get tenant membership: %w", err)
	}
	return &membership, nil
}

func (r *identityRepo) UpdateTenantMembershipStatus(
	ctx context.Context,
	tenantID, username, status string,
	expectedVersion int64,
) (*model.TenantMembership, error) {
	if err := validateTenantUsername(tenantID, username); err != nil {
		return nil, err
	}
	if !validMembershipStatus(status) {
		return nil, fmt.Errorf("invalid tenant membership status")
	}
	if expectedVersion < 1 {
		return nil, fmt.Errorf("expected_version must be positive")
	}
	result := r.db.DB(ctx).Model(&model.TenantMembership{}).
		Where("tenant_id = ? AND username = ? AND version = ?", tenantID, username, expectedVersion).
		Updates(map[string]any{"status": status, "version": gorm.Expr("version + 1")})
	if result.Error != nil {
		return nil, fmt.Errorf("update tenant membership: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		if _, err := r.GetTenantMembership(ctx, tenantID, username); err != nil {
			return nil, err
		}
		return nil, ErrIdentityVersionConflict
	}
	return r.GetTenantMembership(ctx, tenantID, username)
}

func (r *identityRepo) CreateSystemRoleBinding(ctx context.Context, binding *model.SystemRoleBinding) error {
	if binding == nil {
		return fmt.Errorf("system role binding cannot be nil")
	}
	if err := validateTenantUsername(binding.TenantID, binding.Username); err != nil {
		return err
	}
	if !validSystemRole(binding.Role) {
		return fmt.Errorf("%w: %s", ErrUnknownSystemRole, binding.Role)
	}
	return r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.TenantMembership{}).
			Where("tenant_id = ? AND username = ?", binding.TenantID, binding.Username).
			Count(&count).Error; err != nil {
			return fmt.Errorf("check tenant membership: %w", err)
		}
		if count != 1 {
			return ErrTenantMembershipNotFound
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(binding)
		if result.Error != nil {
			return fmt.Errorf("create system role binding: %w", result.Error)
		}
		if result.RowsAffected == 1 {
			versionResult := tx.Model(&model.TenantMembership{}).
				Where("tenant_id = ? AND username = ?", binding.TenantID, binding.Username).
				Update("version", gorm.Expr("version + 1"))
			if versionResult.Error != nil {
				return fmt.Errorf("advance membership version for system role: %w", versionResult.Error)
			}
			if versionResult.RowsAffected != 1 {
				return ErrTenantMembershipNotFound
			}
		}
		return nil
	})
}

func (r *identityRepo) DeleteSystemRoleBinding(ctx context.Context, tenantID, username, role string) error {
	if err := validateTenantUsername(tenantID, username); err != nil {
		return err
	}
	if !validSystemRole(role) {
		return fmt.Errorf("%w: %s", ErrUnknownSystemRole, role)
	}
	return r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		result := tx.Where("tenant_id = ? AND username = ? AND role = ?", tenantID, username, role).
			Delete(&model.SystemRoleBinding{})
		if result.Error != nil {
			return fmt.Errorf("delete system role binding: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		versionResult := tx.Model(&model.TenantMembership{}).
			Where("tenant_id = ? AND username = ?", tenantID, username).
			Update("version", gorm.Expr("version + 1"))
		if versionResult.Error != nil {
			return fmt.Errorf("advance membership version for removed system role: %w", versionResult.Error)
		}
		if versionResult.RowsAffected != 1 {
			return ErrTenantMembershipNotFound
		}
		return nil
	})
}

func (r *identityRepo) ListSystemRoleBindings(ctx context.Context, tenantID, username string) ([]*model.SystemRoleBinding, error) {
	if err := validateTenantUsername(tenantID, username); err != nil {
		return nil, err
	}
	var bindings []*model.SystemRoleBinding
	if err := r.db.DB(ctx).Where("tenant_id = ? AND username = ?", tenantID, username).
		Order("role ASC").Find(&bindings).Error; err != nil {
		return nil, fmt.Errorf("list system role bindings: %w", err)
	}
	return bindings, nil
}

func (r *identityRepo) ListTenantMemberships(
	ctx context.Context,
	tenantID string,
	limit int,
) ([]*model.TenantMembership, error) {
	if err := validateTenantID(tenantID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("tenant membership limit must be between 1 and 100")
	}
	var memberships []*model.TenantMembership
	if err := r.db.DB(ctx).
		Where("tenant_id = ?", tenantID).
		Order("username ASC").
		Limit(limit).
		Find(&memberships).Error; err != nil {
		return nil, fmt.Errorf("list tenant memberships: %w", err)
	}
	return memberships, nil
}

func (r *identityRepo) ResolveTenantAuthorization(ctx context.Context, tenantID, username string) (*TenantAuthorization, error) {
	if err := validateTenantUsername(tenantID, username); err != nil {
		return nil, err
	}
	type authorizationRow struct {
		TenantID string  `gorm:"column:tenant_id"`
		Username string  `gorm:"column:username"`
		Status   string  `gorm:"column:status"`
		Version  int64   `gorm:"column:version"`
		Role     *string `gorm:"column:role"`
	}
	var rows []authorizationRow
	if err := r.db.DB(ctx).Raw(`
		SELECT m.tenant_id, m.username, m.status, m.version, b.role
		FROM t_tenant_membership AS m
		LEFT JOIN t_system_role_binding AS b
		  ON b.tenant_id = m.tenant_id AND b.username = m.username
		WHERE m.tenant_id = ? AND m.username = ?
		ORDER BY b.role ASC`, tenantID, username).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("resolve tenant authorization: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrTenantMembershipNotFound
	}
	authorization := &TenantAuthorization{Membership: &model.TenantMembership{
		TenantID: rows[0].TenantID,
		Username: rows[0].Username,
		Status:   rows[0].Status,
		Version:  rows[0].Version,
	}}
	for _, row := range rows {
		if row.Role != nil && *row.Role != "" {
			authorization.Roles = append(authorization.Roles, *row.Role)
		}
	}
	return authorization, nil
}

func (r *identityRepo) Close() error { return nil }

func validateTenantUsername(tenantID, username string) error {
	if err := validateTenantID(tenantID); err != nil {
		return err
	}
	if strings.TrimSpace(username) == "" || strings.TrimSpace(username) != username || len(username) > 64 {
		return fmt.Errorf("username must contain 1 to 64 bytes")
	}
	return nil
}

func validateTenantID(tenantID string) error {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(tenantID) != tenantID || len(tenantID) > 64 {
		return fmt.Errorf("tenant_id must contain 1 to 64 bytes")
	}
	return nil
}

func validMembershipStatus(status string) bool {
	return status == model.TenantMembershipStatusActive || status == model.TenantMembershipStatusDisabled
}

func validSystemRole(role string) bool {
	return role == model.SystemRoleUser || role == model.SystemRoleIAMAdmin
}

func normalizedRoles(roles []string) []string {
	seen := make(map[string]struct{}, len(roles))
	result := make([]string, 0, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		result = append(result, role)
	}
	sort.Strings(result)
	return result
}
