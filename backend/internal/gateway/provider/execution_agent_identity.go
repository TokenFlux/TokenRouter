package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type agentIdentityWSConnectionInvalidator interface {
	InvalidateAgentIdentityWSConnections(accountID int64)
}

// 兼容入口只转换记录和写回时机；锁、复查与登记规则由唯一账号协调器执行。
func ensureAgentIdentityTaskForAccount(ctx context.Context, coordinator *acctcore.OpenAITaskCoordinator, register func(context.Context, *acctcore.Record) (string, error), repo ExecutionAccountStore, wsInvalidator agentIdentityWSConnectionInvalidator, taskMu *sync.Mutex, value *ExecutionAccount, expectedTaskID string) error {
	input := ExecutionRecord(value)
	originals := map[*acctcore.Record]*ExecutionAccount{input: value}
	legacyValue := func(record *acctcore.Record) *ExecutionAccount {
		if original, ok := originals[record]; ok {
			return original
		}
		return NewExecutionAccount(record)
	}
	options := acctcore.OpenAITaskOptions{
		FallbackMutex: taskMu,
		Register: func(ctx context.Context, record *acctcore.Record) (string, error) {
			return register(ctx, legacyValue(record).View())
		},
		Persist: func(ctx context.Context, record *acctcore.Record, credentials map[string]any) error {
			original := legacyValue(record)
			err := PersistExecutionCredentials(ctx, repo, original, credentials)
			record.Credentials = original.Record.Credentials
			return err
		},
	}
	if repo != nil {
		options.Read = func(ctx context.Context, id int64) (*acctcore.Record, error) {
			original, err := repo.GetByID(ctx, id)
			record := ExecutionRecord(original)
			originals[record] = original
			return record, err
		}
	}
	if wsInvalidator != nil {
		options.Invalidate = wsInvalidator.InvalidateAgentIdentityWSConnections
	}
	err := coordinator.Ensure(ctx, options, input, expectedTaskID)
	if value != nil {
		value.Record.Credentials = input.Credentials
	}
	return err
}

func (s *ExecutionAgentIdentity) Ensure(ctx context.Context, account *ExecutionAccount, expectedTaskID string) error {
	if s == nil {
		return errors.New("openai gateway service is nil")
	}
	return ensureAgentIdentityTaskForAccount(ctx, s.coordinator, s.register, s.store, s, &s.taskMu, account, expectedTaskID)
}

func (s *ExecutionAgentIdentity) Headers(ctx context.Context, account *ExecutionAccount, token string) (http.Header, error) {
	if account == nil {
		return nil, errors.New("account is nil")
	}
	credAccount := account
	if account.View().IsShadow() {
		resolved, err := CredentialAccount(ctx, s.store, account)
		if err != nil {
			return nil, err
		}
		credAccount = resolved
	}
	headers := make(http.Header)
	if credAccount != nil && credAccount.View().IsOpenAIAgentIdentity() {
		agentHeaders, err := buildAgentIdentityAuthenticationHeaders(ctx, s.coordinator, s.register, s.store, s, &s.taskMu, credAccount)
		if err != nil {
			return nil, err
		}
		return agentHeaders, nil
	}
	headers.Set("Authorization", "Bearer "+token)
	return headers, nil
}

func buildAgentIdentityAuthenticationHeaders(ctx context.Context, coordinator *acctcore.OpenAITaskCoordinator, register func(context.Context, *acctcore.Record) (string, error), repo ExecutionAccountStore, wsInvalidator agentIdentityWSConnectionInvalidator, taskMu *sync.Mutex, account *ExecutionAccount) (http.Header, error) {
	headers, _, err := buildAgentIdentityAuthenticationHeadersWithTask(ctx, coordinator, register, repo, wsInvalidator, taskMu, account)
	return headers, err
}

// buildAgentIdentityAuthenticationHeadersWithTask 同时返回本次签名使用的 task，供失败恢复执行 CAS。
func buildAgentIdentityAuthenticationHeadersWithTask(ctx context.Context, coordinator *acctcore.OpenAITaskCoordinator, register func(context.Context, *acctcore.Record) (string, error), repo ExecutionAccountStore, wsInvalidator agentIdentityWSConnectionInvalidator, taskMu *sync.Mutex, account *ExecutionAccount) (http.Header, string, error) {
	if account == nil || !account.View().IsOpenAIAgentIdentity() {
		return nil, "", errors.New("agent identity account is required")
	}
	if err := ensureAgentIdentityTaskForAccount(ctx, coordinator, register, repo, wsInvalidator, taskMu, account, ""); err != nil {
		return nil, "", err
	}
	key, err := accountprovider.AgentIdentityKey(account.View())
	if err != nil {
		return nil, "", err
	}
	assertion, err := openai.BuildAgentAssertion(key, time.Now())
	if err != nil {
		return nil, "", err
	}
	headers := make(http.Header)
	headers.Set("Authorization", assertion)
	return headers, key.TaskID, nil
}

