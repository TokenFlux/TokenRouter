package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	wireprotocol "github.com/TokenFlux/TokenRouter/internal/protocol"
)

// routes.go registers gateway endpoints, aliases, protocol guards and
// middleware for incoming HTTP requests.
// Gateway aliases need the same authentication and selected-group protocol
// rules as their versioned entry points. Route registration fixes the inbound
// protocol before endpoint execution, while compatible text dispatch chooses
// platform execution from the selected provider. General and text body limits
// are installed at their endpoint scopes.
//
// Responses wildcard paths are validated before forwarding and before Compact
// admission, preventing malformed suffixes from altering an upstream target.
// Google's model:action form requires wildcard capture in Gin. Dedicated
// Antigravity routes install their platform restriction before credential
// admission, and removed Codex model paths are rejected before the dynamic Live
// sideband route can treat them as call IDs.

// RouteEndpoints collects gateway HTTP handlers and externally supplied endpoint functions.
type RouteEndpoints struct {
	CountTokens  *CountTokensHandler  // Messages token-count HTTP handler
	GeminiNative *GeminiNativeHandler // Gemini native protocol handler
	OpenAIText   *OpenAITextHandler   // unified OpenAI text handler
	OpenAITokens *OpenAITokensHandler // OpenAI token preflight handler
	ResponsesWS  gin.HandlerFunc      // Responses WebSocket ingress handler
	Models       *ModelsHandler       // client model catalogue handler
	Messages     *MessagesHandler     // Antigravity Messages entry handler
	Media        *MediaHandler        // image and video HTTP handlers
	Auxiliary    *AuxiliaryHandler    // auxiliary and voice HTTP handlers
	Live         *LiveHandler         // Live creation and sideband handlers
	Search       *SearchHandler       // standalone Web and X handlers
	PublicUsage  gin.HandlerFunc      // authenticated public usage lookup handler
}

