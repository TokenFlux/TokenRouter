// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	context "context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
	slog "log/slog"
)

// ─── 常量 ──────────────────────────────────────────────────────────────────

// ─── 配置辅助函数 ─────────────────────────────────────────────────────────

// getDingTalkOAuthConfig 返回 DingTalk OAuth 最终生效配置。
// 优先从 settingSvc（settings 表）读取，回退到 h.cfg.DingTalk。
func (h *AuthHandler) getDingTalkOAuthConfig(ctx context.Context) (config.DingTalkConnectConfig, error) {
	if h != nil && h.settingSvc != nil {
		return h.settingSvc.GetDingTalkConnectOAuthConfig(ctx)
	}
	if h == nil || h.cfg == nil {
		return config.DingTalkConnectConfig{}, infraerrors.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
	}
	if !h.cfg.DingTalk.Enabled {
		return config.DingTalkConnectConfig{}, infraerrors.NotFound("OAUTH_DISABLED", "dingtalk oauth login is disabled")
	}
	return h.cfg.DingTalk, nil
}

// ─── Cookie 辅助函数（使用 dingtalk path）─────────────────────────────────

// ─── 钉钉 OAuth 启动 ───────────────────────────────────────────────────────

func (h *AuthHandler) DingTalkOAuthStart(c *gin.Context) { h.dingTalkHTTP().DingTalkOAuthStart(c) }

// ─── 构建钉钉授权地址 ─────────────────────────────────────────────────────

// ─── 查找兼容邮箱用户 ─────────────────────────────────────────────────────

// ─── 创建钉钉待选择会话 ───────────────────────────────────────────────────

// ─── 钉钉 OAuth 回调 ───────────────────────────────────────────────────────

func (h *AuthHandler) DingTalkOAuthCallback(c *gin.Context) {
	h.dingTalkHTTP().DingTalkOAuthCallback(c)
}

func buildDingTalkSyntheticEmail(userID string) string {
	return identitycore.DingTalkSyntheticEmail(userID)
}

func buildDingTalkUpstreamClaims(staff *DingTalkStaffInfo, unionID, corpID string) map[string]any {
	return identitycore.DingTalkUpstreamClaims(staff, unionID, corpID)
}

func checkDingTalkCorpAllowed(cfg config.DingTalkConnectConfig, corpID string) bool {
	return identitycore.DingTalkCorpAllowed(identitycore.DingTalkOAuthOptions(cfg), corpID)
}

func decideDingTalkStep34Strategy(policy string, stepErr error) (shouldFallback bool, isFatal bool) {
	return identitycore.DingTalkStep34Strategy(policy, stepErr)
}

func (h *AuthHandler) dingTalkClient(cfg config.DingTalkConnectConfig) *DingTalkClient {
	return h.dingTalkClients.ForConfig(dingTalkClientConfig{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, TokenURL: cfg.TokenURL, UserInfoURL: cfg.UserInfoURL})
}

// ─── 构建钉钉授权地址 ─────────────────────────────────────────────────────

// ─── 完成注册 ─────────────────────────────────────────────────────────────

func (h *AuthHandler) CompleteDingTalkOAuthRegistration(c *gin.Context) {
	h.dingTalkHTTP().CompleteDingTalkOAuthRegistration(c)
}

func (h *AuthHandler) CreateDingTalkOAuthAccount(c *gin.Context) {
	h.dingTalkHTTP().CreateDingTalkOAuthAccount(c)
}

func (h *AuthHandler) BindDingTalkOAuthLogin(c *gin.Context) {
	h.dingTalkHTTP().BindDingTalkOAuthLogin(c)
}

// ─── DingTalk 身份同步 ─────────────────────────────────────────────────────

func (h *AuthHandler) syncDingTalkIdentity(ctx context.Context, cfg config.DingTalkConnectConfig, client *DingTalkClient, userID int64, staff *DingTalkStaffInfo, syncUsername bool) {
	h.dingTalkSyncRuntime().Sync(ctx, identitycore.DingTalkOAuthOptions(cfg), client, userID, staff, syncUsername)
}

// maybeSyncDingTalkAfterRegistration 在通用 OAuth 注册路径完成后调用。
// 同步 4 个字段：users.username（首次） + dingtalk_name/email/department（每次）。
func (h *AuthHandler) maybeSyncDingTalkAfterRegistration(ctx context.Context, session *dbent.PendingAuthSession, userID int64) {
	h.dispatchDingTalkPendingSync(ctx, session, userID, true)
}

// maybeSyncDingTalkAfterLogin 在通用 OAuth 登录/绑定路径完成后调用。
// 仅刷新 3 个属性（dingtalk_name/email/department），不动 users.username。
func (h *AuthHandler) maybeSyncDingTalkAfterLogin(ctx context.Context, session *dbent.PendingAuthSession, userID int64) {
	h.dispatchDingTalkPendingSync(ctx, session, userID, false)
}

func (h *AuthHandler) dispatchDingTalkPendingSync(ctx context.Context, session *dbent.PendingAuthSession, userID int64, syncUsername bool) {
	h.dingTalkSyncRuntime().Pending(ctx, identitypostgres.PendingAuthSessionFromEntity(session), userID, syncUsername)
}

func dingTalkStaffFromClaims(claims map[string]any) *DingTalkStaffInfo {
	return identitycore.DingTalkProfileFromClaims(claims)
}

// dingTalkProfileSync 投影旧入口依赖，不持有后台任务或属性缓存。
func (h *AuthHandler) dingTalkProfileSync() *identitycore.DingTalkProfileSync {
	return &identitycore.DingTalkProfileSync{Users: identityUserCore(h.userService), Attributes: h.userAttributeService, Observe: func(level, message string, args ...any) {
		switch level {
		case "warn":
			slog.Warn(message, args...)
		default:
			slog.Info(message, args...)
		}
	}}
}

// dingTalkSyncRuntime 只投影旧入口依赖，后台工作仍进入 app 已绑定的统一跟踪器。
func (h *AuthHandler) dingTalkSyncRuntime() *identitycore.DingTalkSyncRuntime {
	return &identitycore.DingTalkSyncRuntime{
		LoadConfig: func(ctx context.Context) (identitycore.DingTalkOAuthOptions, error) {
			v, e := h.getDingTalkOAuthConfig(ctx)
			return identitycore.DingTalkOAuthOptions(v), e
		},
		Client: func(v identitycore.DingTalkOAuthOptions) identitycore.DingTalkOAuthClient {
			return h.dingTalkClient(config.DingTalkConnectConfig(v))
		},
		Profiles: h.dingTalkProfileSync(), Run: service.RunBackgroundTask, Observe: dingTalkSyncObserve,
	}
}
func dingTalkSyncObserve(level, message string, args ...any) {
	switch level {
	case "error":
		slog.Error(message, args...)
	case "debug":
		slog.Debug(message, args...)
	case "warn":
		slog.Warn(message, args...)
	default:
		slog.Info(message, args...)
	}
}

// dingTalkHTTP 保留旧入口形状，唯一流程与同步规则分别由 identity HTTP 和核心拥有。
func (h *AuthHandler) dingTalkHTTP() *identityhttp.DingTalkHandler {
	runtime := h.dingTalkSyncRuntime()
	options := identityhttp.DingTalkHTTPOptions{LoadConfig: runtime.LoadConfig}
	if h.settingSvc != nil {
		options.RegistrationEnabled = h.settingSvc.IsRegistrationEnabled
	}
	return identityhttp.NewDingTalkHandler(h.pendingHTTP(), h.oauthBindHTTP(), runtime, options)
}
