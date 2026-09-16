package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/util/urlvalidator"

	"github.com/gin-gonic/gin"
)

func (s *GatewayService) buildUpstreamRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, token, tokenType, modelID string, reqStream bool, mimicClaudeCode bool) (*http.Request, []byte, error) {
	if account.Platform == PlatformAnthropic && account.Type == AccountTypeServiceAccount {
		body = stripDeferredToolCacheControl(body)
		req, err := s.buildUpstreamRequestAnthropicVertex(ctx, c, account, body, token, modelID, reqStream)
		return req, body, err
	}
	return claude.BuildRequest(ctx, body, token, tokenType, modelID, reqStream, mimicClaudeCode, s.anthropicRequestOptions(ctx, c, account, modelID, tokenType, mimicClaudeCode))
}

func mergeAnthropicBeta(required []string, incoming string) string {
	return claude.MergeAnthropicBeta(required, incoming)
}

func mergeAnthropicBetaDropping(required []string, incoming string, drop map[string]struct{}) string {
	return claude.MergeAnthropicBetaDropping(required, incoming, drop)
}

func stripBetaTokens(header string, tokens []string) string {
	return claude.StripBetaTokens(header, tokens)
}

func stripBetaTokensWithSet(header string, drop map[string]struct{}) string {
	return claude.StripBetaTokensWithSet(header, drop)
}

type BetaBlockedError = claude.BetaBlockedError

// betaPolicyResult holds the evaluated result of beta policy rules for a single request.
type betaPolicyResult struct {
	blockErr  *BetaBlockedError   // non-nil if a block rule matched
	filterSet map[string]struct{} // tokens to filter (may be nil)
}

func (s *GatewayService) evaluateBetaPolicy(ctx context.Context, betaHeader string, account *Account, model string) betaPolicyResult {
	if s.settingService == nil {
		return betaPolicyResult{}
	}
	settings, err := s.settingService.GetBetaPolicySettings(ctx)
	if err != nil || settings == nil {
		return betaPolicyResult{}
	}
	r := claude.EvaluateBetaPolicy(settings, betaHeader, account.IsOAuth(), account.IsBedrock(), model)
	return betaPolicyResult{blockErr: r.BlockErr, filterSet: r.FilterSet}
}

func mergeDropSets(policySet map[string]struct{}, extra ...string) map[string]struct{} {
	return claude.MergeDropSets(policySet, extra...)
}

// betaPolicyFilterSetKey is the gin.Context key for caching the policy filter set within a request.
const betaPolicyFilterSetKey = "betaPolicyFilterSet"

// getBetaPolicyFilterSet returns the beta policy filter set, using the gin context cache if available.
// In the /v1/messages path, Forward() evaluates the policy first and caches the result;
// buildUpstreamRequest reuses it (zero extra DB calls). In the count_tokens path, this
// evaluates on demand (one DB call).
func (s *GatewayService) getBetaPolicyFilterSet(ctx context.Context, c *gin.Context, account *Account, model string) map[string]struct{} {
	if c != nil {
		if v, ok := c.Get(betaPolicyFilterSetKey); ok {
			if fs, ok := v.(map[string]struct{}); ok {
				return fs
			}
		}
	}
	return s.evaluateBetaPolicy(ctx, "", account, model).filterSet
}

func betaPolicyScopeMatches(scope string, isOAuth bool, isBedrock bool) bool {
	return claude.BetaPolicyScopeMatches(scope, isOAuth, isBedrock)
}

func resolveRuleAction(rule BetaPolicyRule, model string) (action, errorMessage string) {
	return claude.ResolveRuleAction(rule, model)
}

func droppedBetaSet(extra ...string) map[string]struct{} { return claude.DroppedBetaSet(extra...) }

func containsBetaToken(header, token string) bool { return claude.ContainsBetaToken(header, token) }

func filterBetaTokens(tokens []string, filterSet map[string]struct{}) []string {
	return claude.FilterBetaTokens(tokens, filterSet)
}

