package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
)

// RequestCredentials 保留普通凭据读取与 Grok 请求级恢复的原顺序，固定依赖由 app 注入。
type RequestCredentials struct {
	Source             *accountcore.OpenAIExecutionCredentials
	HasGrokTokenSource bool
	Recovery           *accountcore.GrokCredentialRecovery
	Runtime            *accountcore.RuntimeBlockState
}

// CredentialObserver 只接收本次脱敏分类，HTTP Adapter 把它关联到现有 Ops 请求。
type CredentialObserver interface {
	ObserveCredentialFailure(int64, forwardcore.GrokCredentialFailure)
}

// @project-doc docs/interfaces/grok_upstream.md#grok_account_contract
func (s *RequestCredentials) Resolve(ctx context.Context, state *requeststate.CredentialBudget, output CredentialObserver, account *ExecutionAccount) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if account == nil {
		return "", "", errors.New("account is nil")
	}
	if !account.View().IsGrokOAuth() {
		return s.Source.Resolve(ctx, ExecutionRecord(account))
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if s == nil || !s.HasGrokTokenSource {
		return "", "", credentialFailover(output, account, forwardcore.GrokCredentialFailure{
			Scope:   forwardcore.GatewayFailureScopeProvider,
			Reason:  forwardcore.GrokCredentialReasonProviderConfig,
			Action:  forwardcore.NextAccountStop,
			Message: "Grok OAuth credential provider is unavailable",
		})
	}
	if s.blocked(account) {
		return "", "", credentialFailover(output, account, forwardcore.GrokCredentialFailure{
			Scope:   forwardcore.GatewayFailureScopeAccount,
			Reason:  forwardcore.GrokCredentialReasonAccountChanged,
			Action:  forwardcore.NextAccountRetry,
			Message: "Grok OAuth account is not currently schedulable",
		})
	}

	credentialCtx, cancel, budgetExpired := state.Acquire(ctx)
	if cancel != nil {
		defer cancel()
	}
	if budgetExpired {
		return "", "", credentialFailover(output, account, forwardcore.GrokCredentialFailure{
			Scope:   forwardcore.GatewayFailureScopeRequest,
			Reason:  forwardcore.GrokCredentialReasonFailoverTimeout,
			Action:  forwardcore.NextAccountStop,
			Message: "Grok OAuth credential failover budget exhausted",
		})
	}

	token, kind, err := s.Source.Resolve(credentialCtx, ExecutionRecord(account))
	if err == nil {
		if s.blocked(account) {
			return "", "", credentialFailover(output, account, forwardcore.GrokCredentialFailure{
				Scope:   forwardcore.GatewayFailureScopeAccount,
				Reason:  forwardcore.GrokCredentialReasonAccountChanged,
				Action:  forwardcore.NextAccountRetry,
				Message: "Grok OAuth account is not currently schedulable",
			})
		}
		return token, kind, nil
	}
	if parentErr := ctx.Err(); parentErr != nil {
		return "", "", parentErr
	}
	if credentialCtx.Err() != nil {
		return "", "", credentialFailover(output, account, forwardcore.GrokCredentialFailure{
			Scope:   forwardcore.GatewayFailureScopeRequest,
			Reason:  forwardcore.GrokCredentialReasonFailoverTimeout,
			Action:  forwardcore.NextAccountStop,
			Message: "Grok OAuth credential failover budget exhausted",
		})
	}

	class := forwardcore.ClassifyGrokCredentialFailure(account != nil && account.Record.ProxyID != nil, err)
	if snapshot, ok := accountcore.GrokCredentialFailureSnapshot(err); ok {
		class.SetSnapshot(&snapshot)
	}
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	if class.Permanent || class.Transient {
		freshToken, mutationErr := s.Recovery.Apply(credentialCtx, ExecutionRecord(account), credentialMutation(class))
		if freshToken != "" {
			return freshToken, "oauth", nil
		}
		if mutationErr != nil {
			if ctx.Err() != nil {
				return "", "", ctx.Err()
			}
			if credentialCtx.Err() != nil {
				return "", "", credentialFailover(output, account, forwardcore.GrokCredentialFailure{
					Scope:   forwardcore.GatewayFailureScopeRequest,
					Reason:  forwardcore.GrokCredentialReasonFailoverTimeout,
					Action:  forwardcore.NextAccountStop,
					Message: "Grok OAuth credential failover budget exhausted",
				})
			}
			if errors.Is(mutationErr, accountcore.ErrRefreshAccountStateChanged) {
				class = forwardcore.GrokCredentialFailure{
					Scope:   forwardcore.GatewayFailureScopeAccount,
					Reason:  forwardcore.GrokCredentialReasonAccountChanged,
					Action:  forwardcore.NextAccountRetry,
					Message: "Grok OAuth account eligibility changed",
				}
			} else if errors.Is(mutationErr, accountcore.ErrRefreshAccountRereadFailed) {
				class = forwardcore.GrokCredentialFailure{
					Scope:   forwardcore.GatewayFailureScopeProvider,
					Reason:  forwardcore.GrokCredentialReasonProviderDown,
					Action:  forwardcore.NextAccountStop,
					Message: "Grok OAuth account state is temporarily unavailable",
				}
			} else {
				class = forwardcore.GrokCredentialFailure{
					Scope:   forwardcore.GatewayFailureScopeProvider,
					Reason:  forwardcore.GrokCredentialReasonStateUpdate,
					Action:  forwardcore.NextAccountStop,
					Message: "Grok OAuth account state could not be updated safely",
				}
			}
		}
	}
	return "", "", credentialFailover(output, account, class)
}
func credentialFailover(output CredentialObserver, account *ExecutionAccount, class forwardcore.GrokCredentialFailure) error {
	if strings.TrimSpace(class.Message) == "" {
		class.Message = "Grok OAuth credentials are unavailable"
	}
	if output != nil {
		output.ObserveCredentialFailure(account.Record.ID, class)
	}
	return &forwardcore.UpstreamFailoverError{
		Stage:  forwardcore.GatewayFailureStageAccountAuth,
		Scope:  class.Scope,
		Reason: class.Reason, NextAccountAction: class.Action, ClientStatusCode: http.StatusServiceUnavailable,
		ClientMessage: forwardcore.GrokCredentialUnavailableClientMessage,
	}
}

// credentialMutation 只投影存储意图，网关的范围、动作和客户端文案不进入账号核心。
func credentialMutation(class forwardcore.GrokCredentialFailure) accountcore.GrokCredentialMutation {
	return accountcore.GrokCredentialMutation{
		Permanent:     class.Permanent,
		Transient:     class.Transient,
		VerifyMissing: class.Reason == forwardcore.GrokCredentialReasonMissing,
		VerifyProxy:   class.Reason == forwardcore.GrokCredentialReasonProxyInvalid,
		Reason:        string(class.Reason),
		Snapshot:      class.Snapshot(),
	}
}
func (s *RequestCredentials) blocked(value *ExecutionAccount) bool {
	if s == nil || value == nil {
		return false
	}
	return s.Runtime.Blocked(value.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(ExecutionRecord(value)) })
}
