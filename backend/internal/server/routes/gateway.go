package routes

import (
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

type groupClientProtocolErrorFormat string

const (
	groupClientProtocolErrorAnthropic groupClientProtocolErrorFormat = "anthropic"
	groupClientProtocolErrorOpenAI    groupClientProtocolErrorFormat = "openai"
	groupClientProtocolErrorGoogle    groupClientProtocolErrorFormat = "google"
)

// requireGroupClientProtocol 在进入业务处理器前执行分组协议准入检查。
func requireGroupClientProtocol(protocol service.GroupClientProtocol, format groupClientProtocolErrorFormat) gin.HandlerFunc {
	return func(c *gin.Context) {
		if enforceGroupClientProtocol(c, protocol, format) {
			c.Next()
		}
	}
}

// withGroupClientProtocol 把协议门禁包在已完成路径校验的终端处理器外层。
func withGroupClientProtocol(protocol service.GroupClientProtocol, format groupClientProtocolErrorFormat, next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if enforceGroupClientProtocol(c, protocol, format) {
			next(c)
		}
	}
}

// enforceGroupClientProtocol 执行检查并在拒绝时写入协议原生错误。
func enforceGroupClientProtocol(c *gin.Context, protocol service.GroupClientProtocol, format groupClientProtocolErrorFormat) bool {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil || apiKey.Group.AllowsClientProtocol(protocol) {
		return true
	}

	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	message := groupClientProtocolDeniedMessage(protocol)
	switch format {
	case groupClientProtocolErrorGoogle:
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"code":    http.StatusForbidden,
				"message": message,
				"status":  "PERMISSION_DENIED",
			},
		})
	case groupClientProtocolErrorOpenAI:
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"message": message,
				"type":    "permission_error",
				"param":   nil,
				"code":    "protocol_not_allowed",
			},
		})
	default:
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "permission_error",
				"message": message,
			},
		})
	}
	return false
}

func groupClientProtocolDeniedMessage(protocol service.GroupClientProtocol) string {
	switch protocol {
	case service.GroupClientProtocolAnthropicMessages:
		return "This group does not allow Anthropic Messages requests"
	case service.GroupClientProtocolOpenAIResponses:
		return "This group does not allow OpenAI Responses requests"
	case service.GroupClientProtocolOpenAIChatCompletions:
		return "This group does not allow OpenAI Chat Completions requests"
	case service.GroupClientProtocolGeminiGenerateContent:
		return "This group does not allow Gemini GenerateContent requests"
	default:
		return "This group does not allow the requested client protocol"
	}
}

// requireGeminiGenerateContentProtocol 只门禁 Gemini 的三个文本生成 POST 动作。
func requireGeminiGenerateContentProtocol(c *gin.Context) {
	rest := strings.TrimSpace(strings.TrimPrefix(c.Param("modelAction"), "/"))
	separator := strings.LastIndexAny(rest, ":/")
	if separator <= 0 || separator == len(rest)-1 {
		c.Next()
		return
	}
	switch rest[separator+1:] {
	case "generateContent", "streamGenerateContent", "countTokens":
		requireGroupClientProtocol(service.GroupClientProtocolGeminiGenerateContent, groupClientProtocolErrorGoogle)(c)
	default:
		c.Next()
	}
}

