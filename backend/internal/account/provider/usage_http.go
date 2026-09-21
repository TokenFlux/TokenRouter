package provider

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usagecontract"
)

// UsageHTTPOptions 只接受查询技术端口与出站策略，不持有旧账号服务或完整配置。
type UsageHTTPOptions struct {
	Available  bool
	Policy     egress.UsageURLPolicy
	ResolveTLS func(egress.TLSSelection) *tlsfingerprint.Profile
	Do         func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error)
}

func NewUpstreamUsageHTTPExecution(options UsageHTTPOptions) *UpstreamUsageExecution {
	options.Policy.UpstreamHosts = slices.Clone(options.Policy.UpstreamHosts)
	return NewUpstreamUsageExecution(UpstreamUsageExecutionOptions{
		Available: func() bool { return options.Available && options.Do != nil },
		BaseURL:   UpstreamUsageBaseURL,
		Request: func(value *account.Record, config account.UpstreamUsageQueryConfig) (*usagecontract.Request, error) {
			return usageHTTPRequest(value, config, options)
		},
	})
}

func usageHTTPRequest(value *account.Record, query account.UpstreamUsageQueryConfig, options UsageHTTPOptions) (*usagecontract.Request, error) {
	apiKey := strings.TrimSpace(value.GetCredential("api_key"))
	if apiKey == "" {
		return nil, account.ErrUpstreamUsageAccountInvalid
	}
	base := strings.TrimSpace(query.BaseURL)
	if base == "" {
		base = UpstreamUsageBaseURL(value)
	}
	validated, err := options.Policy.Validate(base)
	if err != nil {
		return nil, account.ErrUpstreamUsageConfigInvalid.WithCause(err)
	}
	proxyURL := ""
	if value.ProxyID != nil {
		if value.Proxy == nil {
			return nil, account.ErrUpstreamUsageRequestFailed
		}
		if value.Proxy.ID != *value.ProxyID {
			return nil, account.ErrUpstreamUsageIdentityChanged
		}
		proxyURL = value.Proxy.URL()
	}
	var profile *tlsfingerprint.Profile
	if options.ResolveTLS != nil {
		profile = options.ResolveTLS(egress.TLSSelection{Enabled: value.IsTLSFingerprintEnabled(), DirectProfileID: value.GetTLSFingerprintProfileID()})
	}
	return &usagecontract.Request{
		BaseURL: validated, APIKey: apiKey,
		WalletToken:       value.GetCredential(account.NewAPIUserAccessTokenCredentialKey),
		WalletUserID:      value.GetCredential(account.NewAPIUserIDCredentialKey),
		ZhipuOrganization: value.GetCredential("zhipu_organization"), ZhipuProject: value.GetCredential("zhipu_project"),
		ApplyHeaders: func(headers http.Header) {
			if headers != nil {
				policy := egress.RequestPolicy(egress.RequestPolicyInput{Headers: value.HeaderOverrides()})
				egressprovider.ApplyRequestHeaders(headers, policy, anthropic.ResolveWireCasing)
			}
		},
		Endpoint: httpclient.BuildOpenAIEndpointURL,
		Context: func(ctx context.Context) context.Context {
			return upstream.WithHTTPUpstreamRedirectsDisabled(upstream.WithHTTPUpstreamProfile(ctx, upstream.HTTPUpstreamProfileOpenAI))
		},
		Do: func(request *http.Request) (*http.Response, error) {
			return options.Do(request, proxyURL, value.ID, value.Concurrency, profile)
		},
	}, nil
}

// UpstreamUsageBaseURL 只选择原查询根地址，具体供应商仍按原规则替换路径。
func UpstreamUsageBaseURL(value *account.Record) string {
	if value == nil {
		return ""
	}
	switch value.Platform {
	case capability.PlatformOpenAI, capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformDeepseek:
		return (account.ProtocolTarget{Record: value}).GetOpenAIBaseURL()
	case capability.PlatformAnthropic:
		return value.GetBaseURL()
	case capability.PlatformGrok:
		return grok.ResolveAccountBaseURL(value.IsGrokOAuth(), value.GetCredential("base_url"), "")
	case capability.PlatformGemini, capability.PlatformAntigravity:
		return value.GetGeminiBaseURL("https://generativelanguage.googleapis.com")
	default:
		return strings.TrimSpace(value.GetCredential("base_url"))
	}
}
