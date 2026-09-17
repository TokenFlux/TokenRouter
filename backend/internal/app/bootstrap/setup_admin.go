package bootstrap

import (
	"context"
	"database/sql"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	ip "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
)

// CreateInitialAdmin 只装配安装所需身份能力，不构造认证运行时。
func CreateInitialAdmin(ctx context.Context, db *sql.DB, input identity.InitialAdminInput, password func() (string, error)) (bool, string, error) {
	return ip.CreateInitialAdmin(ctx, db, input, password)
}
