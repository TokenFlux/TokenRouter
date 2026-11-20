package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_request_client_policy.go enforces OAuth client restrictions and
// resolves request TLS and user-agent policy.
// OAuth client eligibility reads caller identity and current global permissions
// before an auxiliary or long-lived entry sends upstream traffic. TLS routing
// and user-agent policy use the selected provider, while rejection logging
// records request metadata and HTTP enforcement leaves WebSocket error output
// to its caller.

// OpenAIClientPolicyForbiddenMessage returns client-facing text for the
// configured restriction policy. Codex-only and TLS-router-only policies
// receive specific messages; other policy values use the
// configured-OpenAI-client message suitable for the rejection response.
func OpenAIClientPolicyForbiddenMessage(result providercore.CodexClientRestrictionDetectionResult) string {
	if result.Policy == providercore.OpenAIOAuthClientPolicyCodexOnly {
		return "This provider only allows Codex official clients"
	}
	if result.Policy == providercore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly {
		return "This provider only allows clients matched by the configured TLS router"
	}
	return "This provider only allows configured OpenAI OAuth clients"
}

// DetectClient checks provider restrictions using the HTTP user-agent and
// originator. It uses the inbound context when available, otherwise background
// context, and supplies empty identity strings when no Gin context is present.
func (s *OpenAIRequests) DetectClient(c *gin.Context, provider *gatewayprovider.ExecutionProvider, tlsRouterMatch egress.TLSFingerprintRouterMatchResult) providercore.CodexClientRestrictionDetectionResult {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	return s.DetectClientInput(ctx, func() (string, string) {
		if c == nil {
			return "", ""
		}
		return c.GetHeader("User-Agent"), c.GetHeader("originator")
	}, provider, tlsRouterMatch)
}

// DetectClientInput checks the supplied client identity with current provider
// restrictions. For Codex-only providers, configured runtime readers can add
// the Claude Code plugin permission. The configured detector, or
// clientDetector's default, evaluates the identity callback and TLS-router
// match.
func (s *OpenAIRequests) DetectClientInput(ctx context.Context, readClient func() (string, string), provider *gatewayprovider.ExecutionProvider, tlsRouterMatch egress.TLSFingerprintRouterMatchResult) providercore.CodexClientRestrictionDetectionResult {
	var globalAllowedClients []string
	if provider != nil && provider.View().IsCodexCLIOnlyEnabled() && s != nil && s.Readers != nil {
		if s.Readers.Gateway.IsOpenAIAllowClaudeCodeCodexPluginEnabled(ctx) {
			globalAllowedClients = []string{openai.AllowedClientClaudeCode}
		}
	}
	return s.clientDetector().DetectClient(readClient, gatewayprovider.ExecutionRecord(provider), globalAllowedClients, tlsRouterMatch.Matched)
}

