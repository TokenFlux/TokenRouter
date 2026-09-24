package provider_test

import (
	"strconv"
	"testing"
	time "time"

	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestShouldFlattenOpenAIResponsesNamespaces(t *testing.T) {
	oauth := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	apiKey := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	grokOAuth := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	flattenOAuth := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type:  capability.AccountTypeOAuth,
		Extra: map[string]any{"openai_responses_flatten_namespaces": true}},
	}
	flattenAPIKey := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type:  capability.AccountTypeAPIKey,
		Extra: map[string]any{"openai_responses_flatten_namespaces": true}},
	}

	tests := []struct {
		name               string
		account            *gatewayprovider.ExecutionAccount
		transport          egress.OpenAIUpstreamTransport
		passthroughEnabled bool
		compactPath        bool
		want               bool
	}{
		{name: "oauth_http_default_preserves", account: oauth, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: false},
		{name: "oauth_http_passthrough_default_preserves", account: oauth, transport: egress.OpenAIUpstreamTransportHTTPSSE, passthroughEnabled: true, want: false},
		{name: "oauth_wsv2_default_preserves", account: oauth, transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2, want: false},
		// compact 端点的 schema 更窄，保持既有摊平行为。
		{name: "oauth_compact_flattens", account: oauth, transport: egress.OpenAIUpstreamTransportHTTPSSE, compactPath: true, want: true},
		{name: "oauth_compact_wsv2_preserves", account: oauth, transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2, compactPath: true, want: false},
		{name: "apikey_compact", account: apiKey, transport: egress.OpenAIUpstreamTransportHTTPSSE, compactPath: true, want: false},
		{name: "oauth_flatten_enabled_http", account: flattenOAuth, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: true},
		{name: "oauth_flatten_enabled_http_passthrough", account: flattenOAuth, transport: egress.OpenAIUpstreamTransportHTTPSSE, passthroughEnabled: true, want: true},
		// WSv2 出口原样转发上游事件、不做回程还原，摊平会让客户端收到无法匹配的平名。
		{name: "oauth_flatten_enabled_wsv2", account: flattenOAuth, transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2, want: false},
		// 透传账号先于 WSv2 分支经 HTTP 转发返回，开关打开时仍需摊平。
		{name: "oauth_flatten_enabled_wsv2_passthrough", account: flattenOAuth, transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2, passthroughEnabled: true, want: true},
		{name: "apikey_flatten_enabled_http", account: flattenAPIKey, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: false},
		{name: "apikey_http", account: apiKey, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: false},
		{name: "grok_oauth_http", account: grokOAuth, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: false},
		{name: "nil_account", account: nil, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, gatewayprovider.ShouldFlattenOpenAIResponsesNamespaces(
				tt.account, tt.transport, tt.passthroughEnabled, tt.compactPath,
			))
		})
	}
}