func (s *ExecutionAgentIdentity) RefreshHeaders(ctx context.Context, account *ExecutionAccount, headers http.Header) (http.Header, error) {
	if account == nil {
		return upstream.CloneHeader(headers), nil
	}
	credAccount := account
	if account.View().IsShadow() {
		resolved, err := CredentialAccount(ctx, s.store, account)
		if err != nil {
			return nil, err
		}
		credAccount = resolved
	}
	if !credAccount.View().IsOpenAIAgentIdentity() {
		return upstream.CloneHeader(headers), nil
	}
	refreshed := upstream.CloneHeader(headers)
	if refreshed == nil {
		refreshed = make(http.Header)
	}
	authHeaders, err := buildAgentIdentityAuthenticationHeaders(ctx, s.coordinator, s.register, s.store, s, &s.taskMu, credAccount)
	if err != nil {
		return nil, err
	}
	refreshed.Set("Authorization", authHeaders.Get("Authorization"))
	return refreshed, nil
}

func (s *ExecutionAgentIdentity) Recover(ctx context.Context, account *ExecutionAccount, expectedTaskID string) error {
	if account != nil && account.View().IsShadow() {
		if resolved, err := CredentialAccount(ctx, s.store, account); err == nil && resolved != nil && strings.TrimSpace(expectedTaskID) == "" {
			expectedTaskID = strings.TrimSpace(resolved.View().GetCredential("task_id"))
		}
	}
	return s.Ensure(ctx, account, expectedTaskID)
}

func (s *ExecutionAgentIdentity) UsesAgentIdentity(ctx context.Context, account *ExecutionAccount) bool {
	if account == nil {
		return false
	}
	credAccount := account
	if account.View().IsShadow() {
		resolved, err := CredentialAccount(ctx, s.store, account)
		if err != nil {
			return false
		}
		credAccount = resolved
	}
	return credAccount != nil && credAccount.View().IsOpenAIAgentIdentity()
}

// RedactExecutionAgentBody 在上游错误进入日志、Ops 或响应前移除凭据值。
// 正常响应不应回显这些字段，此处仍做纵深防护以阻止异常上游泄漏。
func RedactExecutionAgentBody(ctx context.Context, repo ExecutionAccountStore, account *ExecutionAccount, body []byte) []byte {
	if account == nil || len(body) == 0 {
		return body
	}
	credAccount := account
	if account != nil && account.View().IsShadow() {
		if resolved, err := CredentialAccount(ctx, repo, account); err == nil && resolved != nil {
			credAccount = resolved
		}
	}
	if credAccount == nil || !credAccount.View().IsOpenAIAgentIdentity() {
		return body
	}
	return openai.RedactAgentIdentityBody(body, credAccount.View().GetCredential)
}

func (s *ExecutionAgentIdentity) Redact(ctx context.Context, account *ExecutionAccount, body []byte) []byte {
	if !s.UsesAgentIdentity(ctx, account) {
		return body
	}
	return RedactExecutionAgentBody(ctx, s.store, account, body)
}

// ExecutionAgentIdentity 封装本次执行所需身份协作，不持有 Gin、配置或第二份注册状态。
type ExecutionAgentIdentity struct {
	store       ExecutionAccountStore
	coordinator *acctcore.OpenAITaskCoordinator
	register    func(context.Context, *acctcore.Record) (string, error)
	invalidate  func(int64)
	taskMu      sync.Mutex
}

func NewExecutionAgentIdentity(coordinator *acctcore.OpenAITaskCoordinator, store ExecutionAccountStore, register func(context.Context, *acctcore.Record) (string, error), invalidate func(int64)) *ExecutionAgentIdentity {
	return &ExecutionAgentIdentity{store: store, coordinator: coordinator, register: register, invalidate: invalidate}
}
func (s *ExecutionAgentIdentity) InvalidateAgentIdentityWSConnections(id int64) {
	if s.invalidate != nil {
		s.invalidate(id)
	}
}
