package service

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

const (
	OpenAIAuthModeAgentIdentity          = acctcore.OpenAIAuthModeAgentIdentity
	agentIdentityAuthAPIBaseURL          = "https://auth.openai.com/api/accounts"
	agentIdentityTaskRegistrationTimeout = 30 * time.Second
)

var openAIAgentIdentityAuthAPIBaseURL = agentIdentityAuthAPIBaseURL

type agentIdentityWSConnectionInvalidator interface {
	InvalidateAgentIdentityWSConnections(accountID int64)
}

type agentIdentityKey struct {
	runtimeID  string
	privateKey ed25519.PrivateKey
	taskID     string
}

type agentIdentityTaskRecoveredError struct{}

func (e *agentIdentityTaskRecoveredError) Error() string {
	return "agent identity task recovered"
}

func (a *Account) IsOpenAIAgentIdentity() bool { return protocolRecord(a).IsOpenAIAgentIdentity() }

func agentIdentityPrivateKey(account *Account) (ed25519.PrivateKey, error) {
	if account == nil {
		return nil, errors.New("agent identity account is nil")
	}
	return native.ParseAgentIdentityPrivateKey(account.GetCredential("agent_private_key"))
}

// ValidateOpenAIAgentIdentityPrivateKey 校验 PKCS#8 Ed25519 私钥，但不返回或记录密钥材料。
func ValidateOpenAIAgentIdentityPrivateKey(encoded string) error {
	account := &Account{Credentials: map[string]any{"agent_private_key": encoded}}
	_, err := agentIdentityPrivateKey(account)
	return err
}

func agentIdentityKeyFromAccount(account *Account) (agentIdentityKey, error) {
	privateKey, err := agentIdentityPrivateKey(account)
	if err != nil {
		return agentIdentityKey{}, err
	}
	runtimeID := strings.TrimSpace(account.GetCredential("agent_runtime_id"))
	if runtimeID == "" {
		return agentIdentityKey{}, errors.New("agent identity runtime id is missing")
	}
	return agentIdentityKey{
		runtimeID:  runtimeID,
		privateKey: privateKey,
		taskID:     strings.TrimSpace(account.GetCredential("task_id")),
	}, nil
}

func buildAgentAssertion(key agentIdentityKey, now time.Time) (string, error) {
	return native.BuildAgentAssertion(nativeAgentIdentityKey(key), now)
}

func decryptAgentTaskID(key agentIdentityKey, encoded string) (string, error) {
	return native.DecryptAgentTaskID(nativeAgentIdentityKey(key), encoded)
}

func registerAgentIdentityTask(ctx context.Context, account *Account) (string, error) {
	key, err := agentIdentityKeyFromAccount(account)
	if err != nil {
		return "", err
	}
	now := time.Now()
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	return native.RegisterAgentIdentityTask(ctx, nativeAgentIdentityKey(key), proxyURL, openAIAgentIdentityAuthAPIBaseURL, now)
}

// 兼容入口只转换记录和写回时机；锁、复查与登记规则由唯一账号协调器执行。
func ensureAgentIdentityTaskForAccount(ctx context.Context, repo AccountRepository, wsInvalidator agentIdentityWSConnectionInvalidator, taskMu *sync.Mutex, value *Account, expectedTaskID string) error {
	input := AccountRecordView(value)
	originals := map[*acctcore.Record]*Account{input: value}
	legacyValue := func(record *acctcore.Record) *Account {
		if original, ok := originals[record]; ok {
			return original
		}
		return AccountFromRecord(record)
	}
	options := acctcore.OpenAITaskOptions{
		FallbackMutex: taskMu,
		Register: func(ctx context.Context, record *acctcore.Record) (string, error) {
			return registerAgentIdentityTask(ctx, legacyValue(record))
		},
		Persist: func(ctx context.Context, record *acctcore.Record, credentials map[string]any) error {
			original := legacyValue(record)
			err := persistAccountCredentials(ctx, repo, original, credentials)
			record.Credentials = original.Credentials
			return err
		},
	}
	if repo != nil {
		options.Read = func(ctx context.Context, id int64) (*acctcore.Record, error) {
			original, err := repo.GetByID(ctx, id)
			record := AccountRecordView(original)
			originals[record] = original
			return record, err
		}
	}
	if wsInvalidator != nil {
		options.Invalidate = wsInvalidator.InvalidateAgentIdentityWSConnections
	}
	err := acctcore.SharedOpenAITaskCoordinator().Ensure(ctx, options, input, expectedTaskID)
	if value != nil {
		value.Credentials = input.Credentials
	}
	return err
}

