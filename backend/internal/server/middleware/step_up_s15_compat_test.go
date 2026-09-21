// S15：旧实体仅保留为原中间件测试的局部输入适配，生产装配已直接调用 identity；S16 删除。
// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	"github.com/TokenFlux/TokenRouter/internal/identity"

	context "context"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	gin "github.com/gin-gonic/gin"
)

// StepUpAuthMiddleware 敏感操作 step-up 2FA 门控中间件类型。

// stepUpGrantChecker 抽象 TOTP step-up 授权检查能力（由 TotpService 实现）。
type stepUpGrantChecker interface {
	HasStepUpGrant(ctx context.Context, userID int64, sessionKey string) (bool, error)
}

// stepUpUserReader 抽象用户读取能力（检查 TOTP 是否启用）。
type stepUpUserReader interface {
	GetByID(ctx context.Context, id int64) (*identity.User, error)
}

// stepUpSettingReader 抽象 step-up 功能开关读取能力（由 SettingService 实现）。
type stepUpSettingReader interface {
	IsStepUpEnabled(ctx context.Context) bool
}

// StepUpSessionKey 委托身份 HTTP 适配。
func StepUpSessionKey(c *gin.Context, userID int64) string {
	return identityhttp.StepUpSessionKey(c, userID)
}

// NewStepUpAuthMiddleware 创建敏感操作 step-up 2FA 门控中间件。
//
// 功能开关 step_up_enabled（默认关闭）关闭时中间件直接放行，行为与门控引入前一致。
// 开启时的通过条件（全部满足）：
//  1. 必须是 JWT 认证的真人会话——admin API key（机器凭证）一律拒绝
//  2. 当前用户已启用 TOTP（未启用则拒绝并提示先启用 2FA）
//  3. 当前会话在有效期内完成过 TOTP step-up 验证（POST /api/v1/user/totp/step-up）
//
// 失败响应使用可区分的错误码，前端据此弹出 TOTP 验证对话框后重试。
func NewStepUpAuthMiddleware(
	totpService *identity.TotpService,
	userService *identity.UserService,
	settingService *identity.RuntimeSettings,
) StepUpAuthMiddleware {
	return StepUpAuthMiddleware(stepUpAuth(totpService, userService, stepUpSettingsOrNil(settingService)))
}

// stepUpSettingsOrNil 将可能为 nil 的具体指针归一化为接口，
// 避免 typed-nil 装箱后绕过 enforceStepUp 内的 nil 判断。
func stepUpSettingsOrNil(settingService *identity.RuntimeSettings) stepUpSettingReader {
	if settingService == nil {
		return nil
	}
	return settingService
}

// stepUpAuth 委托身份 HTTP 适配，保留旧调用签名。
func stepUpAuth(grantChecker stepUpGrantChecker, userReader stepUpUserReader, settings stepUpSettingReader) gin.HandlerFunc {
	return identityhttp.StepUpAuth(grantChecker, identityHTTPUser{userReader}, settings)
}

// EnforceStepUp 对当前请求执行与 StepUpAuthMiddleware 相同语义的 step-up 门控，
// 供 handler 在需要按请求内容条件触发时调用（如仅当把用户角色提升为管理员时）。
// 校验失败时写入错误响应并中止请求，返回 false；通过返回 true。
func EnforceStepUp(
	c *gin.Context,
	totpService *identity.TotpService,
	userService *identity.UserService,
	settingService *identity.RuntimeSettings,
) bool {
	return enforceStepUp(c, totpService, userService, stepUpSettingsOrNil(settingService))
}

// EnforceStepUpAlways 与 EnforceStepUp 语义相同但不读取功能开关，无条件执行门控。
// 供调用方已确知门控必须生效的场景使用（如"关闭 step-up 开关"本身：调用方刚从
// 持久化设置读到开关为开启状态，不应依赖二次读取——读取失败会导致门控被跳过）。
func EnforceStepUpAlways(
	c *gin.Context,
	totpService *identity.TotpService,
	userService *identity.UserService,
) bool {
	return enforceStepUp(c, totpService, userService, nil)
}

// enforceStepUp 委托身份 HTTP 适配，保留旧调用签名。
func enforceStepUp(c *gin.Context, grantChecker stepUpGrantChecker, userReader stepUpUserReader, settings stepUpSettingReader) bool {
	return identityhttp.EnforceStepUp(c, grantChecker, identityHTTPUser{userReader}, settings)
}
