package httpapi

import (
	"net/http"
	"slices"
	"strings"

	wireprotocol "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
)

// RouteAccess 是每次门禁从当前认证请求读取的最小投影，不持有完整旧实体。
type RouteAccess struct {
	HasGroup         bool
	Platform         string
	Composite        bool
	AllowedProtocols []wireprotocol.ProtocolID
}

// RouteMiddleware 在 app 一次装配，路由不构造认证、配置或观测服务。
type RouteMiddleware struct {
	APIKeyAuth, GoogleAPIKeyAuth, BodyLimit, TextBodyLimit, ClientRequestID, OpsErrorLogger, EndpointNormalization gin.HandlerFunc
	RequireGroupAnthropic, RequireGroupGoogle, ForceAntigravity                                                    gin.HandlerFunc
	Access                                                                                                         func(*gin.Context) RouteAccess
	ForcedPlatform                                                                                                 func(*gin.Context) (string, bool)
	InstallClientProtocol                                                                                          func(*gin.Context, wireprotocol.ProtocolID)
	ObserveBusinessLimit                                                                                           func(*gin.Context, string)
}

const (
	RouteLimitLocalPolicyDenied = "local_policy_denied"
	RouteLimitLocalFeatureGate  = "local_feature_gate"
)

// RouteGuards 共享现有路由表和当次认证投影，不读取请求报文。
type RouteGuards struct{ options RouteMiddleware }

func NewRouteGuards(options RouteMiddleware) *RouteGuards  { return &RouteGuards{options: options} }
func (g *RouteGuards) GroupPlatform(c *gin.Context) string { return g.options.Access(c).Platform }

type GroupClientProtocolErrorFormat string

const (
	GroupClientProtocolErrorAnthropic GroupClientProtocolErrorFormat = "anthropic"
	GroupClientProtocolErrorOpenAI    GroupClientProtocolErrorFormat = "openai"
	GroupClientProtocolErrorGoogle    GroupClientProtocolErrorFormat = "google"
)

// requireGroupClientProtocol 在进入业务处理器前执行分组协议准入检查。
func (g *RouteGuards) RequireGroupClientProtocol(protocol wireprotocol.ProtocolID, format GroupClientProtocolErrorFormat) gin.HandlerFunc {
	return func(c *gin.Context) {
		if g.EnforceGroupClientProtocol(c, protocol, format) {
			c.Next()
		}
	}
}

// withGroupClientProtocol 把协议门禁包在已完成路径校验的终端处理器外层。
func (g *RouteGuards) WithGroupClientProtocol(protocol wireprotocol.ProtocolID, format GroupClientProtocolErrorFormat, next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if g.EnforceGroupClientProtocol(c, protocol, format) {
			next(c)
		}
	}
}

// enforceGroupClientProtocol 执行检查并在拒绝时写入协议原生错误。
func (g *RouteGuards) EnforceGroupClientProtocol(c *gin.Context, protocol wireprotocol.ProtocolID, format GroupClientProtocolErrorFormat) bool {
	access := g.options.Access(c)
	if protocol == wireprotocol.ProtocolOpenAIResponses && RouteProtocol(c.Request.Method, c.Request.URL.Path) == wireprotocol.ProtocolResponsesCompact && (access.Platform == capability.PlatformOpenAI || access.Platform == capability.PlatformGrok) {
		protocol = wireprotocol.ProtocolResponsesCompact
	}
	g.options.InstallClientProtocol(c, protocol)
	group := routing.Group{AllowedProtocols: access.AllowedProtocols}
	if !access.HasGroup || group.AllowsClientProtocol(protocol) {
		return true
	}
	g.options.ObserveBusinessLimit(c, RouteLimitLocalPolicyDenied)

	message := groupClientProtocolDeniedMessage(protocol)
	switch format {
	case GroupClientProtocolErrorGoogle:
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"code":    http.StatusForbidden,
				"message": message,
				"status":  "PERMISSION_DENIED",
			},
		})
	case GroupClientProtocolErrorOpenAI:
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

func groupClientProtocolDeniedMessage(protocol wireprotocol.ProtocolID) string {
	switch protocol {
	case wireprotocol.ProtocolAnthropicMessages:
		return "This group does not allow Anthropic Messages requests"
	case wireprotocol.ProtocolOpenAIResponses:
		return "This group does not allow OpenAI Responses requests"
	case wireprotocol.ProtocolOpenAIChatCompletions:
		return "This group does not allow OpenAI Chat Completions requests"
	case wireprotocol.ProtocolGeminiGenerateContent:
		return "This group does not allow Gemini GenerateContent requests"
	default:
		return "This group does not allow the requested client protocol"
	}
}

// requireGeminiGenerateContentProtocol 只门禁 Gemini 的三个文本生成 POST 动作。
func (g *RouteGuards) RequireGeminiGenerateContentProtocol(c *gin.Context) {
	rest := strings.TrimSpace(strings.TrimPrefix(c.Param("modelAction"), "/"))
	separator := strings.LastIndexAny(rest, ":/")
	if separator <= 0 || separator == len(rest)-1 {
		c.Next()
		return
	}
	switch rest[separator+1:] {
	case "generateContent", "streamGenerateContent", "countTokens":
		g.RequireGroupClientProtocol(wireprotocol.ProtocolGeminiGenerateContent, GroupClientProtocolErrorGoogle)(c)
	default:
		c.Next()
	}
}

// ExtendedRouteProtocol 为非文本入口及别名统一命名；已有任务操作不受新建开关影响。
func ExtendedRouteProtocol(method, path string) wireprotocol.ProtocolID {
	protocol := RouteProtocol(method, path)
	// Compact 由 Responses 通配路由完成路径校验后检查，保留无效路径的错误语义。
	if protocol == wireprotocol.ProtocolResponsesCompact {
		return ""
	}
	return protocol
}

// RouteProtocol 仅移除完整的别名前缀，避免相似路径被误归为已知入口。
func RouteProtocol(method, path string) wireprotocol.ProtocolID {
	for _, prefix := range []string{"/backend-api/codex", "/v1"} {
		if strings.HasPrefix(path, prefix+"/") {
			path = strings.TrimPrefix(path, prefix)
		}
	}
	protocol, _ := ProtocolForRoute(method, path)
	return protocol
}

// RequireExtendedProtocol 只拦截分组平台支持的入口，保持辅助操作的既有拒绝顺序。
func (g *RouteGuards) RequireExtendedProtocol(c *gin.Context) {
	protocol := ExtendedRouteProtocol(c.Request.Method, c.Request.URL.Path)
	access := g.options.Access(c)
	if access.HasGroup && !slices.Contains(capability.SupportedGroupClientProtocols(access.Platform), protocol) {
		c.Next()
		return
	}
	if protocol == "" || g.EnforceGroupClientProtocol(c, protocol, GroupClientProtocolErrorOpenAI) {
		c.Next()
	}
}