func (s *OpenAIGatewayService) ensureAgentIdentityTask(ctx context.Context, account *Account, expectedTaskID string) error {
	if s == nil {
		return errors.New("openai gateway service is nil")
	}
	return ensureAgentIdentityTaskForAccount(ctx, s.accountRepo, s, &s.agentIdentityTaskMu, account, expectedTaskID)
}

func isAgentIdentityTaskInvalidHTTPResponse(statusCode int, body []byte) bool {
	return native.IsAgentTaskInvalidHTTPResponse(statusCode, body)
}

type agentIdentityTaskRecoveryContextKey struct{}

func markAgentIdentityTaskRecoveryTried(ctx context.Context) context.Context {
	return context.WithValue(ctx, agentIdentityTaskRecoveryContextKey{}, true)
}

func agentIdentityTaskRecoveryWasTried(ctx context.Context) bool {
	tried, _ := ctx.Value(agentIdentityTaskRecoveryContextKey{}).(bool)
	return tried
}

func isAgentIdentityTaskInvalidWSDialError(err *openAIWSDialError) bool {
	return err != nil && isAgentIdentityTaskInvalidHTTPResponse(err.StatusCode, err.ResponseBody)
}

func (s *OpenAIGatewayService) buildOpenAIAuthenticationHeaders(ctx context.Context, account *Account, token string) (http.Header, error) {
	if account == nil {
		return nil, errors.New("account is nil")
	}
	credAccount := account
	if account.IsShadow() {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return nil, err
		}
		credAccount = resolved
	}
	headers := make(http.Header)
	if credAccount != nil && credAccount.IsOpenAIAgentIdentity() {
		agentHeaders, err := buildAgentIdentityAuthenticationHeaders(ctx, s.accountRepo, s, &s.agentIdentityTaskMu, credAccount)
		if err != nil {
			return nil, err
		}
		return agentHeaders, nil
	}
	headers.Set("Authorization", "Bearer "+token)
	return headers, nil
}

func buildAgentIdentityAuthenticationHeaders(ctx context.Context, repo AccountRepository, wsInvalidator agentIdentityWSConnectionInvalidator, taskMu *sync.Mutex, account *Account) (http.Header, error) {
	headers, _, err := buildAgentIdentityAuthenticationHeadersWithTask(ctx, repo, wsInvalidator, taskMu, account)
	return headers, err
}

// buildAgentIdentityAuthenticationHeadersWithTask 同时返回本次签名使用的 task，供失败恢复执行 CAS。
func buildAgentIdentityAuthenticationHeadersWithTask(ctx context.Context, repo AccountRepository, wsInvalidator agentIdentityWSConnectionInvalidator, taskMu *sync.Mutex, account *Account) (http.Header, string, error) {
	if account == nil || !account.IsOpenAIAgentIdentity() {
		return nil, "", errors.New("agent identity account is required")
	}
	if err := ensureAgentIdentityTaskForAccount(ctx, repo, wsInvalidator, taskMu, account, ""); err != nil {
		return nil, "", err
	}
	key, err := agentIdentityKeyFromAccount(account)
	if err != nil {
		return nil, "", err
	}
	assertion, err := buildAgentAssertion(key, time.Now())
	if err != nil {
		return nil, "", err
	}
	headers := make(http.Header)
	headers.Set("Authorization", assertion)
	return headers, key.taskID, nil
}