// RegisterGatewayRoutes 注册 API 网关路由（Claude/OpenAI/Gemini 兼容）
// @project-doc docs/architecture/gateway_request_lifecycle.md#gateway_pipeline
// @project-doc docs/interfaces/upstream_account_matrix.md#public_gateway_protocols
func RegisterGatewayRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	cfg *config.Config,
) {
	bodyLimit := middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize)
	textBodyLimit := middleware.RequestBodyLimit(cfg.Gateway.TextMaxBodySize)
	clientRequestID := middleware.ClientRequestID()
	opsErrorLogger := handler.OpsErrorLoggerMiddleware(opsService)
	endpointNorm := handler.InboundEndpointMiddleware()
	messagesProtocolGate := requireGroupClientProtocol(service.GroupClientProtocolAnthropicMessages, groupClientProtocolErrorAnthropic)
	responsesProtocolGate := requireGroupClientProtocol(service.GroupClientProtocolOpenAIResponses, groupClientProtocolErrorOpenAI)
	chatCompletionsProtocolGate := requireGroupClientProtocol(service.GroupClientProtocolOpenAIChatCompletions, groupClientProtocolErrorOpenAI)

	// 未分组 Key 拦截中间件（按协议格式区分错误响应）
	requireGroupAnthropic := middleware.RequireGroupAssignment(settingService, middleware.AnthropicErrorWriter)
	requireGroupGoogle := middleware.RequireGroupAssignment(settingService, middleware.GoogleErrorWriter)

	isOpenAIResponsesCompatibleGatewayPlatform := func(c *gin.Context) bool {
		switch getGroupPlatform(c) {
		case service.PlatformOpenAI, service.PlatformGrok:
			return true
		default:
			return false
		}
	}
	// count_tokens 需要识别强制平台别名，避免把 Antigravity 当成分组原平台处理。
	countTokensPlatform := func(c *gin.Context) string {
		if platform, ok := middleware.GetForcePlatformFromContext(c); ok && strings.TrimSpace(platform) != "" {
			return platform
		}
		return getGroupPlatform(c)
	}
	// 只有实际支持 count_tokens 的平台才受 Messages 协议门禁控制。
	countTokensProtocolGate := func(c *gin.Context) {
		switch countTokensPlatform(c) {
		case service.PlatformAntigravity, service.PlatformQoder:
			c.Next()
		default:
			messagesProtocolGate(c)
		}
	}
	countTokensHandler := func(c *gin.Context) {
		switch countTokensPlatform(c) {
		case service.PlatformAntigravity, service.PlatformQoder:
			c.JSON(http.StatusNotFound, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "not_found_error",
					"message": "count_tokens endpoint is not supported for this platform",
				},
			})
		case service.PlatformOpenAI:
			h.OpenAIGateway.CountTokens(c)
		case service.PlatformGrok:
			h.OpenAIGateway.GrokCountTokens(c)
		default:
			h.Gateway.CountTokens(c)
		}
	}
	imagesHandler := func(c *gin.Context) {
		switch getGroupPlatform(c) {
		case service.PlatformOpenAI:
			h.OpenAIGateway.Images(c)
		case service.PlatformGrok:
			h.OpenAIGateway.GrokImages(c)
		default:
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "not_found_error",
					"message": "Images API is not supported for this platform",
				},
			})
		}
	}
	videoGenerationHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformGrok {
			h.OpenAIGateway.GrokVideoGeneration(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	videoStatusHandler := func(c *gin.Context) {
		apiKey, _ := middleware.GetAPIKeyFromContext(c)
		if getGroupPlatform(c) == service.PlatformGrok || (apiKey != nil && apiKey.IsComposite) {
			h.OpenAIGateway.GrokVideoStatus(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	videoContentHandler := func(c *gin.Context) {
		apiKey, _ := middleware.GetAPIKeyFromContext(c)
		if getGroupPlatform(c) == service.PlatformGrok || (apiKey != nil && apiKey.IsComposite) {
			h.OpenAIGateway.GrokVideoContent(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	videoEditHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformGrok {
			h.OpenAIGateway.GrokVideoEdit(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	videoExtensionHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformGrok {
			h.OpenAIGateway.GrokVideoExtension(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	qoderResponsesSubpathUnsupported := func(c *gin.Context) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Qoder Responses subpaths are not supported",
			},
		})
	}
	responsesWebSocketUnsupported := func(c *gin.Context) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		message := "Responses WebSocket is not supported for this upstream platform"
		if getGroupPlatform(c) == service.PlatformQoder {
			message = "Qoder Responses WebSocket is not supported"
		}
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": message,
			},
		})
	}
	responsesWebSocketHandler := func(c *gin.Context) {
		switch getGroupPlatform(c) {
		case service.PlatformOpenAI, service.PlatformGrok:
			h.OpenAIGateway.ResponsesWebSocket(c)
		default:
			responsesWebSocketUnsupported(c)
		}
	}
	// Sideband 动态段不能吞掉 fork 已明确移除的旧 Codex models 路由。
	rejectRemovedCodexRoute := func(c *gin.Context) {
		if c.Param("call_id") == "models" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
	// /responses/*subpath 的子路径会被转发到上游同名端点之后，因此在入口就拒掉
	// 不可转发的子路径，不让它进入调度与转发流程。可转发的判定见
	// service.IsForwardableOpenAIResponsesRequestPath 及 upstream_path_guard.go。
	guardResponsesSubpath := func(next gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			if !service.IsForwardableOpenAIResponsesRequestPath(c) {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
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

	// API网关（Claude API兼容）
	gateway := r.Group("/v1")
	gateway.Use(bodyLimit)
	gateway.Use(clientRequestID)
	gateway.Use(opsErrorLogger)
	gateway.Use(endpointNorm)
	gateway.Use(gin.HandlerFunc(apiKeyAuth))
	gateway.Use(requireGroupAnthropic)
	{
		// /v1/messages: auto-route based on group platform
		gateway.POST("/messages", messagesProtocolGate, func(c *gin.Context) {
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				h.OpenAIGateway.Messages(c)
				return
			}
			if getGroupPlatform(c) == service.PlatformQoder {
				h.QoderGateway.Messages(c)
				return
			}
			h.Gateway.Messages(c)
		})
		// /v1/messages/count_tokens：OpenAI 桥接上游，Grok 本地估算，其余 Anthropic
		// 兼容平台保留原处理路径。
		gateway.POST("/messages/count_tokens", countTokensProtocolGate, countTokensHandler)
		gateway.GET("/models", h.Gateway.Models)
		gateway.GET("/usage", h.Gateway.Usage)
		gateway.POST("/live", h.OpenAIGateway.Live)
		gateway.GET("/live/:call_id", h.OpenAIGateway.LiveSideband)
		// OpenAI Responses API: auto-route based on group platform
		gateway.POST("/responses", responsesProtocolGate, func(c *gin.Context) {
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				h.OpenAIGateway.Responses(c)
				return
			}
			if getGroupPlatform(c) == service.PlatformQoder {
				h.QoderGateway.Responses(c)
				return
			}
			h.Gateway.Responses(c)
		})
		gateway.POST("/responses/*subpath", guardResponsesSubpath(withGroupClientProtocol(service.GroupClientProtocolOpenAIResponses, groupClientProtocolErrorOpenAI, func(c *gin.Context) {
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				h.OpenAIGateway.Responses(c)
				return
			}
			if getGroupPlatform(c) == service.PlatformQoder {
				qoderResponsesSubpathUnsupported(c)
				return
			}
			h.Gateway.Responses(c)
		})))
		gateway.POST("/alpha/search", textBodyLimit, h.OpenAIGateway.AlphaSearch)
		gateway.GET("/responses", responsesWebSocketHandler)
		// OpenAI Chat Completions API: auto-route based on group platform
		gateway.POST("/chat/completions", chatCompletionsProtocolGate, func(c *gin.Context) {
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				h.OpenAIGateway.ChatCompletions(c)
				return
			}
			if getGroupPlatform(c) == service.PlatformQoder {
				h.QoderGateway.ChatCompletions(c)
				return
			}
			h.Gateway.ChatCompletions(c)
		})
		gateway.POST("/embeddings", textBodyLimit, func(c *gin.Context) {
			if getGroupPlatform(c) != service.PlatformOpenAI {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{
					"error": gin.H{
						"type":    "not_found_error",
						"message": "Embeddings API is not supported for this platform",
					},
				})
				return
			}
			h.OpenAIGateway.Embeddings(c)
		})
		gateway.POST("/images/generations", imagesHandler)
		gateway.POST("/images/edits", imagesHandler)
		gateway.POST("/images/batches", h.BatchImage.Submit)
		gateway.GET("/images/batches", h.BatchImage.List)
		gateway.GET("/images/batches/models", h.BatchImage.Models)
		gateway.GET("/images/batches/:id", h.BatchImage.Get)
		gateway.GET("/images/batches/:id/items", h.BatchImage.Items)
		gateway.GET("/images/batches/:id/items/:custom_id/content", h.BatchImage.ItemContent)
		gateway.GET("/images/batches/:id/download", h.BatchImage.Download)
		gateway.POST("/images/batches/:id/cancel", h.BatchImage.Cancel)
		gateway.DELETE("/images/batches/:id", h.BatchImage.DeleteRecord)
		gateway.DELETE("/images/batches/:id/outputs", h.BatchImage.DeleteOutputs)
		// OpenAI 兼容客户端可以通过 /videos 创建任务；Grok 媒体转发器会为 xAI
		// 转换为标准的 /videos/generations 路由。
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

		// xAI Voice API 仅供 Grok 平台使用，包括 HTTP TTS/STT 和实时 WebSocket。
		// 这些接口仅用于网关中继，不属于创作中心产品界面。
		voiceHandler := func(endpoint string) gin.HandlerFunc {
			return func(c *gin.Context) {
				if getGroupPlatform(c) != service.PlatformGrok {
					service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
					c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
					return
				}
				h.OpenAIGateway.GrokVoice(c, endpoint)
			}
		}
		gateway.POST("/tts", voiceHandler("tts"))
		gateway.POST("/stt", voiceHandler("stt"))
		gateway.POST("/custom-voices", voiceHandler("custom-voices"))
		customVoicePathHandler := func(c *gin.Context) {
			if getGroupPlatform(c) != service.PlatformGrok {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
				return
			}
			h.OpenAIGateway.GrokVoice(c, grokCustomVoiceEndpoint(c))
		}
		gateway.GET("/custom-voices", voiceHandler("custom-voices"))
		gateway.GET("/custom-voices/:voice_id/audio", customVoicePathHandler)
		gateway.GET("/custom-voices/:voice_id", customVoicePathHandler)
		gateway.PATCH("/custom-voices/:voice_id", customVoicePathHandler)
		gateway.DELETE("/custom-voices/:voice_id", customVoicePathHandler)
		gateway.GET("/realtime", func(c *gin.Context) {
			if getGroupPlatform(c) != service.PlatformGrok {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Realtime API is not supported for this platform"}})
				return
			}
			h.OpenAIGateway.GrokRealtime(c)
		})
		gateway.POST("/web_search", func(c *gin.Context) {
			if getGroupPlatform(c) != service.PlatformGrok {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Web Search API is not supported for this platform"}})
				return
			}
			h.Gateway.WebSearch(c)
		})
		gateway.POST("/x_search", func(c *gin.Context) {
			if getGroupPlatform(c) != service.PlatformGrok {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "X Search API is not supported for this platform"}})
				return
			}
			h.Gateway.XSearch(c)
		})
	}

	// Gemini 原生 API 兼容层（Gemini SDK/CLI 直连）
	gemini := r.Group("/v1beta")
	gemini.Use(bodyLimit)
	gemini.Use(clientRequestID)
	gemini.Use(opsErrorLogger)
	gemini.Use(endpointNorm)
	gemini.Use(middleware.APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, cfg))
	gemini.Use(requireGroupGoogle)
	{
		gemini.GET("/models", h.Gateway.GeminiV1BetaListModels)
		gemini.GET("/models/*model", h.Gateway.GeminiV1BetaGetModel)
		// Gin treats ":" as a param marker, but Gemini uses "{model}:{action}" in the same segment.
		gemini.POST("/models/*modelAction", requireGeminiGenerateContentProtocol, h.Gateway.GeminiV1BetaModels)
	}

	// OpenAI Responses API（不带v1前缀的别名）— auto-route based on group platform
	responsesHandler := func(c *gin.Context) {
		if isOpenAIResponsesCompatibleGatewayPlatform(c) {
			h.OpenAIGateway.Responses(c)
			return
		}
		if getGroupPlatform(c) == service.PlatformQoder {
			if c.Param("subpath") != "" {
				qoderResponsesSubpathUnsupported(c)
				return
			}
			h.QoderGateway.Responses(c)
			return
		}
		h.Gateway.Responses(c)
	}
	r.POST("/responses", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, responsesProtocolGate, responsesHandler)
	r.POST("/responses/*subpath", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, guardResponsesSubpath(withGroupClientProtocol(service.GroupClientProtocolOpenAIResponses, groupClientProtocolErrorOpenAI, responsesHandler)))
	r.POST("/alpha/search", textBodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, h.OpenAIGateway.AlphaSearch)
	r.GET("/responses", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, responsesWebSocketHandler)
	// Codex 客户端会访问不带 v1 前缀的模型列表，保持与 /v1/models 相同的本地模型语义。
	r.GET("/models", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, h.Gateway.Models)
	r.POST("/messages/count_tokens", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, countTokensProtocolGate, countTokensHandler)
	r.GET(
		"/backend-api/codex/:call_id",
		rejectRemovedCodexRoute,
		bodyLimit,
		clientRequestID,
		opsErrorLogger,
		endpointNorm,
		gin.HandlerFunc(apiKeyAuth),
		requireGroupAnthropic,
		h.OpenAIGateway.LiveSideband,
	)
	codexDirect := r.Group("/backend-api/codex")
	codexDirect.Use(bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic)
	{
		codexDirect.POST("/realtime/calls", h.OpenAIGateway.Live)
		codexDirect.POST("/responses", responsesProtocolGate, responsesHandler)
		codexDirect.POST("/responses/*subpath", guardResponsesSubpath(withGroupClientProtocol(service.GroupClientProtocolOpenAIResponses, groupClientProtocolErrorOpenAI, responsesHandler)))
		codexDirect.POST("/alpha/search", textBodyLimit, h.OpenAIGateway.AlphaSearch)
		codexDirect.GET("/responses", responsesWebSocketHandler)
	}
	// OpenAI Chat Completions API（不带v1前缀的别名）— auto-route based on group platform
	r.POST("/chat/completions", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, chatCompletionsProtocolGate, func(c *gin.Context) {
		if isOpenAIResponsesCompatibleGatewayPlatform(c) {
			h.OpenAIGateway.ChatCompletions(c)
			return
		}
		if getGroupPlatform(c) == service.PlatformQoder {
			h.QoderGateway.ChatCompletions(c)
			return
		}
		h.Gateway.ChatCompletions(c)
	})
	r.POST("/embeddings", textBodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, func(c *gin.Context) {
		if getGroupPlatform(c) != service.PlatformOpenAI {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "not_found_error",
					"message": "Embeddings API is not supported for this platform",
				},
			})
			return
		}
		h.OpenAIGateway.Embeddings(c)
	})
	r.POST("/images/generations", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, imagesHandler)
	r.POST("/images/edits", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, imagesHandler)
	// 与 /v1/videos 保持兼容，为 OpenAI 风格客户端提供无前缀的视频创建入口。
	r.POST("/videos", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoGenerationHandler)
	r.POST("/videos/generations", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoGenerationHandler)
	r.POST("/videos/edits", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoEditHandler)
	r.POST("/videos/extensions", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoExtensionHandler)
	r.GET("/videos/generations/:request_id/content", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoContentHandler)
	r.GET("/videos/edits/:request_id/content", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoContentHandler)
	r.GET("/videos/extensions/:request_id/content", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoContentHandler)
	r.GET("/videos/generations/:request_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoStatusHandler)
	r.GET("/videos/edits/:request_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoStatusHandler)
	r.GET("/videos/extensions/:request_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoStatusHandler)
	r.GET("/videos/:request_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoStatusHandler)
	r.GET("/videos/:request_id/content", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoContentHandler)

	rootVoiceHandler := func(endpoint string) gin.HandlerFunc {
		return func(c *gin.Context) {
			if getGroupPlatform(c) != service.PlatformGrok {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
				return
			}
			h.OpenAIGateway.GrokVoice(c, endpoint)
		}
	}
	r.POST("/tts", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, rootVoiceHandler("tts"))
	r.POST("/stt", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, rootVoiceHandler("stt"))
	r.POST("/custom-voices", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, rootVoiceHandler("custom-voices"))
	rootCustomVoicePathHandler := func(c *gin.Context) {
		if getGroupPlatform(c) != service.PlatformGrok {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
			return
		}
		h.OpenAIGateway.GrokVoice(c, grokCustomVoiceEndpoint(c))
	}
	r.GET("/custom-voices", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, rootVoiceHandler("custom-voices"))
	r.GET("/custom-voices/:voice_id/audio", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, rootCustomVoicePathHandler)
	r.GET("/custom-voices/:voice_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, rootCustomVoicePathHandler)
	r.PATCH("/custom-voices/:voice_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, rootCustomVoicePathHandler)
	r.DELETE("/custom-voices/:voice_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, rootCustomVoicePathHandler)
	r.GET("/realtime", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, func(c *gin.Context) {
		if getGroupPlatform(c) != service.PlatformGrok {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Realtime API is not supported for this platform"}})
			return
		}
		h.OpenAIGateway.GrokRealtime(c)
	})
	r.POST("/web_search", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, func(c *gin.Context) {
		if getGroupPlatform(c) != service.PlatformGrok {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Web Search API is not supported for this platform"}})
			return
		}
		h.Gateway.WebSearch(c)
	})
	r.POST("/x_search", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, func(c *gin.Context) {
		if getGroupPlatform(c) != service.PlatformGrok {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "X Search API is not supported for this platform"}})
			return
		}
		h.Gateway.XSearch(c)
	})

	// Antigravity 模型列表
	r.GET("/antigravity/models", middleware.ForcePlatform(service.PlatformAntigravity), gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, h.Gateway.AntigravityModels)

	// Antigravity 专用路由（仅使用 antigravity 账户，不混合调度）
	antigravityV1 := r.Group("/antigravity/v1")
	antigravityV1.Use(bodyLimit)
	antigravityV1.Use(clientRequestID)
	antigravityV1.Use(opsErrorLogger)
	antigravityV1.Use(endpointNorm)
	antigravityV1.Use(middleware.ForcePlatform(service.PlatformAntigravity))
	antigravityV1.Use(gin.HandlerFunc(apiKeyAuth))
	antigravityV1.Use(requireGroupAnthropic)
	{
		antigravityV1.POST("/messages", messagesProtocolGate, h.Gateway.Messages)
		antigravityV1.POST("/messages/count_tokens", countTokensProtocolGate, countTokensHandler)
		antigravityV1.GET("/models", h.Gateway.AntigravityModels)
		antigravityV1.GET("/usage", h.Gateway.Usage)
	}

	antigravityV1Beta := r.Group("/antigravity/v1beta")
	antigravityV1Beta.Use(bodyLimit)
	antigravityV1Beta.Use(clientRequestID)
	antigravityV1Beta.Use(opsErrorLogger)
	antigravityV1Beta.Use(endpointNorm)
	antigravityV1Beta.Use(middleware.ForcePlatform(service.PlatformAntigravity))
	antigravityV1Beta.Use(middleware.APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, cfg))
	antigravityV1Beta.Use(requireGroupGoogle)
	{
		antigravityV1Beta.GET("/models", h.Gateway.GeminiV1BetaListModels)
		antigravityV1Beta.GET("/models/*model", h.Gateway.GeminiV1BetaGetModel)
		antigravityV1Beta.POST("/models/*modelAction", requireGeminiGenerateContentProtocol, h.Gateway.GeminiV1BetaModels)
	}

}

// getGroupPlatform extracts the group platform from the API Key stored in context.
func getGroupPlatform(c *gin.Context) string {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		return ""
	}
	return apiKey.Group.Platform
}

// grokCustomVoiceEndpoint 根据已匹配路由生成上游自定义 Voice 路径。
func grokCustomVoiceEndpoint(c *gin.Context) string {
	endpoint := "custom-voices/" + c.Param("voice_id")
	if strings.HasSuffix(c.FullPath(), "/:voice_id/audio") {
		endpoint += "/audio"
	}
	return endpoint
}