// LogCodexCLIOnlyDetection records the outcome of an enabled OAuth client
// restriction. Disabled restrictions skip logging. Accepted requests use info
// level; rejected requests use warning level with filtered request details, and
// a nil context uses background logging.
func LogCodexCLIOnlyDetection(ctx context.Context, c *gin.Context, provider *gatewayprovider.ExecutionProvider, apiKeyID int64, result providercore.CodexClientRestrictionDetectionResult, body []byte) {
	if !result.Enabled {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	providerID := int64(0)
	if provider != nil {
		providerID = provider.Record.ID
	}
	fields := []zap.Field{
		zap.String("component", "service.openai_gateway"),
		zap.Int64("provider_id", providerID),
		zap.Bool("codex_cli_only_enabled", result.Enabled),
		zap.Bool("codex_official_client_match", result.Matched),
		zap.String("reject_reason", result.Reason),
	}
	if apiKeyID > 0 {
		fields = append(fields, zap.Int64("api_key_id", apiKeyID))
	}
	if !result.Matched {
		fields = AppendCodexRejectedRequestFields(fields, c, body)
	}
	log := logging.FromContext(ctx).With(fields...)
	if result.Matched {
		log.Info("OpenAI codex_cli_only allowed request")
		return
	}
	log.Warn("OpenAI codex_cli_only rejected unofficial client request")
}

// EnforceClient rejects a disallowed OAuth client and records a local-policy
// limitation. Disabled or matched restrictions return nil. HTTP requests
// mark semantic commitment and receive a 403 JSON response or a terminal SSE
// error when event-stream framing is already committed, while WebSocket
// callers receive the error for their own frame handling.
func (s *OpenAIRequests) EnforceClient(ctx context.Context, c *gin.Context, provider *gatewayprovider.ExecutionProvider, body []byte, tlsRouterMatch egress.TLSFingerprintRouterMatchResult) error {
	result := s.DetectClient(c, provider, tlsRouterMatch)
	apiKeyID := APIKeyIDFromContext(c)
	LogCodexCLIOnlyDetection(ctx, c, provider, apiKeyID, result, body)
	if !result.Enabled || result.Matched {
		return nil
	}
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	if c != nil && GetOpenAIClientTransport(c) != OpenAIClientTransportWS {
		MarkResponseCommitted(c)
		message := OpenAIClientPolicyForbiddenMessage(result)
		if CommittedSSE(c) {
			writeCommittedClientSSEError(c, http.StatusForbidden, "forbidden_error", "", message)
		} else {
			c.JSON(http.StatusForbidden, gin.H{
				"error": gin.H{
					"type":    "forbidden_error",
					"message": message,
				},
			})
		}
	}
	return errors.New("openai oauth client policy restriction: client is not allowed")
}

// ApplyUserAgentHeader applies the HTTP user-agent policy to a handshake header
// map. The supplied map is modified through a temporary request; nil headers
// are ignored. Only the first optional router match is consumed by
// ApplyUserAgent.
func (s *OpenAIRequests) ApplyUserAgentHeader(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	headers http.Header,
	passthrough bool,
	routerMatch ...egress.TLSFingerprintRouterMatchResult,
) {
	if headers == nil {
		return
	}
	req := &http.Request{Header: headers}
	s.ApplyUserAgent(ctx, c, provider, req, passthrough, routerMatch...)
}

// MatchTLS resolves the selected provider's TLS-router match from HTTP
// User-Agent. A nil Gin context supplies an empty user-agent string;
// MatchTLSInput defers that read until a configured router is actually
// available.
func (s *OpenAIRequests) MatchTLS(c *gin.Context, provider *gatewayprovider.ExecutionProvider) egress.TLSFingerprintRouterMatchResult {
	return s.MatchTLSInput(func() string {
		if c == nil {
			return ""
		}
		return c.GetHeader("User-Agent")
	}, provider)
}

// MatchTLSInput matches User-Agent against the selected provider's configured
// TLS router. Unavailable service, router, provider, or router ID returns an
// empty match before invoking readUserAgent. Callers provide that callback for
// an enabled router.
func (s *OpenAIRequests) MatchTLSInput(readUserAgent func() string, provider *gatewayprovider.ExecutionProvider) egress.TLSFingerprintRouterMatchResult {
	if s == nil || s.Routers == nil || provider == nil || provider.View().GetTLSFingerprintRouterID() <= 0 {
		return egress.TLSFingerprintRouterMatchResult{}
	}
	userAgent := readUserAgent()
	return s.Routers.MatchUserAgent(provider.View().GetTLSFingerprintRouterID(), userAgent)
}

// TLSProfile resolves outgoing TLS selection for the provider and optional
// router match. A missing service or profile reader returns nil. The profile
// service applies its provider defaults and router-to-fixed-profile recovery
// rules.
func (s *OpenAIRequests) TLSProfile(value *gatewayprovider.ExecutionProvider, routerMatch ...egress.TLSFingerprintRouterMatchResult) *tlsfingerprint.Profile {
	if s == nil || s.Profiles == nil {
		return nil
	}
	return s.Profiles.ResolveRequestTLS(gatewayprovider.ExecutionTLSSelection(value, routerMatch))
}

// WSTLSProfile resolves an HTTP/1-compatible WebSocket profile and its pool
// identity. Unavailable profile selection returns nil and an empty identity.
// The derived identity includes the HTTP/1 profile cache key so pooling follows
// the applied profile.
func (s *OpenAIRequests) WSTLSProfile(provider *gatewayprovider.ExecutionProvider, routerMatch ...egress.TLSFingerprintRouterMatchResult) (*tlsfingerprint.Profile, string) {
	profile := s.TLSProfile(provider, routerMatch...)
	if profile == nil {
		return nil, ""
	}
	// Responses WebSocket performs an HTTP/1.1 Upgrade. Remove h2 from the
	// selected profile before deriving the identity used to distinguish
	// pooled connections.
	profile = tlsfingerprint.HTTP1OnlyProfile(profile)
	return profile, egress.WebSocketTLSIdentity(gatewayprovider.ExecutionTLSSelection(provider, routerMatch), true, tlsfingerprint.CacheKey(profile))
}

// ApplyUserAgent applies the configured probe policy to the outgoing request
// header. It forwards the current provider and first optional router match in
// the supplied passthrough mode. Callers configure ClientPolicy before invoking
// this helper.
func (s *OpenAIRequests) ApplyUserAgent(ctx context.Context, _ *gin.Context, value *gatewayprovider.ExecutionProvider, req *http.Request, passthrough bool, matches ...egress.TLSFingerprintRouterMatchResult) {
	var match egress.TLSFingerprintRouterMatchResult
	if len(matches) > 0 {
		match = matches[0]
	}
	s.ClientPolicy.ApplyUserAgent(ctx, gatewayprovider.ExecutionRecord(value), req, passthrough, match)
}

// clientDetector returns the injected client restriction detector or a
// configured default. The default uses strict official-client checks and
// allowed-client matching; it honors startup ForceCLI when a request service is
// present.
func (s *OpenAIRequests) clientDetector() providercore.ClientRestrictionDetector {
	if s != nil && s.Detector != nil {
		return s.Detector
	}
	// Direct request fixtures may omit a detector; derive the default from
	// startup emulation and the same official-client predicates used by
	// configured services.
	force := s != nil && s.Options.ForceCLI
	return &providercore.CodexClientDetector{Options: providercore.CodexClientOptions{ForceCLI: force, OfficialUserAgent: openai.IsCodexOfficialClientRequestStrict, OfficialOriginator: openai.IsCodexOfficialClientOriginator, AllowedClients: openai.MatchAllowedClients}}
}