func TestShouldKeepOpenAIResponsesToolCallNamespaces(t *testing.T) {
	oauth := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	apiKey := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	setupToken := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeSetupToken}}
	flattenOAuth := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type:  capability.AccountTypeOAuth,
		Extra: map[string]any{"openai_responses_flatten_namespaces": true}},
	}

	tests := []struct {
		name               string
		account            *gatewayprovider.ExecutionAccount
		transport          egress.OpenAIUpstreamTransport
		passthroughEnabled bool
		compactPath        bool
		body               []byte
		want               bool
	}{
		{name: "oauth_http_keeps", account: oauth, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: true},
		{name: "oauth_http_passthrough_keeps", account: oauth, transport: egress.OpenAIUpstreamTransportHTTPSSE, passthroughEnabled: true, want: true},
		{name: "oauth_compact_strips", account: oauth, transport: egress.OpenAIUpstreamTransportHTTPSSE, compactPath: true, want: false},
		{name: "oauth_flatten_enabled_strips", account: flattenOAuth, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: false},
		{name: "oauth_wsv2_keeps", account: oauth, transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2, want: true},
		{name: "oauth_compact_wsv2_strips", account: oauth, transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2, compactPath: true, want: false},
		// API Key 默认按标准 Responses API 清理；请求显式声明 namespace 工具时，
		// 自定义上游需要原样接收对应的历史调用。
		{name: "apikey_without_namespace_tool_strips", account: apiKey, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: false},
		{name: "apikey_with_namespace_tool_keeps", account: apiKey, transport: egress.OpenAIUpstreamTransportHTTPSSE, body: []byte(`{"tools":[{"type":"namespace","name":"mcp__codex_app","tools":[]}]}`), want: true},
		{name: "apikey_with_mixed_case_namespace_tool_keeps", account: apiKey, transport: egress.OpenAIUpstreamTransportHTTPSSE, body: []byte(`{"tools":[{"type":" Namespace ","name":"mcp__codex_app","tools":[]}]}`), want: true},
		{name: "apikey_function_tool_with_namespace_field_strips", account: apiKey, transport: egress.OpenAIUpstreamTransportHTTPSSE, body: []byte(`{"tools":[{"type":"function","name":"automation_update","namespace":"mcp__codex_app"}]}`), want: false},
		{name: "apikey_compact_with_namespace_tool_strips", account: apiKey, transport: egress.OpenAIUpstreamTransportHTTPSSE, compactPath: true, body: []byte(`{"tools":[{"type":"namespace","name":"mcp__codex_app","tools":[]}]}`), want: false},
		{name: "setup_token_keeps", account: setupToken, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: true},
		{name: "nil_account", account: nil, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, gatewayprovider.ShouldKeepOpenAIResponsesToolCallNamespaces(
				tt.account, tt.transport, tt.passthroughEnabled, tt.compactPath, tt.body,
			))
		})
	}
}

func TestShouldStripOpenAIResponsesInputNamespaces(t *testing.T) {
	oauth := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	apiKey := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	setupToken := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeSetupToken}}
	grokOAuth := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}

	tests := []struct {
		name               string
		account            *gatewayprovider.ExecutionAccount
		transport          egress.OpenAIUpstreamTransport
		passthroughEnabled bool
		want               bool
	}{
		{name: "oauth_http", account: oauth, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: true},
		{name: "apikey_http", account: apiKey, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: true},
		{name: "oauth_wsv2", account: oauth, transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2, want: false},
		{name: "apikey_wsv2", account: apiKey, transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2, want: false},
		{name: "oauth_wsv2_passthrough", account: oauth, transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2, passthroughEnabled: true, want: true},
		{name: "apikey_wsv2_passthrough", account: apiKey, transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2, passthroughEnabled: true, want: true},
		{name: "setup_token_http", account: setupToken, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: true},
		{name: "grok_oauth_http", account: grokOAuth, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: false},
		{name: "nil_account", account: nil, transport: egress.OpenAIUpstreamTransportHTTPSSE, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, gatewayprovider.ShouldStripOpenAIResponsesInputNamespaces(tt.account, tt.transport, tt.passthroughEnabled))
		})
	}
}

func TestStripOpenAIResponsesInputNamespaces(t *testing.T) {
	body := []byte(`{
		"meta":9007199254740993,
		"scientific":1.25e+42,
		"escaped":"line\\n\\u003ctag\\u003e",
		"tools":[{"type":"function","name":"keep","namespace":"tool-namespace"}],
		"input":[
			{"type":"function_call","namespace":"n0","name":"one","content":{"namespace":"nested"},"large":9007199254740993},
			{"type":"message","namespace":"n1","content":[{"type":"input_text","text":"hello","namespace":"nested-content"}]},
			{"type":"custom_tool_call","namespace":"n2","input":"{}"},
			{"type":"function_call_output","namespace":"n3","output":"ok"},
			{"type":"item","namespace":"n4"},
			{"type":"item","namespace":"n5"},
			{"type":"item","namespace":"n6"},
			{"type":"item","namespace":"n7"}
		]
	}`)

	stripped, err := protocolbridge.StripOpenAIResponsesInputNamespaces(body, false)
	require.NoError(t, err)
	for index := 0; index < 8; index++ {
		require.False(t, gjson.GetBytes(stripped, "input."+strconv.Itoa(index)+".namespace").Exists())
	}
	require.Equal(t, "nested", gjson.GetBytes(stripped, "input.0.content.namespace").String())
	require.Equal(t, "nested-content", gjson.GetBytes(stripped, "input.1.content.0.namespace").String())
	require.Equal(t, "tool-namespace", gjson.GetBytes(stripped, "tools.0.namespace").String())
	require.Equal(t, gjson.GetBytes(body, "meta").Raw, gjson.GetBytes(stripped, "meta").Raw)
	require.Equal(t, gjson.GetBytes(body, "scientific").Raw, gjson.GetBytes(stripped, "scientific").Raw)
	require.Equal(t, gjson.GetBytes(body, "escaped").Raw, gjson.GetBytes(stripped, "escaped").Raw)
	require.Equal(t, gjson.GetBytes(body, "input.0.large").Raw, gjson.GetBytes(stripped, "input.0.large").Raw)
}

