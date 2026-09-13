// 本文件拥有账号错误策略与状态变更顺序；供应商只传入解析结果。
package account

import (
	"context"
	"strconv"
	"time"
)

// ErrorPolicyResult 表示错误策略检查的结果
type ErrorPolicyResult int

const (
	ErrorPolicyNone            ErrorPolicyResult = iota // 未命中任何策略，继续默认逻辑
	ErrorPolicyCustomSkipped                            // 自定义错误码开启但未命中，返回通用错误
	ErrorPolicyCustomMatched                            // 自定义错误码命中，停止调度
	ErrorPolicyTempUnscheduled                          // 临时不可调度规则命中
	ErrorPolicyPoolBypassed                             // 池模式跳过默认本地状态，继续响应分类
	// ErrorPolicyMatched 保留旧测试与外部调用方的枚举别名。
	ErrorPolicyMatched = ErrorPolicyCustomMatched
	// ErrorPolicySkipped 保留旧测试与外部调用方的枚举别名。
	ErrorPolicySkipped = ErrorPolicyCustomSkipped
)

// UpstreamErrorDecision 汇总显式策略和默认账号状态处理结果。
// 网关必须使用 Policy 区分池模式绕过与自定义错误码未命中，不能只依赖 StopScheduling。
type UpstreamErrorDecision struct {
	Policy         ErrorPolicyResult
	StopScheduling bool
}

// ShouldReturnGenericError 表示应按自定义错误码约定返回通用 500，且不切换账号。
func (d UpstreamErrorDecision) ShouldReturnGenericError() bool {
	return d.Policy == ErrorPolicyCustomSkipped
}

// ShouldFailover 表示当前请求应切换账号。池模式配置的重试状态码可以把原本的
// 非故障转移状态提升为故障转移；显式策略命中则始终切换账号。
func (d UpstreamErrorDecision) ShouldFailover(account *Record, statusCode int, defaultFailover bool) bool {
	if d.Policy == ErrorPolicyCustomSkipped {
		return false
	}
	if d.Policy == ErrorPolicyCustomMatched || d.Policy == ErrorPolicyTempUnscheduled {
		return true
	}
	if d.Policy == ErrorPolicyPoolBypassed {
		return defaultFailover || (account != nil && account.IsPoolModeRetryableStatus(statusCode))
	}
	return defaultFailover || d.StopScheduling
}

// ShouldFailoverWithDefaults 分别保留普通账号的入口既有切号规则和池模式的上游错误分类。
// 这样显式策略可以跨入口统一，又不会把某个入口原本只回写客户端的状态扩大成普通账号切号。
func (d UpstreamErrorDecision) ShouldFailoverWithDefaults(
	account *Record,
	statusCode int,
	nonPoolDefault bool,
	poolDefault bool,
) bool {
	switch d.Policy {
	case ErrorPolicyCustomSkipped:
		return false
	case ErrorPolicyCustomMatched, ErrorPolicyTempUnscheduled:
		return true
	case ErrorPolicyPoolBypassed:
		return poolDefault || (account != nil && account.IsPoolModeRetryableStatus(statusCode))
	default:
		return nonPoolDefault
	}
}

// RetryableOnSameAccount 仅允许未命中显式策略的池模式错误在当前账号上重试。
func (d UpstreamErrorDecision) RetryableOnSameAccount(account *Record, statusCode int) bool {
	return d.Policy == ErrorPolicyPoolBypassed && account != nil && account.IsPoolModeRetryableStatus(statusCode)
}

// ErrorDecisionWithoutPersistence 在错误状态服务未注入时保留纯配置决策。
// 该路径不能写数据库，但仍必须识别自定义错误码和池模式，否则会丢失通用错误或同账号重试语义。
func ErrorDecisionWithoutPersistence(account *Record, statusCode int) UpstreamErrorDecision {
	decision := UpstreamErrorDecision{Policy: ErrorPolicyNone}
	if account == nil {
		return decision
	}
	if account.IsCustomErrorCodesEnabled() {
		if account.ShouldHandleErrorCode(statusCode) {
			decision.Policy = ErrorPolicyCustomMatched
			decision.StopScheduling = true
		} else {
			decision.Policy = ErrorPolicyCustomSkipped
		}
		return decision
	}
	if account.IsPoolMode() {
		decision.Policy = ErrorPolicyPoolBypassed
	}
	return decision
}

