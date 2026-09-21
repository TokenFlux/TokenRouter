package egress

// OpenAIUpstreamTransport 表示 OpenAI 上游传输协议。
type OpenAIUpstreamTransport string

const (
	OpenAIUpstreamTransportAny                  OpenAIUpstreamTransport = ""
	OpenAIUpstreamTransportHTTPSSE              OpenAIUpstreamTransport = "http_sse"
	OpenAIUpstreamTransportResponsesWebsocket   OpenAIUpstreamTransport = "responses_websockets"
	OpenAIUpstreamTransportResponsesWebsocketV2 OpenAIUpstreamTransport = "responses_websockets_v2"
	// OpenAIUpstreamTransportResponsesWebsocketV2Ingress 仅用于 WS ingress 选号，允许 ctx_pool/passthrough/http_bridge。
	OpenAIUpstreamTransportResponsesWebsocketV2Ingress OpenAIUpstreamTransport = "responses_websockets_v2_ingress"
)

// OpenAIWSProtocolDecision 表示协议决策结果。
type OpenAIWSProtocolDecision struct {
	Transport OpenAIUpstreamTransport
	Reason    string
}

// OpenAIWSAccount 不含凭据，只提供协议资格与选定模式。
type OpenAIWSAccount struct {
	Present, OpenAI, ForceHTTP, OAuthLike, APIKey, WSEnabled bool
	Mode                                                     string
	Concurrency                                              int
}

// OpenAIWSOptions 只保存协议选择所需开关；nil 仍表示配置缺失。
type OpenAIWSOptions struct {
	Enabled, ForceHTTP, OAuthEnabled, APIKeyEnabled                 bool
	ModeRouterV2Enabled, ResponsesWebsockets, ResponsesWebsocketsV2 bool
}

// ResolveOpenAIWSTransport 按原优先级选择出站协议，只消费认证与运行参数投影。
func ResolveOpenAIWSTransport(account OpenAIWSAccount, wsCfg *OpenAIWSOptions) OpenAIWSProtocolDecision {
	if !account.Present {
		return OpenAIWSHTTPDecision("account_missing")
	}
	if !account.OpenAI {
		return OpenAIWSHTTPDecision("platform_not_openai")
	}
	if account.ForceHTTP {
		return OpenAIWSHTTPDecision("account_force_http")
	}
	if wsCfg == nil {
		return OpenAIWSHTTPDecision("config_missing")
	}

	if wsCfg.ForceHTTP {
		return OpenAIWSHTTPDecision("global_force_http")
	}
	if !wsCfg.Enabled {
		return OpenAIWSHTTPDecision("global_disabled")
	}
	if account.OAuthLike {
		if !wsCfg.OAuthEnabled {
			return OpenAIWSHTTPDecision("oauth_disabled")
		}
	} else if account.APIKey {
		if !wsCfg.APIKeyEnabled {
			return OpenAIWSHTTPDecision("apikey_disabled")
		}
	} else {
		return OpenAIWSHTTPDecision("unknown_auth_type")
	}
	if wsCfg.ModeRouterV2Enabled {
		mode := account.Mode
		switch mode {
		case "off":
			return OpenAIWSHTTPDecision("account_mode_off")
		case "ctx_pool", "passthrough":
			// continue
		case "http_bridge":
			return OpenAIWSHTTPDecision("ws_v2_mode_http_bridge")
		case "shared", "dedicated":
			// 历史值兼容：按 ctx_pool 处理。
			mode = "ctx_pool"
		default:
			return OpenAIWSHTTPDecision("account_mode_off")
		}
		if account.Concurrency <= 0 {
			return OpenAIWSHTTPDecision("account_concurrency_invalid")
		}
		if wsCfg.ResponsesWebsocketsV2 {
			return OpenAIWSProtocolDecision{
				Transport: OpenAIUpstreamTransportResponsesWebsocketV2,
				Reason:    "ws_v2_mode_" + mode,
			}
		}
		if wsCfg.ResponsesWebsockets {
			return OpenAIWSProtocolDecision{
				Transport: OpenAIUpstreamTransportResponsesWebsocket,
				Reason:    "ws_v1_mode_" + mode,
			}
		}
		return OpenAIWSHTTPDecision("feature_disabled")
	}
	if !account.WSEnabled {
		return OpenAIWSHTTPDecision("account_disabled")
	}
	if wsCfg.ResponsesWebsocketsV2 {
		return OpenAIWSProtocolDecision{
			Transport: OpenAIUpstreamTransportResponsesWebsocketV2,
			Reason:    "ws_v2_enabled",
		}
	}
	if wsCfg.ResponsesWebsockets {
		return OpenAIWSProtocolDecision{
			Transport: OpenAIUpstreamTransportResponsesWebsocket,
			Reason:    "ws_v1_enabled",
		}
	}
	return OpenAIWSHTTPDecision("feature_disabled")
}

// OpenAIWSHTTPDecision 保留回退原因与 HTTP/SSE 传输值。
func OpenAIWSHTTPDecision(reason string) OpenAIWSProtocolDecision {
	return OpenAIWSProtocolDecision{
		Transport: OpenAIUpstreamTransportHTTPSSE,
		Reason:    reason,
	}
}
