package account

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// ClientRejectionObservation 是上游错误分类的只读结果，账号决定是否停调。
type ClientRejectionObservation struct {
	Message                      string
	OrganizationDisabled         bool
	CreditBalanceExhausted       bool
	IdentityVerificationRequired bool
	WorkspaceDeactivated         bool
}

// ApplyBadRequest 保留原状态转换及错误消息，不解析供应商报文。
func (s *HealthService) ApplyBadRequest(ctx context.Context, account *Record, observation ClientRejectionObservation) bool {
	// "organization has been disabled" → 永久禁用
	if observation.OrganizationDisabled {
		msg := "Organization disabled (400): " + observation.Message
		s.ApplyAuthenticationFailure(ctx, account, msg)
		return true
	} else if account.Platform == capability.PlatformAnthropic && observation.CreditBalanceExhausted {
		// Anthropic API key 余额不足（语义等同 402），停止调度
		msg := "Credit balance exhausted (400): " + observation.Message
		s.ApplyAuthenticationFailure(ctx, account, msg)
		return true
	} else if observation.IdentityVerificationRequired {
		// KYC 身份验证要求 → 永久禁用，账号需完成身份验证后才能恢复
		msg := "Identity verification required (400): " + observation.Message
		s.ApplyAuthenticationFailure(ctx, account, msg)
		return true
	}
	// 其他 400 错误（如参数问题）不处理，不禁用账号
	return false
}

// ApplyPaymentRequired 保留原状态转换及错误消息，不解析供应商报文。
func (s *HealthService) ApplyPaymentRequired(ctx context.Context, account *Record, observation ClientRejectionObservation) bool {
	// 国产供应商：余额不足是可恢复状态（充值/检测恢复后由周期任务自动解除），
	// 不能走 handleAuthError 永久置 status=error。改为可恢复的临时停调。
	if account.IsCNProvider() {
		s.ApplyCNInsufficientBalance(ctx, account, observation.Message)
		return true
	}
	// OpenAI: deactivated_workspace 表示工作区已停用，直接标记 error
	if account.Platform == capability.PlatformOpenAI && observation.WorkspaceDeactivated {
		msg := "Workspace deactivated (402): workspace has been deactivated"
		s.ApplyAuthenticationFailure(ctx, account, msg)
		return true
	}
	// 支付要求：余额不足或计费问题，停止调度
	msg := "Payment required (402): insufficient balance or billing issue"
	if observation.Message != "" {
		msg = "Payment required (402): " + observation.Message
	}
	s.ApplyAuthenticationFailure(ctx, account, msg)
	return true
}
