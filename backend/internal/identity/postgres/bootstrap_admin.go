package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// CreateInitialAdmin 保留两次计数与原写入边界，不覆盖已有用户密码。
func CreateInitialAdmin(ctx context.Context, db *sql.DB, input identity.InitialAdminInput, password func() (string, error)) (bool, string, error) {
	var total, admins int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(1) FROM users").Scan(&total); err != nil {
		return false, "", err
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(1) FROM users WHERE role = $1", identity.RoleAdmin).Scan(&admins); err != nil {
		return false, "", err
	}
	create, reason := identity.DecideAdminBootstrap(total, admins)
	if !create {
		return false, reason, nil
	}
	if strings.TrimSpace(input.Password) == "" {
		value, err := password()
		if err != nil {
			return false, "", fmt.Errorf("failed to generate admin password: %w", err)
		}
		input.Password = value
	}
	user := &identity.User{Email: input.Email, Role: identity.RoleAdmin, Status: identity.StatusActive, Balance: 0, Concurrency: input.Concurrency, CreatedAt: input.Now(), UpdatedAt: input.Now()}
	if err := user.SetPassword(input.Password); err != nil {
		return false, "", err
	}
	_, err := db.ExecContext(ctx, `INSERT INTO users (email,password_hash,role,balance,concurrency,status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, user.Email, user.PasswordHash, user.Role, user.Balance, user.Concurrency, user.Status, user.CreatedAt, user.UpdatedAt)
	if err != nil {
		return false, "", err
	}
	return true, reason, nil
}
