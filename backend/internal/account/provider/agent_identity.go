package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// AgentIdentityKey 只在执行边界解析私钥，保留原错误与字段校验顺序。
func AgentIdentityKey(value *account.Record) (openai.AgentIdentityKey, error) {
	if value == nil {
		return openai.AgentIdentityKey{}, errors.New("agent identity account is nil")
	}
	privateKey, err := openai.ParseAgentIdentityPrivateKey(value.GetCredential("agent_private_key"))
	if err != nil {
		return openai.AgentIdentityKey{}, err
	}
	runtimeID := strings.TrimSpace(value.GetCredential("agent_runtime_id"))
	if runtimeID == "" {
		return openai.AgentIdentityKey{}, errors.New("agent identity runtime id is missing")
	}
	return openai.AgentIdentityKey{RuntimeID: runtimeID, PrivateKey: privateKey, TaskID: strings.TrimSpace(value.GetCredential("task_id"))}, nil
}

// RegisterAgentIdentityTask 复用供应商交换，账号协调器拥有认领、复查和持久化。
func RegisterAgentIdentityTask(ctx context.Context, value *account.Record, baseURL string) (string, error) {
	key, err := AgentIdentityKey(value)
	if err != nil {
		return "", err
	}
	now := time.Now()
	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}
	return openai.RegisterAgentIdentityTask(ctx, key, proxyURL, baseURL, now)
}

// AgentIdentityHeaders 同时返回本次签名 task，恢复只能针对该身份执行。
func AgentIdentityHeaders(ctx context.Context, value *account.Record, ensure func(context.Context, *account.Record, string) error) (http.Header, string, error) {
	if value == nil || !value.IsOpenAIAgentIdentity() {
		return nil, "", errors.New("agent identity account is required")
	}
	if err := ensure(ctx, value, ""); err != nil {
		return nil, "", err
	}
	key, err := AgentIdentityKey(value)
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

// RedactAgentIdentityBody 在原位置读取影子母账号，并把凭据值交给平台脱敏器。
func RedactAgentIdentityBody(ctx context.Context, read func(context.Context, int64) (*account.Record, error), value *account.Record, body []byte) []byte {
	if value == nil || len(body) == 0 {
		return body
	}
	credential := value
	if value.IsCredentialShadow() {
		if resolved, err := account.ResolveCredentialRecord(ctx, read, value); err == nil && resolved != nil {
			credential = resolved
		}
	}
	if credential == nil || !credential.IsOpenAIAgentIdentity() {
		return body
	}
	return openai.RedactAgentIdentityBody(body, credential.GetCredential)
}