func TestStripOpenAIResponsesInputNamespacesLeavesOtherShapesByteExact(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"input":"text","namespace":"top-level"}`),
		[]byte(`{"input":{"namespace":"single-object"}}`),
		[]byte(`{"input":[{"content":{"namespace":"nested-only"}}],"tools":[{"namespace":"keep"}]}`),
	}
	for _, body := range tests {
		for _, keepToolCallNamespaces := range []bool{false, true} {
			stripped, err := protocolbridge.StripOpenAIResponsesInputNamespaces(body, keepToolCallNamespaces)
			require.NoError(t, err)
			require.Equal(t, body, stripped)
		}
	}
}

// 保留模式下只有工具调用项留住 namespace，普通历史项上的残留字段仍会被清理。
func TestStripOpenAIResponsesInputNamespacesKeepsToolCallNamespaces(t *testing.T) {
	body := []byte(`{
		"meta":9007199254740993,
		"input":[
			{"type":"function_call","namespace":"collaboration","name":"spawn_agent","arguments":"{}","large":9007199254740993},
			{"type":"custom_tool_call","namespace":"codex_app","name":"exec","input":"{}"},
			{"type":"tool_call","namespace":"mcp__codex_apps__gmail","name":"send"},
			{"type":"mcp_tool_call","namespace":"mcp__codex_apps__gmail","name":"list"},
			{"type":"message","namespace":"leftover","role":"assistant","content":[]},
			{"type":"function_call_output","namespace":"leftover","output":"ok"},
			{"type":"reasoning","namespace":"leftover"},
			{"type":"item","namespace":"leftover"}
		]
	}`)

	stripped, err := protocolbridge.StripOpenAIResponsesInputNamespaces(body, true)
	require.NoError(t, err)

	require.Equal(t, "collaboration", gjson.GetBytes(stripped, "input.0.namespace").String())
	require.Equal(t, "codex_app", gjson.GetBytes(stripped, "input.1.namespace").String())
	require.Equal(t, "mcp__codex_apps__gmail", gjson.GetBytes(stripped, "input.2.namespace").String())
	require.Equal(t, "mcp__codex_apps__gmail", gjson.GetBytes(stripped, "input.3.namespace").String())
	for index := 4; index < 8; index++ {
		require.False(t, gjson.GetBytes(stripped, "input."+strconv.Itoa(index)+".namespace").Exists())
	}
	require.Equal(t, gjson.GetBytes(body, "meta").Raw, gjson.GetBytes(stripped, "meta").Raw)
	require.Equal(t, gjson.GetBytes(body, "input.0.large").Raw, gjson.GetBytes(stripped, "input.0.large").Raw)

	// 类型比较不区分大小写与首尾空白。
	mixedCase := []byte(`{"input":[{"type":" Function_Call ","namespace":"collaboration","name":"spawn_agent"}]}`)
	keptMixedCase, err := protocolbridge.StripOpenAIResponsesInputNamespaces(mixedCase, true)
	require.NoError(t, err)
	require.Equal(t, mixedCase, keptMixedCase)

	// 全部为调用项时不重建请求，保持字节级不变。
	callsOnly := []byte(`{"input":[{"type":"function_call","namespace":"collaboration","name":"spawn_agent"}]}`)
	unchanged, err := protocolbridge.StripOpenAIResponsesInputNamespaces(callsOnly, true)
	require.NoError(t, err)
	require.Equal(t, callsOnly, unchanged)

	strippedAll, err := protocolbridge.StripOpenAIResponsesInputNamespaces(body, false)
	require.NoError(t, err)
	for index := 0; index < 8; index++ {
		require.False(t, gjson.GetBytes(strippedAll, "input."+strconv.Itoa(index)+".namespace").Exists())
	}
}