// CheckErrorPolicy 检查自定义错误码和临时不可调度规则。
// 自定义错误码开启时覆盖后续所有逻辑（包括临时不可调度）。
func (s *HealthService) CheckErrorPolicy(ctx context.Context, account *Record, statusCode int, responseBody []byte, model string, escalateRepeated401 bool) ErrorPolicyResult {
	if account == nil {
		return ErrorPolicyNone
	}
	if account.IsCustomErrorCodesEnabled() {
		if account.ShouldHandleErrorCode(statusCode) {
			return ErrorPolicyCustomMatched
		}
		s.options.Info("account_error_code_skipped", "account_id", account.ID, "status_code", statusCode)
		return ErrorPolicyCustomSkipped
	}
	if account.IsPoolMode() {
		// 池模式下管理员显式配置的临时不可调度规则仍优先；401 不执行默认的
		// 二次认证错误升级，否则会违背池模式不写默认账号错误状态的约定。
		if s.TryTempUnschedulable(ctx, account, statusCode, responseBody, false, model) {
			return ErrorPolicyTempUnscheduled
		}
		return ErrorPolicyPoolBypassed
	}
	// 普通账号保持原全局过载回退，显式账号策略优先。
	if statusCode == 529 {
		return ErrorPolicyCustomMatched
	}
	if s.TryTempUnschedulable(ctx, account, statusCode, responseBody, escalateRepeated401, model) {
		return ErrorPolicyTempUnscheduled
	}
	return ErrorPolicyNone
}

// ApplyAuthenticationFailure 处理认证类错误(401/403)，停止账号调度
func (s *HealthService) ApplyAuthenticationFailure(ctx context.Context, account *Record, errorMsg string) {
	s.notifyAccountSchedulingBlocked(account, time.Time{}, "auth_error")
	if err := s.accountRepo.SetError(ctx, account.ID, errorMsg); err != nil {
		s.options.Warn("account_set_error_failed", "account_id", account.ID, "error", err)
		return
	}
	s.options.Warn("account_disabled_auth_error", "account_id", account.ID, "error", errorMsg)
}

// ApplyCustomErrorCode 处理自定义错误码，停止账号调度
func (s *HealthService) ApplyCustomErrorCode(ctx context.Context, account *Record, statusCode int, errorMsg string) {
	msg := "Custom error code " + strconv.Itoa(statusCode) + ": " + errorMsg
	s.notifyAccountSchedulingBlocked(account, time.Time{}, "custom_error_code")
	if err := s.accountRepo.SetError(ctx, account.ID, msg); err != nil {
		s.options.Warn("account_set_error_failed", "account_id", account.ID, "status_code", statusCode, "error", err)
		return
	}
	s.options.Warn("account_disabled_custom_error", "account_id", account.ID, "status_code", statusCode, "error", errorMsg)
}

// ApplyOverload 处理529过载错误
// 根据配置决定是否暂停账号调度及冷却时长
func (s *HealthService) ApplyOverload(ctx context.Context, account *Record) {
	var settings *OverloadCooldownSettings
	if s.options.OverloadSettings != nil {
		var err error
		settings, err = s.options.OverloadSettings(ctx)
		if err != nil {
			s.options.Warn("overload_settings_read_failed", "account_id", account.ID, "error", err)
			settings = nil
		}
	}
	// 回退到配置文件
	if settings == nil {
		cooldown := s.options.OverloadMinutes
		if cooldown <= 0 {
			cooldown = 10
		}
		settings = &OverloadCooldownSettings{Enabled: true, CooldownMinutes: cooldown}
	}

	if !settings.Enabled {
		s.options.Info("account_529_ignored", "account_id", account.ID, "reason", "overload_cooldown_disabled")
		return
	}

	cooldownMinutes := settings.CooldownMinutes
	if cooldownMinutes <= 0 {
		cooldownMinutes = 10
	}

	until := s.options.Now().Add(time.Duration(cooldownMinutes) * time.Minute)
	s.notifyAccountSchedulingBlocked(account, until, "529")
	if err := s.accountRepo.SetOverloaded(ctx, account.ID, until); err != nil {
		s.options.Warn("overload_set_failed", "account_id", account.ID, "error", err)
		return
	}

	s.options.Info("account_overloaded", "account_id", account.ID, "until", until)
}
