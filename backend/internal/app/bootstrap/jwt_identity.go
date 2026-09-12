package bootstrap

import (
	"database/sql"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
)

// JWTIdentity 只持有维护命令需要的用户读取和访问令牌签发，不构造刷新会话、认证图或后台 worker。
type JWTIdentity struct {
	Users  *identitypostgres.UserStore
	Tokens *identity.SessionService
}

// NewJWTIdentity 复用已经引导的连接，命令继续拥有唯一关闭责任。
func NewJWTIdentity(client *dbent.Client, db *sql.DB, cfg *config.Config) JWTIdentity {
	users := identitypostgres.NewUserStore(client, db)
	options := identity.SessionOptions{Now: time.Now, Secret: cfg.JWT.Secret, ExpireHour: cfg.JWT.ExpireHour, AccessTokenExpireMinutes: cfg.JWT.AccessTokenExpireMinutes, RefreshTokenExpireDays: cfg.JWT.RefreshTokenExpireDays}
	return JWTIdentity{Users: users, Tokens: identity.NewSessionService(options, users, nil, nil, nil)}
}
