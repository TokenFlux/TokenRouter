package account

import "testing"

func TestResolveUpstreamTextProtocol(t *testing.T) {
	modes := []TextRouteMode{
		TextRouteModePreserveClientProtocol,
		TextRouteModeForceResponses,
		TextRouteModeForceChatCompletions,
	}
	statuses := []string{"supported", "unsupported", "unknown"}
	preferredProtocols := []TextProtocol{TextProtocolChatCompletions, TextProtocolResponses}

	for _, mode := range modes {
		for _, status := range statuses {
			for _, preferred := range preferredProtocols {
				extra := map[string]any{
					ExtraKeyTextRouteMode:           string(mode),
					"openai_responses_probe_status": string(status),
				}
				want := preferred
				switch mode {
				case TextRouteModeForceResponses:
					want = TextProtocolResponses
				case TextRouteModeForceChatCompletions:
					want = TextProtocolChatCompletions

				}

				if got := ResolveUpstreamTextProtocol(extra, preferred); got != want {
					t.Fatalf("mode=%q status=%q preferred=%q: got %q, want %q", mode, status, preferred, got, want)
				}
			}
		}
	}
}

func TestResolveUpstreamTextProtocolDefaults(t *testing.T) {
	if got := ResolveUpstreamTextProtocol(nil, TextProtocolChatCompletions); got != TextProtocolChatCompletions {
		t.Fatalf("Chat 首选协议默认得到 %q", got)
	}
	if got := ResolveUpstreamTextProtocol(map[string]any{}, TextProtocolResponses); got != TextProtocolResponses {
		t.Fatalf("Responses 首选协议默认得到 %q", got)
	}
	if got := ResolveUpstreamTextProtocol(map[string]any{
		ExtraKeyTextRouteMode:           "invalid",
		"openai_responses_probe_status": "invalid",
	}, TextProtocolResponses); got != TextProtocolResponses {
		t.Fatalf("非法配置默认得到 %q", got)
	}
}

func TestNormalizeTextProtocolConfiguration(t *testing.T) {
	if got := NormalizeTextRouteMode("invalid"); got != TextRouteModePreserveClientProtocol {
		t.Fatalf("非法路由模式得到 %q", got)
	}
}

func TestResolveResponsesContinuationSupported(t *testing.T) {
	if ResolveResponsesContinuationSupported(nil) {
		t.Fatal("缺失配置必须按不支持处理")
	}
	if ResolveResponsesContinuationSupported(map[string]any{
		ExtraKeyResponsesContinuationSupported: "true",
	}) {
		t.Fatal("非布尔配置必须按不支持处理")
	}
	if !ResolveResponsesContinuationSupported(map[string]any{
		ExtraKeyResponsesContinuationSupported: true,
	}) {
		t.Fatal("显式 true 必须启用 continuation")
	}
	if ResolveResponsesContinuationSupported(map[string]any{
		ExtraKeyResponsesContinuationSupported: false,
	}) {
		t.Fatal("显式 false 必须关闭 continuation")
	}
}
