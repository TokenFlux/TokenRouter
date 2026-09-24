package messageforward

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
	"github.com/tidwall/gjson"
)

// buildRequest 只按已选账号类型选择原生构造器，不提前交换凭据或改变重试时点。
func (r *Runtime) buildRequest(ctx context.Context, output HTTPBoundary, state *AttemptState, target *provider.ExecutionAccount, body []byte, token, tokenType, model string, stream, mimic bool) (*http.Request, []byte, error) {
	if target.Record.Platform == capability.PlatformAnthropic && target.Record.Type == capability.AccountTypeServiceAccount {
		body = anthropic.StripDeferredToolCacheControl(body)
		request, err := r.buildVertexRequest(ctx, output, target, body, token, model, stream)
		return request, body, err
	}
	return anthropic.BuildRequest(ctx, body, token, tokenType, model, stream, mimic, r.requestOptions(ctx, output, state, target, model, tokenType, mimic))
}

func (r *Runtime) buildVertexRequest(ctx context.Context, output HTTPBoundary, target *provider.ExecutionAccount, body []byte, token, model string, stream bool) (*http.Request, error) {
	var headers http.Header
	var beta string
	if output.RequestPresent() {
		headers = output.RequestHeaders()
		beta = anthropic.GetHeaderRaw(headers, "anthropic-beta")
	}
	return vertex.BuildAnthropicRequest(ctx, body, token, model, stream, vertex.AnthropicRequestOptions{
		ClientBeta: beta, ClientHeaders: headers, AllowedHeaders: anthropic.AllowedHeaders,
		Project: func() string {
			return provider.ExecutionProtocolRecord(target).VertexProjectID(vertex.ServiceAccountProjectID)
		},
		Location: func(model string) string {
			return provider.ExecutionProtocolRecord(target).VertexLocation(model)
		},
		Policy: func(ctx context.Context, header string) (map[string]struct{}, error) {
			policy := r.evaluateBeta(ctx, target, header, model)
			if policy.BlockErr != nil {
				return nil, policy.BlockErr
			}
			return anthropic.MergeDropSets(policy.FilterSet), nil
		},
		SanitizeBody: anthropic.SanitizeAnthropicBodyForBetaTokens,
		WireCasing:   anthropic.ResolveWireCasing,
		AddHeader:    anthropic.AddHeaderRaw,
		SetHeader:    anthropic.SetHeaderRaw,
		DeleteHeader: anthropic.DeleteHeaderAllForms,
		Debug: func(header http.Header, body []byte, fields map[string]string) {
			if r.dependencies.Debug != nil {
				r.dependencies.Debug.Snapshot("UPSTREAM_FORWARD_VERTEX_ANTHROPIC", header, body, fields)
			}
		},
	})
}

func (r *Runtime) buildPassthroughRequest(ctx context.Context, output HTTPBoundary, state *AttemptState, target *provider.ExecutionAccount, body []byte, token string) (*http.Request, []byte, error) {
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	options := r.requestOptions(ctx, output, state, target, model, "apikey", false)
	options.URL = func() (string, error) {
		if base := target.View().GetBaseURL(); base != "" {
			validated, err := r.validateBaseURL(base)
			if err != nil {
				return "", err
			}
			return validated + "/v1/messages?beta=true", nil
		}
		return anthropic.ClaudeAPIURL, nil
	}
	options.OriginalPolicy = func(ctx context.Context, header, model string) (map[string]struct{}, error) {
		policy := r.evaluateBeta(ctx, target, header, model)
		if policy.BlockErr != nil {
			return nil, policy.BlockErr
		}
		return policy.FilterSet, nil
	}
	return anthropic.BuildRequestPassthrough(ctx, body, token, options)
}

func (r *Runtime) buildCountRequest(ctx context.Context, output HTTPBoundary, state *AttemptState, target *provider.ExecutionAccount, body []byte, token, tokenType, model string, mimic, passthrough bool) (*http.Request, []byte, error) {
	if passthrough {
		options := r.countOptions(ctx, output, state, target, "", "apikey", false, true)
		request, err := anthropic.BuildCountTokensRequestPassthrough(ctx, body, token, options)
		return request, nil, err
	}
	options := r.countOptions(ctx, output, state, target, model, tokenType, mimic, false)
	return anthropic.BuildCountTokensRequest(ctx, body, token, tokenType, model, mimic, options)
}

