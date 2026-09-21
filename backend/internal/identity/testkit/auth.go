// Package testkit 只组合身份契约测试所需的原生端口，不持有认证规则或缓存副本。
package testkit

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
)

// AuthOptions 保留旧测试构造的启动参数投影，不读取环境或运行设置。
func AuthOptions(cfg *config.Config) *identity.AuthOptions {
	if cfg == nil {
		return nil
	}
	out := &identity.AuthOptions{}
	out.Default.UserBalance = cfg.Default.UserBalance
	out.Default.UserConcurrency = cfg.Default.UserConcurrency
	out.JWT = identity.SessionOptions{Secret: cfg.JWT.Secret, ExpireHour: cfg.JWT.ExpireHour, AccessTokenExpireMinutes: cfg.JWT.AccessTokenExpireMinutes, RefreshTokenExpireDays: cfg.JWT.RefreshTokenExpireDays}
	out.Server.Mode = cfg.Server.Mode
	out.Turnstile.Required = cfg.Turnstile.Required
	return out
}

// Auth 绑定原事务适配与用户仓储实际支持的可选能力，构造不启动任务。
func Auth(client *dbent.Client, deps *identity.AuthDependencies) *identity.AuthService {
	if deps.Observer.Log == nil {
		deps.Observer.Log = logging.LegacyPrintf
	}
	if source, ok := deps.Users.(identity.RegistrationEmailDomainRepository); ok {
		deps.DomainRegistration = source
	}
	if source, ok := deps.Users.(identity.AuthNormalizedEmailBindingConflictChecker); ok {
		deps.NormalizedEmailConflict = source
	}
	if source, ok := deps.Users.(identity.AuthEmailIdentityAliasGuardRepository); ok {
		deps.EmailAliasGuard = source
	}
	if source, ok := deps.Users.(identity.EmailAliasLookupRepository); ok {
		deps.AliasLookup = source
	}
	if source, ok := deps.Users.(identity.EmailAliasOwnerLookupRepository); ok {
		deps.AliasOwner = source
	}
	state := identitypostgres.NewAuthState(client, deps)
	core := identity.NewAuthService(deps, &identitypostgres.AuthRepository{State: state})
	state.Rules = core
	return core
}

// Email 保留未配置邮件挑战时真正的 nil 接口。
func Email(value *identity.EmailChallenges) identity.AuthEmail {
	if value == nil {
		return nil
	}
	return value
}

// EmailQueue 保留可选队列的 nil 语义，不创建后台执行器。
func EmailQueue(value *notification.EmailQueueService) identity.AuthEmailQueue {
	if value == nil {
		return nil
	}
	return value
}

// Promo 只暴露身份用例所需的推广端口。
func Promo(value *promotion.PromoService) identity.AuthPromo {
	if value == nil {
		return nil
	}
	return value
}

// Affiliate 只转换返回值形状，返利与关系规则仍由推广模块执行。
func Affiliate(value *promotion.AffiliateService) identity.AuthAffiliate {
	if value == nil {
		return nil
	}
	return affiliate{value}
}

type affiliate struct{ source *promotion.AffiliateService }

func (a affiliate) EnsureUserAffiliate(ctx context.Context, id int64) error {
	_, err := a.source.EnsureUserAffiliate(ctx, id)
	return err
}

func (a affiliate) BindInviterByCode(ctx context.Context, id int64, code string) error {
	return a.source.BindInviterByCode(ctx, id, code)
}
