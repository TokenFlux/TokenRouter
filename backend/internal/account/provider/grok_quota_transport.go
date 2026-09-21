package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// GrokQuotaTransport 只执行供应商查询；认领、快照、健康写回和后台任务归账号用例。
type GrokQuotaTransport struct {
	Do                func(*http.Request, string, int64, int) (*http.Response, error)
	Proxy             func(context.Context, int64) (*egress.Proxy, error)
	DefaultBaseURL    func(context.Context) string
	OperatorValidator grok.BaseURLValidator
	MapStatus         func(int) int
}

// GrokBaseURLValidator 区分官方 OAuth 地址与受运营方策略约束的自定义地址。
func GrokBaseURLValidator(value *account.Record, operator grok.BaseURLValidator) (grok.BaseURLValidator, error) {
	if value == nil || !value.IsGrok() {
		return nil, fmt.Errorf("grok account is required")
	}
	if operator == nil {
		operator = grok.ValidateBaseURL
	}
	switch value.Type {
	case capability.AccountTypeOAuth:
		return grok.RedactedBaseURLValidator(func(raw string) (string, error) {
			if grok.IsOfficialBaseURL(raw) {
				return grok.ValidateTrustedBaseURL(raw)
			}
			return operator(raw)
		}), nil
	case capability.AccountTypeAPIKey:
		return grok.RedactedBaseURLValidator(operator), nil
	default:
		return nil, fmt.Errorf("unsupported grok account type: %s", value.Type)
	}
}

func (s *GrokQuotaTransport) baseURL(ctx context.Context, value *account.Record, dynamic bool) string {
	fallback := ""
	if dynamic && s.DefaultBaseURL != nil {
		fallback = s.DefaultBaseURL(ctx)
	}
	return grok.ResolveAccountBaseURL(value.IsGrokOAuth(), value.GetCredential("base_url"), fallback)
}

func applyGrokQuotaHeaders(value *account.Record, headers http.Header) {
	if headers == nil {
		return
	}
	policy := egress.RequestPolicy(egress.RequestPolicyInput{Headers: value.HeaderOverrides()})
	egressprovider.ApplyRequestHeaders(headers, policy, anthropic.ResolveWireCasing)
}

// ResolveProxy 保留关联对象优先、必要时读取存储，以及原有缺失返回值。
func (s *GrokQuotaTransport) ResolveProxy(ctx context.Context, value *account.Record) string {
	if value == nil || value.ProxyID == nil {
		return ""
	}
	if value.Proxy != nil {
		return value.Proxy.URL()
	}
	if s.Proxy != nil {
		if proxy, err := s.Proxy(ctx, *value.ProxyID); err == nil && proxy != nil {
			value.Proxy = proxy
			return proxy.URL()
		}
	}
	return ""
}

func (s *GrokQuotaTransport) FetchBilling(ctx context.Context, value *account.Record, token, proxy string, weekly bool) (*grok.BillingSummary, int, error) {
	validator, err := GrokBaseURLValidator(value, s.OperatorValidator)
	var target string
	if err == nil {
		target, err = grok.BuildBillingEndpointURL(s.baseURL(ctx, value, false), weekly, validator)
	}
	if err != nil {
		return nil, 0, apperror.Newf(http.StatusBadRequest, "GROK_QUOTA_BASE_URL_INVALID", "invalid Grok base_url: %v", err)
	}
	return grok.FetchBilling(ctx, grok.BillingFetchOptions{URL: target, Token: token, AccountID: value.ID, Weekly: weekly, MaxAttempts: 2, RetryDelay: 100 * time.Millisecond,
		Do: func(request *http.Request) (*http.Response, error) {
			return s.Do(request, proxy, value.ID, max(value.Concurrency, 2))
		},
		ApplyHeaders: func(headers http.Header) { applyGrokQuotaHeaders(value, headers) }, Truncate: openai.Truncate, MapStatus: s.MapStatus, Warn: slog.Warn,
	})
}

// GrokQuotaProbeBody 维持缺省模型、载荷与序列化错误行为。
func GrokQuotaProbeBody(model string) ([]byte, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		model = grok.DefaultResponsesModel
	}
	return json.Marshal(map[string]any{"model": model, "input": "hi", "stream": true})
}

func (s *GrokQuotaTransport) ActiveQuota(ctx context.Context, value *account.Record, token, proxy, model string, observe func(*grok.QuotaSnapshot, int)) error {
	body, err := GrokQuotaProbeBody(model)
	if err != nil {
		return apperror.Newf(http.StatusBadRequest, "GROK_QUOTA_PROBE_BODY_ERROR", "failed to build probe body: %v", err)
	}
	validator, err := GrokBaseURLValidator(value, s.OperatorValidator)
	var target string
	if err == nil {
		// 原 Responses 构造在此以 Background 读取动态设置，保留读取时点和取消策略。
		target, err = grok.BuildResponsesURLWithValidator(s.baseURL(context.Background(), value, true), validator)
	}
	if err != nil {
		return apperror.Newf(http.StatusBadRequest, "GROK_QUOTA_BASE_URL_INVALID", "invalid Grok base_url: %v", err)
	}
	return grok.FetchActiveQuota(ctx, grok.ActiveQuotaOptions{URL: target, Body: body, Token: token, AccountID: value.ID, Model: model, Timeout: 20 * time.Second,
		ApplyHeaders: func(headers http.Header) {
			if value.IsGrokOAuth() {
				grok.ApplyCLIHeaders(headers)
			}
			applyGrokQuotaHeaders(value, headers)
		},
		Do: func(request *http.Request) (*http.Response, error) {
			return s.Do(request, proxy, value.ID, max(value.Concurrency, 1))
		},
		Observe: observe, MapStatus: s.MapStatus, Warn: slog.Warn,
	})
}

func (s *GrokQuotaTransport) FetchModels(ctx context.Context, value *account.Record, token string) ([]string, error) {
	base := strings.TrimSpace(s.baseURL(ctx, value, true))
	if base == "" {
		base = grok.DefaultCLIBaseURL
	}
	validator, err := GrokBaseURLValidator(value, s.OperatorValidator)
	if err != nil {
		return nil, err
	}
	base, err = validator(base)
	if err != nil {
		return nil, err
	}
	return grok.FetchObservedModels(ctx, grok.ModelsRequest{URL: httpclient.BuildOpenAIEndpointURL(base, "/v1/models"), Token: token, UserID: value.GetCredential("sub"), Email: value.GetCredential("email"), OAuth: value.IsGrokOAuth(),
		ApplyOverrides: func(headers http.Header) { applyGrokQuotaHeaders(value, headers) },
		Do: func(request *http.Request) (*http.Response, error) {
			proxyURL := ""
			if s.Proxy != nil && value.ProxyID != nil {
				if proxy, readErr := s.Proxy(ctx, *value.ProxyID); readErr == nil && proxy != nil {
					proxyURL = proxy.URL()
				}
			}
			if s.Do == nil {
				return nil, nil
			}
			return s.Do(request, proxyURL, value.ID, value.Concurrency)
		},
	})
}