func (s *OpenAIGatewayService) refreshOpenAIAgentIdentityHeaders(ctx context.Context, account *Account, headers http.Header) (http.Header, error) {
	if account == nil {
		return cloneHeader(headers), nil
	}
	credAccount := account
	if account.IsShadow() {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return nil, err
		}
		credAccount = resolved
	}
	if !credAccount.IsOpenAIAgentIdentity() {
		return cloneHeader(headers), nil
	}
	refreshed := cloneHeader(headers)
	if refreshed == nil {
		refreshed = make(http.Header)
	}
	authHeaders, err := buildAgentIdentityAuthenticationHeaders(ctx, s.accountRepo, s, &s.agentIdentityTaskMu, credAccount)
	if err != nil {
		return nil, err
	}
	refreshed.Set("Authorization", authHeaders.Get("Authorization"))
	return refreshed, nil
}

func (s *OpenAIGatewayService) recoverAgentIdentityTask(ctx context.Context, account *Account, expectedTaskID string) error {
	if account != nil && account.IsShadow() {
		if resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account); err == nil && resolved != nil && strings.TrimSpace(expectedTaskID) == "" {
			expectedTaskID = strings.TrimSpace(resolved.GetCredential("task_id"))
		}
	}
	return s.ensureAgentIdentityTask(ctx, account, expectedTaskID)
}

func (s *OpenAIGatewayService) isAgentIdentityAccount(ctx context.Context, account *Account) bool {
	if account == nil {
		return false
	}
	credAccount := account
	if account.IsShadow() {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return false
		}
		credAccount = resolved
	}
	return credAccount != nil && credAccount.IsOpenAIAgentIdentity()
}

// redactAgentIdentitySensitiveBodyForAccount 在上游错误进入日志、Ops 或响应前移除凭据值。
// 正常响应不应回显这些字段，此处仍做纵深防护以阻止异常上游泄漏。
func redactAgentIdentitySensitiveBodyForAccount(ctx context.Context, repo AccountRepository, account *Account, body []byte) []byte {
	if account == nil || len(body) == 0 {
		return body
	}
	credAccount := account
	if account != nil && account.IsShadow() {
		if resolved, err := resolveCredentialAccount(ctx, repo, account); err == nil && resolved != nil {
			credAccount = resolved
		}
	}
	if credAccount == nil || !credAccount.IsOpenAIAgentIdentity() {
		return body
	}
	redacted := string(body)
	for _, key := range []string{
		"agent_private_key",
		"agent_runtime_id",
		"task_id",
		"access_token",
		"refresh_token",
		"id_token",
		"api_key",
		"session_key",
		"cookie",
	} {
		if value := strings.TrimSpace(credAccount.GetCredential(key)); value != "" {
			redacted = strings.ReplaceAll(redacted, value, "[redacted]")
		}
	}
	const assertionPrefix = "AgentAssertion "
	for offset := 0; offset < len(redacted); {
		relativeStart := strings.Index(redacted[offset:], assertionPrefix)
		if relativeStart < 0 {
			break
		}
		start := offset + relativeStart
		valueStart := start + len(assertionPrefix)
		end := valueStart
		for end < len(redacted) && !strings.ContainsRune(" \t\r\n\"',}", rune(redacted[end])) {
			end++
		}
		redacted = redacted[:valueStart] + "[redacted]" + redacted[end:]
		offset = valueStart + len("[redacted]")
	}
	return []byte(redacted)
}

func (s *OpenAIGatewayService) redactAgentIdentitySensitiveBody(ctx context.Context, account *Account, body []byte) []byte {
	if !s.isAgentIdentityAccount(ctx, account) {
		return body
	}
	return redactAgentIdentitySensitiveBodyForAccount(ctx, s.accountRepo, account, body)
}

// 密钥只在原生执行边界作字段投影，不进入公开结果或日志。
func nativeAgentIdentityKey(key agentIdentityKey) native.AgentIdentityKey {
	return native.AgentIdentityKey{RuntimeID: key.runtimeID, PrivateKey: key.privateKey, TaskID: key.taskID}
}