// requestOptions 固定本次目标与准备状态，动态设置仍由平台构造器在原时点调用。
func (r *Runtime) requestOptions(ctx context.Context, output HTTPBoundary, state *AttemptState, target *provider.ExecutionAccount, model, tokenType string, mimic bool) anthropic.RequestOptions {
	options := anthropic.RequestOptions{
		InjectAPIKeyBeta: r.options.InjectAPIKeyBeta,
		AccountID:        target.Record.ID,
		OAuth:            target.View().IsOAuth(),
		AccountUUID:      target.View().GetExtraString("account_uuid"),
		MaskSession:      target.View().IsSessionIDMaskingEnabled(),
		APIKeyBearer:     provider.ExecutionProtocolRecord(target).GetAnthropicAPIKeyAuthScheme() == account.AnthropicAPIKeyAuthSchemeAuthorizationBearer,
		ClientHeaders:    output.RequestHeaders(),
		ApplyOverrides: func(headers http.Header) {
			accountprovider.ApplyAccountHeaderOverrides(provider.ExecutionProtocolRecord(target), headers)
		},
	}
	if r.dependencies.Fingerprint != nil {
		options.Fingerprint = r.dependencies.Fingerprint
	}
	options.URL = func() (string, error) {
		return r.requestURL(target, false, false)
	}
	options.FastMode = func(ctx context.Context, body []byte, headers http.Header) ([]byte, http.Header, error) {
		return provider.ApplyAnthropicFastMode(ctx, r.dependencies.Prices, target, model, body, headers)
	}
	options.Forwarding = func(ctx context.Context) (bool, bool) {
		if r.dependencies.Settings == nil {
			return true, false
		}
		fingerprint, mimic, _ := r.dependencies.Settings.GetGatewayForwardingSettings(ctx)
		return fingerprint, mimic
	}
	options.FilterSet = func(ctx context.Context) map[string]struct{} {
		return r.betaFilters(ctx, state, target, model)
	}
	options.BetaOverride = func() (string, bool) {
		return accountprovider.HeaderOverrideValue(provider.ExecutionProtocolRecord(target), "anthropic-beta")
	}
	options.CheckFastBeta = func(ctx context.Context) error {
		if err := r.checkBetaTokens(ctx, []string{anthropic.BetaFastMode}, target, model); err != nil {
			return err
		}
		return nil
	}
	options.Debug = func(req *http.Request, body []byte, fields map[string]string) {
		if r.dependencies.Debug != nil {
			r.dependencies.Debug.Snapshot("UPSTREAM_FORWARD", req.Header, body, fields)
		}
	}
	options.Capture = func(req *http.Request, body []byte) {
		if r.dependencies.Debug == nil {
			return
		}
		retain := output.Present() && tokenType == "oauth"
		line := r.dependencies.Debug.Capture(req, body, target, tokenType, mimic, retain)
		if retain {
			state.MimicDebug = line
		}
	}
	return options
}

// requestURL 保留普通、计数和透传端点的原差异，代理查询参数只用于自定义 relay。
func (r *Runtime) requestURL(target *provider.ExecutionAccount, count, passthrough bool) (string, error) {
	endpoint, path := anthropic.ClaudeAPIURL, "/v1/messages"
	if count {
		endpoint, path = anthropic.ClaudeAPICountTokensURL, "/v1/messages/count_tokens"
	}
	if target.Record.Type == capability.AccountTypeAPIKey || (count && passthrough) {
		if base := target.View().GetBaseURL(); base != "" {
			validated, err := r.validateBaseURL(base)
			if err != nil {
				return "", err
			}
			endpoint = validated + path + "?beta=true"
		}
	} else if target.View().IsCustomBaseURLEnabled() {
		custom := target.View().GetCustomBaseURL()
		if custom == "" {
			return "", fmt.Errorf("custom_base_url is enabled but not configured for account %d", target.Record.ID)
		}
		validated, err := r.validateBaseURL(custom)
		if err != nil {
			return "", err
		}
		endpoint = strings.TrimRight(validated, "/") + path + "?beta=true"
		if target.Record.ProxyID != nil && target.Record.Proxy != nil {
			if proxy := target.Record.Proxy.URL(); proxy != "" {
				endpoint += "&proxy=" + url.QueryEscape(proxy)
			}
		}
	}
	return endpoint, nil
}

func (r *Runtime) countOptions(ctx context.Context, output HTTPBoundary, state *AttemptState, target *provider.ExecutionAccount, model, tokenType string, mimic, passthrough bool) anthropic.RequestOptions {
	options := r.requestOptions(ctx, output, state, target, model, tokenType, mimic)
	options.URL = func() (string, error) { return r.requestURL(target, true, passthrough) }
	return options
}

func (r *Runtime) checkBetaTokens(ctx context.Context, tokens []string, target *provider.ExecutionAccount, model string) *anthropic.BetaBlockedError {
	if r.dependencies.Settings == nil || len(tokens) == 0 {
		return nil
	}
	settings, err := r.dependencies.Settings.GetBetaPolicySettings(ctx)
	if err != nil || settings == nil {
		return nil
	}
	return anthropic.CheckBetaPolicyBlockForTokens(provider.AnthropicBetaPolicy(settings), tokens, target.View().IsOAuth(), target.View().IsBedrock(), model)
}