// RegisterGatewayRoutes installs gateway endpoint families with ordered
// middleware and aliases. Assembly provides required handlers, authentication,
// request limits and batch registration before serving requests. The root group
// fixes English protocol error language, and optional model attributes precede
// route admission.
//
// Shared aliases retain the same client protocol controls and provider
// execution entry. Responses wildcard suffixes validate before their terminal
// protocol gate, so malformed paths cannot contribute to an upstream target.
// Google model actions use wildcard capture because their colon syntax shares a
// path segment. Antigravity families install a forced platform before
// authentication and retain it through their selected-group execution.
// @project-doc docs/architecture/gateway_request_lifecycle.md#gateway_pipeline
func RegisterGatewayRoutes(engine *gin.Engine, endpoints RouteEndpoints, options RouteMiddleware, registerBatchImages func(*gin.RouterGroup)) {
	// Install protocol language before admission middleware writes errors, so
	// credential and handler failures use the gateway's fixed English response language.
	r := engine.Group("", func(c *gin.Context) {
		ctx := locale.WithLanguage(c.Request.Context(), "en")
		ctx = timing.WithModeReader(ctx, options.TTFTMode)
		c.Request = c.Request.WithContext(ctx)
		c.Header("Content-Language", "en")
		c.Next()
	})
	if options.ModelAttributes != nil {
		r.Use(options.ModelAttributes)
	}
	downstreamKeepalive := options.DownstreamKeepalive
	if downstreamKeepalive == nil {
		downstreamKeepalive = func(c *gin.Context) { c.Next() }
	}
	guards := NewRouteGuards(options)

	requireGroupClientProtocol := guards.RequireGroupClientProtocol
	withGroupClientProtocol := guards.WithGroupClientProtocol
	requireExtendedProtocol := guards.RequireExtendedProtocol
	requireGeminiGenerateContentProtocol := guards.RequireGeminiGenerateContentProtocol
	countTokensHTTP := endpoints.CountTokens
	geminiNativeHTTP, openAITextHTTP, responsesWSHTTP := endpoints.GeminiNative, endpoints.OpenAIText, endpoints.ResponsesWS
	modelsHTTP, messagesHTTP := endpoints.Models, endpoints.Messages
	mediaHTTP, auxiliaryHTTP, liveHTTP, searchHTTP := endpoints.Media, endpoints.Auxiliary, endpoints.Live, endpoints.Search
	bodyLimit := options.BodyLimit
	textBodyLimit := options.TextBodyLimit
	clientRequestID := options.ClientRequestID
	opsErrorLogger := options.OpsErrorLogger
	endpointNorm := options.EndpointNormalization
	messagesProtocolGate := requireGroupClientProtocol(wireprotocol.ProtocolAnthropicMessages, GroupClientProtocolErrorAnthropic)
	responsesProtocolGate := requireGroupClientProtocol(wireprotocol.ProtocolOpenAIResponses, GroupClientProtocolErrorOpenAI)
	chatCompletionsProtocolGate := requireGroupClientProtocol(wireprotocol.ProtocolOpenAIChatCompletions, GroupClientProtocolErrorOpenAI)

	// Group requirements report errors in the client's protocol format.
	requireGroupAnthropic := options.RequireGroupAnthropic
	requireGroupGoogle := options.RequireGroupGoogle

	responsesInputTokensHandler := func(c *gin.Context) { endpoints.OpenAITokens.ResponsesInputTokens(c) }
	// Forced-platform aliases are carried through to the count_tokens handler.

	// Count requests consume the Messages permission even though their helper
	// entry performs preflight counting instead of generating or billing an answer.
	countTokensProtocolGate := func(c *gin.Context) { messagesProtocolGate(c) }
	countTokensHandler := func(c *gin.Context) { countTokensHTTP.CountTokens(c) }
	imagesHandler := func(c *gin.Context) { mediaHTTP.Images(c) }
	videoGenerationHandler := func(c *gin.Context) { mediaHTTP.GrokVideoGeneration(c) }
	videoStatusHandler := func(c *gin.Context) { mediaHTTP.GrokVideoStatus(c) }
	videoContentHandler := func(c *gin.Context) { mediaHTTP.GrokVideoContent(c) }
	videoEditHandler := func(c *gin.Context) { mediaHTTP.GrokVideoEdit(c) }
	videoExtensionHandler := func(c *gin.Context) { mediaHTTP.GrokVideoExtension(c) }

	responsesWebSocketHandler := func(c *gin.Context) { responsesWSHTTP(c) }
	// The dynamic call-ID route also matches models. Reject that removed
	// resource before authentication or sideband lookup can interpret it as a Live call.
	rejectRemovedCodexRoute := func(c *gin.Context) {
		if c.Param("call_id") == "models" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
	// Responses subpaths are appended to the upstream endpoint, so reject malformed paths
	// before scheduling or forwarding. The validation is shared with
	// IsForwardableOpenAIResponsesRequestPath in responses_path.go.
	guardResponsesSubpath := func(next gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			if !IsForwardableOpenAIResponsesRequestPath(c) {
				options.ObserveBusinessLimit(c, RouteLimitLocalPolicyDenied)
				c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
					"error": gin.H{
						"type":    "not_found_error",
						"message": "Unsupported responses subpath",
					},
				})
				return
			}
			next(c)
		}
	}

	// The /v1 group serves the standard client protocol endpoints.
	gateway := r.Group("/v1")
	gateway.Use(bodyLimit)
	gateway.Use(clientRequestID, TextRequestTiming(), downstreamKeepalive)
	gateway.Use(opsErrorLogger)
	gateway.Use(endpointNorm)
	gateway.Use(options.APIKeyAuth)
	gateway.Use(requireGroupAnthropic, requireExtendedProtocol)
	{
		// Text routes fix the client protocol; provider selection determines platform execution.
		gateway.POST("/messages", messagesProtocolGate, func(c *gin.Context) { openAITextHTTP.Messages(c) })
		// The count_tokens handler bridges OpenAI upstream, estimates Grok locally,
		// and handles other Anthropic-compatible platforms.
		gateway.POST("/messages/count_tokens", countTokensProtocolGate, countTokensHandler)
		gateway.GET("/models", modelsHTTP.Models)
		gateway.GET("/usage", endpoints.PublicUsage)
		gateway.POST("/live", liveHTTP.Live)
		gateway.GET("/live/:call_id", liveHTTP.LiveSideband)
		// Responses execution uses the selected provider's capabilities.
		gateway.POST("/responses", responsesProtocolGate, func(c *gin.Context) {
			if IsOpenAIResponsesInputTokensRequestPath(c) {
				responsesInputTokensHandler(c)
				return
			}
			openAITextHTTP.Responses(c)
		})
		gateway.POST("/responses/*subpath", guardResponsesSubpath(withGroupClientProtocol(wireprotocol.ProtocolOpenAIResponses, GroupClientProtocolErrorOpenAI, func(c *gin.Context) {
			if IsOpenAIResponsesInputTokensRequestPath(c) {
				responsesInputTokensHandler(c)
				return
			}
			openAITextHTTP.Responses(c)
		})))
		gateway.POST("/alpha/search", textBodyLimit, auxiliaryHTTP.AlphaSearch)
		gateway.GET("/responses", responsesWebSocketHandler)
		// Chat Completions execution uses the selected provider's capabilities.
		gateway.POST("/chat/completions", chatCompletionsProtocolGate, func(c *gin.Context) { openAITextHTTP.ChatCompletions(c) })
		gateway.POST("/systemone", textBodyLimit, func(c *gin.Context) { auxiliaryHTTP.SystemOne(c) })
		gateway.POST("/embeddings", textBodyLimit, func(c *gin.Context) { auxiliaryHTTP.Embeddings(c) })
		gateway.POST("/images/generations", imagesHandler)
		gateway.POST("/images/edits", imagesHandler)
		registerBatchImages(gateway)

		// OpenAI-compatible clients can create video tasks through /videos; the Grok forwarder
		// maps this alias to xAI's /videos/generations endpoint.
		gateway.POST("/videos", videoGenerationHandler)
		gateway.POST("/videos/generations", videoGenerationHandler)
		gateway.POST("/videos/edits", videoEditHandler)
		gateway.POST("/videos/extensions", videoExtensionHandler)
		gateway.GET("/videos/generations/:request_id/content", videoContentHandler)
		gateway.GET("/videos/edits/:request_id/content", videoContentHandler)
		gateway.GET("/videos/extensions/:request_id/content", videoContentHandler)
		gateway.GET("/videos/generations/:request_id", videoStatusHandler)
		gateway.GET("/videos/edits/:request_id", videoStatusHandler)
		gateway.GET("/videos/extensions/:request_id", videoStatusHandler)
		gateway.GET("/videos/:request_id", videoStatusHandler)
		gateway.GET("/videos/:request_id/content", videoContentHandler)

		// xAI Voice requests use the Grok platform for HTTP TTS/STT and realtime WebSocket.
		voiceHandler := func(endpoint string) gin.HandlerFunc {
			return func(c *gin.Context) {
				auxiliaryHTTP.GrokVoice(c, endpoint)
			}
		}
		gateway.POST("/tts", voiceHandler("tts"))
		gateway.POST("/stt", voiceHandler("stt"))
		gateway.POST("/custom-voices", voiceHandler("custom-voices"))
		customVoicePathHandler := func(c *gin.Context) {
			auxiliaryHTTP.GrokVoice(c, grokCustomVoiceEndpoint(c))
		}
		gateway.GET("/custom-voices", voiceHandler("custom-voices"))
		gateway.GET("/custom-voices/:voice_id/audio", customVoicePathHandler)
		gateway.GET("/custom-voices/:voice_id", customVoicePathHandler)
		gateway.PATCH("/custom-voices/:voice_id", customVoicePathHandler)
		gateway.DELETE("/custom-voices/:voice_id", customVoicePathHandler)
		gateway.GET("/realtime", func(c *gin.Context) {
			auxiliaryHTTP.GrokRealtime(c)
		})
		gateway.POST("/web_search", func(c *gin.Context) {
			searchHTTP.WebSearch(c)
		})
		gateway.POST("/x_search", func(c *gin.Context) {
			searchHTTP.XSearch(c)
		})
	}

	// The Gemini SDK and CLI use the native /v1beta endpoints.
	gemini := r.Group("/v1beta")
	gemini.Use(bodyLimit)
	gemini.Use(clientRequestID, TextRequestTiming(), downstreamKeepalive)
	gemini.Use(opsErrorLogger)
	gemini.Use(endpointNorm)
	gemini.Use(options.GoogleAPIKeyAuth)
	gemini.Use(requireGroupGoogle)
	{
		gemini.GET("/models", modelsHTTP.GeminiV1BetaListModels)
		gemini.GET("/models/*model", modelsHTTP.GeminiV1BetaGetModel)
		// Capture the whole model/action suffix because Gemini places a colon
		// inside its segment and Gin otherwise interprets it as a route parameter marker.
		gemini.POST("/models/*modelAction", requireGeminiGenerateContentProtocol, geminiNativeHTTP.GeminiV1BetaModels)
	}

	// Responses aliases without /v1 use the same provider selection and execution.
	responsesHandler := func(c *gin.Context) {
		if IsOpenAIResponsesInputTokensRequestPath(c) {
			responsesInputTokensHandler(c)
			return
		}
		openAITextHTTP.Responses(c)
	}
	r.POST("/responses", bodyLimit, clientRequestID, TextRequestTiming(), downstreamKeepalive, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, responsesProtocolGate, responsesHandler)
	r.POST("/responses/*subpath", bodyLimit, clientRequestID, TextRequestTiming(), downstreamKeepalive, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, guardResponsesSubpath(withGroupClientProtocol(wireprotocol.ProtocolOpenAIResponses, GroupClientProtocolErrorOpenAI, responsesHandler)))
	r.POST("/alpha/search", textBodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, auxiliaryHTTP.AlphaSearch)
	r.GET("/responses", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, responsesWebSocketHandler)
	// Codex clients use /models for the same local catalogue served by /v1/models.
	r.GET("/models", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, modelsHTTP.Models)
	r.POST("/messages/count_tokens", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, countTokensProtocolGate, countTokensHandler)
	r.GET(
		"/backend-api/codex/:call_id",
		rejectRemovedCodexRoute,
		bodyLimit,
		clientRequestID,
		opsErrorLogger,
		endpointNorm,
		options.APIKeyAuth,
		requireGroupAnthropic, requireExtendedProtocol,
		liveHTTP.LiveSideband,
	)
	codexDirect := r.Group("/backend-api/codex")
	codexDirect.Use(bodyLimit, clientRequestID, TextRequestTiming(), downstreamKeepalive, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol)
	{
		codexDirect.POST("/realtime/calls", liveHTTP.Live)
		codexDirect.POST("/responses", responsesProtocolGate, responsesHandler)
		codexDirect.POST("/responses/*subpath", guardResponsesSubpath(withGroupClientProtocol(wireprotocol.ProtocolOpenAIResponses, GroupClientProtocolErrorOpenAI, responsesHandler)))
		codexDirect.POST("/alpha/search", textBodyLimit, auxiliaryHTTP.AlphaSearch)
		codexDirect.GET("/responses", responsesWebSocketHandler)
	}
	// Chat Completions aliases without /v1 use the same provider selection and execution.
	r.POST("/chat/completions", bodyLimit, clientRequestID, TextRequestTiming(), downstreamKeepalive, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, chatCompletionsProtocolGate, func(c *gin.Context) { openAITextHTTP.ChatCompletions(c) })
	r.POST("/embeddings", textBodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, func(c *gin.Context) { auxiliaryHTTP.Embeddings(c) })
	r.POST("/images/generations", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, imagesHandler)
	r.POST("/images/edits", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, imagesHandler)
	// Bare video creation aliases share the generation control and handler with
	// /v1/videos, while status and content routes retain task ownership checks.
	r.POST("/videos", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, videoGenerationHandler)
	r.POST("/videos/generations", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, videoGenerationHandler)
	r.POST("/videos/edits", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, videoEditHandler)
	r.POST("/videos/extensions", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, videoExtensionHandler)
	r.GET("/videos/generations/:request_id/content", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, videoContentHandler)
	r.GET("/videos/edits/:request_id/content", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, videoContentHandler)
	r.GET("/videos/extensions/:request_id/content", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, videoContentHandler)
	r.GET("/videos/generations/:request_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, videoStatusHandler)
	r.GET("/videos/edits/:request_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, videoStatusHandler)
	r.GET("/videos/extensions/:request_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, videoStatusHandler)
	r.GET("/videos/:request_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, videoStatusHandler)
	r.GET("/videos/:request_id/content", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, videoContentHandler)

	rootVoiceHandler := func(endpoint string) gin.HandlerFunc {
		return func(c *gin.Context) {
			auxiliaryHTTP.GrokVoice(c, endpoint)
		}
	}
	r.POST("/tts", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootVoiceHandler("tts"))
	r.POST("/stt", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootVoiceHandler("stt"))
	r.POST("/custom-voices", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootVoiceHandler("custom-voices"))
	rootCustomVoicePathHandler := func(c *gin.Context) {
		auxiliaryHTTP.GrokVoice(c, grokCustomVoiceEndpoint(c))
	}
	r.GET("/custom-voices", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootVoiceHandler("custom-voices"))
	r.GET("/custom-voices/:voice_id/audio", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootCustomVoicePathHandler)
	r.GET("/custom-voices/:voice_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootCustomVoicePathHandler)
	r.PATCH("/custom-voices/:voice_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootCustomVoicePathHandler)
	r.DELETE("/custom-voices/:voice_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootCustomVoicePathHandler)
	r.GET("/realtime", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, func(c *gin.Context) {
		auxiliaryHTTP.GrokRealtime(c)
	})
	r.POST("/web_search", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, func(c *gin.Context) {
		searchHTTP.WebSearch(c)
	})
	r.POST("/x_search", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, func(c *gin.Context) {
		searchHTTP.XSearch(c)
	})

	r.GET("/antigravity/models", options.ForceAntigravity, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, modelsHTTP.AntigravityModels)

	// Antigravity-specific routes restrict selection to Antigravity providers.
	antigravityV1 := r.Group("/antigravity/v1")
	antigravityV1.Use(bodyLimit)
	antigravityV1.Use(clientRequestID, TextRequestTiming(), downstreamKeepalive)
	antigravityV1.Use(opsErrorLogger)
	antigravityV1.Use(endpointNorm)
	antigravityV1.Use(options.ForceAntigravity)
	antigravityV1.Use(options.APIKeyAuth)
	antigravityV1.Use(requireGroupAnthropic, requireExtendedProtocol)
	{
		antigravityV1.POST("/messages", messagesProtocolGate, messagesHTTP.Messages)
		antigravityV1.POST("/messages/count_tokens", countTokensProtocolGate, countTokensHandler)
		antigravityV1.GET("/models", modelsHTTP.AntigravityModels)
		antigravityV1.GET("/usage", endpoints.PublicUsage)
	}

	antigravityV1Beta := r.Group("/antigravity/v1beta")
	antigravityV1Beta.Use(bodyLimit)
	antigravityV1Beta.Use(clientRequestID, TextRequestTiming(), downstreamKeepalive)
	antigravityV1Beta.Use(opsErrorLogger)
	antigravityV1Beta.Use(endpointNorm)
	antigravityV1Beta.Use(options.ForceAntigravity)
	antigravityV1Beta.Use(options.GoogleAPIKeyAuth)
	antigravityV1Beta.Use(requireGroupGoogle)
	{
		antigravityV1Beta.GET("/models", modelsHTTP.GeminiV1BetaListModels)
		antigravityV1Beta.GET("/models/*model", modelsHTTP.GeminiV1BetaGetModel)
		antigravityV1Beta.POST("/models/*modelAction", requireGeminiGenerateContentProtocol, geminiNativeHTTP.GeminiV1BetaModels)
	}
}

// grokCustomVoiceEndpoint constructs the upstream custom-voice resource path
// from the route parameter. A matched audio route appends /audio; callers
// establish the valid resource route and voice ID before forwarding this
// endpoint.
func grokCustomVoiceEndpoint(c *gin.Context) string {
	endpoint := "custom-voices/" + c.Param("voice_id")
	if strings.HasSuffix(c.FullPath(), "/:voice_id/audio") {
		endpoint += "/audio"
	}
	return endpoint
}
