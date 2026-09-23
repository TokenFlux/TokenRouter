package httpapi

import (
	"net/http"
	"strings"

	wireprotocol "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
)

// RouteEndpoints 只汇总网关所属的原生 HTTP 处理器与外部终端投影。
type RouteEndpoints struct {
	CountTokens            *CountTokensHandler
	QoderCompatible        *QoderCompatibleHandler
	CompatibleText         *CompatibleTextHandler
	GeminiNative           *GeminiNativeHandler
	OpenAIText             *OpenAITextHandler
	OpenAITokens           *OpenAITokensHandler
	ResponsesWS            *ResponsesWSHandler
	Models                 *ModelsHandler
	Messages               *MessagesHandler
	Media                  *MediaHandler
	Auxiliary              *AuxiliaryHandler
	Live                   *LiveHandler
	Search                 *SearchHandler
	PublicUsage, QoderChat gin.HandlerFunc
}

// RegisterGatewayRoutes 保持每条原生/别名路径及中间件顺序；不增加请求尝试循环。
// @project-doc docs/architecture/gateway_request_lifecycle.md#gateway_pipeline
func RegisterGatewayRoutes(r *gin.Engine, endpoints RouteEndpoints, options RouteMiddleware, registerBatchImages func(*gin.RouterGroup)) {
	guards := NewRouteGuards(options)
	getGroupPlatform := guards.GroupPlatform
	requireGroupClientProtocol := guards.RequireGroupClientProtocol
	withGroupClientProtocol := guards.WithGroupClientProtocol
	requireExtendedProtocol := guards.RequireExtendedProtocol
	requireGeminiGenerateContentProtocol := guards.RequireGeminiGenerateContentProtocol
	countTokensHTTP, qoderCompatibleHTTP, compatibleTextHTTP := endpoints.CountTokens, endpoints.QoderCompatible, endpoints.CompatibleText
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

	// 未分组 Key 拦截中间件（按协议格式区分错误响应）
	requireGroupAnthropic := options.RequireGroupAnthropic
	requireGroupGoogle := options.RequireGroupGoogle

	isOpenAIResponsesCompatibleGatewayPlatform := func(c *gin.Context) bool {
		switch getGroupPlatform(c) {
		case capability.PlatformOpenAI, capability.PlatformGrok,
			capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformDeepseek:
			// 国产 OpenAI 兼容供应商（kimi/zhipu/deepseek）与 openai/grok 一样经 OpenAI 网关转发。
			return true
		default:
			return false
		}
	}
	responsesInputTokensHandler := func(c *gin.Context) {
		if isOpenAIResponsesCompatibleGatewayPlatform(c) {
			endpoints.OpenAITokens.ResponsesInputTokens(c)
			return
		}
		options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Responses input token counting is not supported for this platform",
			},
		})
	}
	// count_tokens 需要识别强制平台别名，避免把 Antigravity 当成分组原平台处理。
	countTokensPlatform := func(c *gin.Context) string {
		if platform, ok := options.ForcedPlatform(c); ok && strings.TrimSpace(platform) != "" {
			return platform
		}
		return getGroupPlatform(c)
	}
	// 只有实际支持 count_tokens 的平台才受 Messages 协议门禁控制。
	countTokensProtocolGate := func(c *gin.Context) {
		switch countTokensPlatform(c) {
		case capability.PlatformAntigravity, capability.PlatformQoder:
			c.Next()
		default:
			messagesProtocolGate(c)
		}
	}
	countTokensHandler := func(c *gin.Context) {
		switch countTokensPlatform(c) {
		case capability.PlatformAntigravity, capability.PlatformQoder:
			c.JSON(http.StatusNotFound, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "not_found_error",
					"message": "count_tokens endpoint is not supported for this platform",
				},
			})
		case capability.PlatformOpenAI, capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformDeepseek:
			endpoints.OpenAITokens.CountTokens(c)
		case capability.PlatformGrok:
			endpoints.OpenAITokens.GrokCountTokens(c)
		default:
			countTokensHTTP.CountTokens(c)
		}
	}
	imagesHandler := func(c *gin.Context) {
		switch getGroupPlatform(c) {
		case capability.PlatformOpenAI:
			mediaHTTP.Images(c)
		case capability.PlatformGrok:
			mediaHTTP.GrokImages(c)
		default:
			options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "not_found_error",
					"message": "Images API is not supported for this platform",
				},
			})
		}
	}
	videoGenerationHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == capability.PlatformGrok {
			mediaHTTP.GrokVideoGeneration(c)
			return
		}
		options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	videoStatusHandler := func(c *gin.Context) {
		access := options.Access(c)
		if getGroupPlatform(c) == capability.PlatformGrok || access.Composite {
			mediaHTTP.GrokVideoStatus(c)
			return
		}
		options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	videoContentHandler := func(c *gin.Context) {
		access := options.Access(c)
		if getGroupPlatform(c) == capability.PlatformGrok || access.Composite {
			mediaHTTP.GrokVideoContent(c)
			return
		}
		options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	videoEditHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == capability.PlatformGrok {
			mediaHTTP.GrokVideoEdit(c)
			return
		}
		options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	videoExtensionHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == capability.PlatformGrok {
			mediaHTTP.GrokVideoExtension(c)
			return
		}
		options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	qoderResponsesSubpathUnsupported := func(c *gin.Context) {
		options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Qoder Responses subpaths are not supported",
			},
		})
	}
	responsesWebSocketUnsupported := func(c *gin.Context) {
		options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
		message := "Responses WebSocket is not supported for this upstream platform"
		if getGroupPlatform(c) == capability.PlatformQoder {
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
		case capability.PlatformOpenAI, capability.PlatformGrok:
			responsesWSHTTP.ResponsesWebSocket(c)
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
	// IsForwardableOpenAIResponsesRequestPath 及 upstream_path_guard.go。
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

	// API网关（Claude API兼容）
	gateway := r.Group("/v1")
	gateway.Use(bodyLimit)
	gateway.Use(clientRequestID)
	gateway.Use(opsErrorLogger)
	gateway.Use(endpointNorm)
	gateway.Use(options.APIKeyAuth)
	gateway.Use(requireGroupAnthropic, requireExtendedProtocol)
	{
		// /v1/messages: auto-route based on group platform
		gateway.POST("/messages", messagesProtocolGate, func(c *gin.Context) {
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				openAITextHTTP.Messages(c)
				return
			}
			if getGroupPlatform(c) == capability.PlatformQoder {
				qoderCompatibleHTTP.Messages(c)
				return
			}
			messagesHTTP.Messages(c)
		})
		// /v1/messages/count_tokens：OpenAI 桥接上游，Grok 本地估算，其余 Anthropic
		// 兼容平台保留原处理路径。
		gateway.POST("/messages/count_tokens", countTokensProtocolGate, countTokensHandler)
		gateway.GET("/models", modelsHTTP.Models)
		gateway.GET("/usage", endpoints.PublicUsage)
		gateway.POST("/live", liveHTTP.Live)
		gateway.GET("/live/:call_id", liveHTTP.LiveSideband)
		// OpenAI Responses API: auto-route based on group platform
		gateway.POST("/responses", responsesProtocolGate, func(c *gin.Context) {
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				openAITextHTTP.Responses(c)
				return
			}
			if getGroupPlatform(c) == capability.PlatformQoder {
				qoderCompatibleHTTP.Responses(c)
				return
			}
			compatibleTextHTTP.Responses(c)
		})
		gateway.POST("/responses/*subpath", guardResponsesSubpath(withGroupClientProtocol(wireprotocol.ProtocolOpenAIResponses, GroupClientProtocolErrorOpenAI, func(c *gin.Context) {
			if IsOpenAIResponsesInputTokensRequestPath(c) {
				responsesInputTokensHandler(c)
				return
			}
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				openAITextHTTP.Responses(c)
				return
			}
			if getGroupPlatform(c) == capability.PlatformQoder {
				qoderResponsesSubpathUnsupported(c)
				return
			}
			compatibleTextHTTP.Responses(c)
		})))
		gateway.POST("/alpha/search", textBodyLimit, auxiliaryHTTP.AlphaSearch)
		gateway.GET("/responses", responsesWebSocketHandler)
		// OpenAI Chat Completions API: auto-route based on group platform
		gateway.POST("/chat/completions", chatCompletionsProtocolGate, func(c *gin.Context) {
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				openAITextHTTP.ChatCompletions(c)
				return
			}
			if getGroupPlatform(c) == capability.PlatformQoder {
				endpoints.QoderChat(c)
				return
			}
			compatibleTextHTTP.ChatCompletions(c)
		})
		gateway.POST("/embeddings", textBodyLimit, func(c *gin.Context) {
			if getGroupPlatform(c) != capability.PlatformOpenAI {
				options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{
					"error": gin.H{
						"type":    "not_found_error",
						"message": "Embeddings API is not supported for this platform",
					},
				})
				return
			}
			auxiliaryHTTP.Embeddings(c)
		})
		gateway.POST("/images/generations", imagesHandler)
		gateway.POST("/images/edits", imagesHandler)
		registerBatchImages(gateway)

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
				if getGroupPlatform(c) != capability.PlatformGrok {
					options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
					c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
					return
				}
				auxiliaryHTTP.GrokVoice(c, endpoint)
			}
		}
		gateway.POST("/tts", voiceHandler("tts"))
		gateway.POST("/stt", voiceHandler("stt"))
		gateway.POST("/custom-voices", voiceHandler("custom-voices"))
		customVoicePathHandler := func(c *gin.Context) {
			if getGroupPlatform(c) != capability.PlatformGrok {
				options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
				return
			}
			auxiliaryHTTP.GrokVoice(c, grokCustomVoiceEndpoint(c))
		}
		gateway.GET("/custom-voices", voiceHandler("custom-voices"))
		gateway.GET("/custom-voices/:voice_id/audio", customVoicePathHandler)
		gateway.GET("/custom-voices/:voice_id", customVoicePathHandler)
		gateway.PATCH("/custom-voices/:voice_id", customVoicePathHandler)
		gateway.DELETE("/custom-voices/:voice_id", customVoicePathHandler)
		gateway.GET("/realtime", func(c *gin.Context) {
			if getGroupPlatform(c) != capability.PlatformGrok {
				options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Realtime API is not supported for this platform"}})
				return
			}
			auxiliaryHTTP.GrokRealtime(c)
		})
		gateway.POST("/web_search", func(c *gin.Context) {
			if getGroupPlatform(c) != capability.PlatformGrok {
				options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Web Search API is not supported for this platform"}})
				return
			}
			searchHTTP.WebSearch(c)
		})
		gateway.POST("/x_search", func(c *gin.Context) {
			if getGroupPlatform(c) != capability.PlatformGrok {
				options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "X Search API is not supported for this platform"}})
				return
			}
			searchHTTP.XSearch(c)
		})
	}

	// Gemini 原生 API 兼容层（Gemini SDK/CLI 直连）
	gemini := r.Group("/v1beta")
	gemini.Use(bodyLimit)
	gemini.Use(clientRequestID)
	gemini.Use(opsErrorLogger)
	gemini.Use(endpointNorm)
	gemini.Use(options.GoogleAPIKeyAuth)
	gemini.Use(requireGroupGoogle)
	{
		gemini.GET("/models", modelsHTTP.GeminiV1BetaListModels)
		gemini.GET("/models/*model", modelsHTTP.GeminiV1BetaGetModel)
		// Gin treats ":" as a param marker, but Gemini uses "{model}:{action}" in the same segment.
		gemini.POST("/models/*modelAction", requireGeminiGenerateContentProtocol, geminiNativeHTTP.GeminiV1BetaModels)
	}

	// OpenAI Responses API（不带v1前缀的别名）— auto-route based on group platform
	responsesHandler := func(c *gin.Context) {
		if IsOpenAIResponsesInputTokensRequestPath(c) {
			responsesInputTokensHandler(c)
			return
		}
		if isOpenAIResponsesCompatibleGatewayPlatform(c) {
			openAITextHTTP.Responses(c)
			return
		}
		if getGroupPlatform(c) == capability.PlatformQoder {
			if c.Param("subpath") != "" {
				qoderResponsesSubpathUnsupported(c)
				return
			}
			qoderCompatibleHTTP.Responses(c)
			return
		}
		compatibleTextHTTP.Responses(c)
	}
	r.POST("/responses", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, responsesProtocolGate, responsesHandler)
	r.POST("/responses/*subpath", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, guardResponsesSubpath(withGroupClientProtocol(wireprotocol.ProtocolOpenAIResponses, GroupClientProtocolErrorOpenAI, responsesHandler)))
	r.POST("/alpha/search", textBodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, auxiliaryHTTP.AlphaSearch)
	r.GET("/responses", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, responsesWebSocketHandler)
	// Codex 客户端会访问不带 v1 前缀的模型列表，保持与 /v1/models 相同的本地模型语义。
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
	codexDirect.Use(bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol)
	{
		codexDirect.POST("/realtime/calls", liveHTTP.Live)
		codexDirect.POST("/responses", responsesProtocolGate, responsesHandler)
		codexDirect.POST("/responses/*subpath", guardResponsesSubpath(withGroupClientProtocol(wireprotocol.ProtocolOpenAIResponses, GroupClientProtocolErrorOpenAI, responsesHandler)))
		codexDirect.POST("/alpha/search", textBodyLimit, auxiliaryHTTP.AlphaSearch)
		codexDirect.GET("/responses", responsesWebSocketHandler)
	}
	// OpenAI Chat Completions API（不带v1前缀的别名）— auto-route based on group platform
	r.POST("/chat/completions", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, chatCompletionsProtocolGate, func(c *gin.Context) {
		if isOpenAIResponsesCompatibleGatewayPlatform(c) {
			openAITextHTTP.ChatCompletions(c)
			return
		}
		if getGroupPlatform(c) == capability.PlatformQoder {
			endpoints.QoderChat(c)
			return
		}
		compatibleTextHTTP.ChatCompletions(c)
	})
	r.POST("/embeddings", textBodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, func(c *gin.Context) {
		if getGroupPlatform(c) != capability.PlatformOpenAI {
			options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "not_found_error",
					"message": "Embeddings API is not supported for this platform",
				},
			})
			return
		}
		auxiliaryHTTP.Embeddings(c)
	})
	r.POST("/images/generations", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, imagesHandler)
	r.POST("/images/edits", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, imagesHandler)
	// 与 /v1/videos 保持兼容，为 OpenAI 风格客户端提供无前缀的视频创建入口。
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
			if getGroupPlatform(c) != capability.PlatformGrok {
				options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
				return
			}
			auxiliaryHTTP.GrokVoice(c, endpoint)
		}
	}
	r.POST("/tts", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootVoiceHandler("tts"))
	r.POST("/stt", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootVoiceHandler("stt"))
	r.POST("/custom-voices", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootVoiceHandler("custom-voices"))
	rootCustomVoicePathHandler := func(c *gin.Context) {
		if getGroupPlatform(c) != capability.PlatformGrok {
			options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
			return
		}
		auxiliaryHTTP.GrokVoice(c, grokCustomVoiceEndpoint(c))
	}
	r.GET("/custom-voices", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootVoiceHandler("custom-voices"))
	r.GET("/custom-voices/:voice_id/audio", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootCustomVoicePathHandler)
	r.GET("/custom-voices/:voice_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootCustomVoicePathHandler)
	r.PATCH("/custom-voices/:voice_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootCustomVoicePathHandler)
	r.DELETE("/custom-voices/:voice_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, rootCustomVoicePathHandler)
	r.GET("/realtime", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, func(c *gin.Context) {
		if getGroupPlatform(c) != capability.PlatformGrok {
			options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Realtime API is not supported for this platform"}})
			return
		}
		auxiliaryHTTP.GrokRealtime(c)
	})
	r.POST("/web_search", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, func(c *gin.Context) {
		if getGroupPlatform(c) != capability.PlatformGrok {
			options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Web Search API is not supported for this platform"}})
			return
		}
		searchHTTP.WebSearch(c)
	})
	r.POST("/x_search", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, func(c *gin.Context) {
		if getGroupPlatform(c) != capability.PlatformGrok {
			options.ObserveBusinessLimit(c, RouteLimitLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "X Search API is not supported for this platform"}})
			return
		}
		searchHTTP.XSearch(c)
	})

	// Antigravity 模型列表
	r.GET("/antigravity/models", options.ForceAntigravity, options.APIKeyAuth, requireGroupAnthropic, requireExtendedProtocol, modelsHTTP.AntigravityModels)

	// Antigravity 专用路由（仅使用 antigravity 账户，不混合调度）
	antigravityV1 := r.Group("/antigravity/v1")
	antigravityV1.Use(bodyLimit)
	antigravityV1.Use(clientRequestID)
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
	antigravityV1Beta.Use(clientRequestID)
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

func grokCustomVoiceEndpoint(c *gin.Context) string {
	endpoint := "custom-voices/" + c.Param("voice_id")
	if strings.HasSuffix(c.FullPath(), "/:voice_id/audio") {
		endpoint += "/audio"
	}
	return endpoint
}