func (s *GatewayService) resolveBedrockBetaTokensForRequest(
	ctx context.Context,
	account *Account,
	betaHeader string,
	body []byte,
	modelID string,
) ([]string, error) {
	// 1. 对原始 header 中的 beta token 做 block 检查（快速失败）
	policy := s.evaluateBetaPolicy(ctx, betaHeader, account, modelID)
	if policy.blockErr != nil {
		return nil, policy.blockErr
	}

	// 2. 解析 header + body 自动注入 + Bedrock 转换/过滤
	betaTokens := ResolveBedrockBetaTokens(betaHeader, body, modelID)

	// 3. 对最终 token 列表再做 block 检查，捕获通过 body 自动注入绕过 header block 的情况。
	//    例如：管理员 block 了 interleaved-thinking，客户端不在 header 中带该 token，
	//    但请求体中包含 thinking 字段 → autoInjectBedrockBetaTokens 会自动补齐 →
	//    如果不做此检查，block 规则会被绕过。
	if blockErr := s.checkBetaPolicyBlockForTokens(ctx, betaTokens, account, modelID); blockErr != nil {
		return nil, blockErr
	}

	return filterBetaTokens(betaTokens, policy.filterSet), nil
}

func (s *GatewayService) checkBetaPolicyBlockForTokens(ctx context.Context, tokens []string, account *Account, model string) *BetaBlockedError {
	if s.settingService == nil || len(tokens) == 0 {
		return nil
	}
	settings, err := s.settingService.GetBetaPolicySettings(ctx)
	if err != nil || settings == nil {
		return nil
	}
	return claude.CheckBetaPolicyBlockForTokens(settings, tokens, account.IsOAuth(), account.IsBedrock(), model)
}

func buildBetaTokenSet(tokens []string) map[string]struct{} { return claude.BuildBetaTokenSet(tokens) }

func truncateForLog(b []byte, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = 2048
	}
	if len(b) > maxBytes {
		b = b[:maxBytes]
	}
	s := string(b)
	// 保持一行，避免污染日志格式
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

// buildCustomRelayURL 构建自定义中继转发 URL
// 在 path 后附加 beta=true 和可选的 proxy 查询参数
func (s *GatewayService) buildCustomRelayURL(baseURL, path string, account *Account) string {
	u := strings.TrimRight(baseURL, "/") + path + "?beta=true"
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL := account.Proxy.URL()
		if proxyURL != "" {
			u += "&proxy=" + url.QueryEscape(proxyURL)
		}
	}
	return u
}

func (s *GatewayService) validateUpstreamBaseURL(raw string) (string, error) {
	if s.cfg != nil && !s.cfg.Security.URLAllowlist.Enabled {
		normalized, err := urlvalidator.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
		if err != nil {
			return "", fmt.Errorf("invalid base_url: %w", err)
		}
		return normalized, nil
	}
	normalized, err := urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
		AllowedHosts:     s.cfg.Security.URLAllowlist.UpstreamHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.cfg.Security.URLAllowlist.AllowPrivateHosts,
	})
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return normalized, nil
}

func filterVertexBetaTokens(header string, drop map[string]struct{}) string {
	return vertex.FilterBetaTokens(header, drop)
}

// 旧入站适配只提供策略和账号投影，不再拥有 Vertex 构造算法。
func (s *GatewayService) buildUpstreamRequestAnthropicVertex(ctx context.Context, c *gin.Context, account *Account, body []byte, token, modelID string, reqStream bool) (*http.Request, error) {
	var headers http.Header
	var beta string
	if c != nil && c.Request != nil {
		headers = c.Request.Header
		beta = getHeaderRaw(headers, "anthropic-beta")
	}
	return vertex.BuildAnthropicRequest(ctx, body, token, modelID, reqStream, vertex.AnthropicRequestOptions{
		ClientBeta: beta, ClientHeaders: headers, AllowedHeaders: allowedHeaders, Project: account.VertexProjectID, Location: account.VertexLocation,
		Policy: func(ctx context.Context, header string) (map[string]struct{}, error) {
			policy := s.evaluateBetaPolicy(ctx, header, account, modelID)
			if policy.blockErr != nil {
				return nil, policy.blockErr
			}
			return mergeDropSets(policy.filterSet), nil
		},
		SanitizeBody: sanitizeAnthropicBodyForBetaTokens, WireCasing: resolveWireCasing, AddHeader: addHeaderRaw, SetHeader: setHeaderRaw, DeleteHeader: deleteHeaderAllForms,
		Debug: func(header http.Header, data []byte, fields map[string]string) {
			s.debugLogGatewaySnapshot("UPSTREAM_FORWARD_VERTEX_ANTHROPIC", header, data, fields)
		},
	})
}
