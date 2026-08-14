package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCountGrokNativeSearchCallsFromJSONBytes(t *testing.T) {
	t.Parallel()
	require.Equal(t, 0, countGrokNativeSearchCallsFromJSONBytes(nil))
	require.Equal(t, 0, countGrokNativeSearchCallsFromJSONBytes([]byte(`{"output":[]}`)))
	body := []byte(`{"output":[
		{"type":"web_search_call","id":"ws1","status":"completed"},
		{"type":"x_search_call","id":"xs1"},
		{"type":"function_call","name":"tool_search","call_id":"ts1"},
		{"type":"function_call","name":"lookup","call_id":"other"}
	]}`)
	require.Equal(t, 3, countGrokNativeSearchCallsFromJSONBytes(body))
}

func TestCountGrokNativeSearchCallsFromJSONBytes_PrefersNestedResponse(t *testing.T) {
	t.Parallel()
	body := []byte(`{"output":[{"type":"web_search_call","id":"duplicate"}],"response":{"output":[{"type":"web_search_call","id":"duplicate"},{"type":"x_search_call","id":"xs1"}]}}`)
	require.Equal(t, 2, countGrokNativeSearchCallsFromJSONBytes(body))
}

func TestCountGrokNativeSearchCallsFromJSONBytes_FallsBackWhenNestedOutputNull(t *testing.T) {
	t.Parallel()
	body := []byte(`{"output":[{"type":"web_search_call","id":"ws1"}],"response":{"output":null}}`)
	require.Equal(t, 1, countGrokNativeSearchCallsFromJSONBytes(body))
}

func TestCountGrokNativeSearchCallsFromSSEBodyDedups(t *testing.T) {
	t.Parallel()
	sse := stringsJoin(
		`data: {"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws1","call_id":"c1"}}`,
		`data: {"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws1","call_id":"c1"}}`,
		`data: {"type":"response.completed","response":{"output":[{"type":"web_search_call","id":"ws1","call_id":"c1"},{"type":"x_search_call","id":"xs1","call_id":"c2"}]}}`,
	)
	require.Equal(t, 2, countGrokNativeSearchCallsFromSSEBody(sse))
}

func TestCountGrokNativeSearchCallsInSSEDataDedup_LiveStreamPath(t *testing.T) {
	t.Parallel()
	// 模拟实时流式累加器，依次处理 item.done 与 response.completed。
	// 同一 call_id 只能计费一次，防止附加费接近翻倍的回归。
	seen := make(map[string]struct{})
	done := []byte(`{"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws1","call_id":"c1"}}`)
	completed := []byte(`{"type":"response.completed","response":{"output":[{"type":"web_search_call","id":"ws1","call_id":"c1"},{"type":"x_search_call","id":"xs1","call_id":"c2"}]}}`)
	require.Equal(t, 1, countGrokNativeSearchCallsInSSEDataDedup(done, seen))
	require.Equal(t, 1, countGrokNativeSearchCallsInSSEDataDedup(completed, seen))
	// 未去重的原始路径仍会重复统计同一对事件包。
	require.Equal(t, 1, countGrokNativeSearchCallsInSSEData(done))
	require.Equal(t, 2, countGrokNativeSearchCallsInSSEData(completed))
}

func TestCountGrokNativeSearchCallsInSSEDataDedup_NoIDStillDedups(t *testing.T) {
	t.Parallel()
	// 上游有时省略 call_id 或 id，合成键仍必须避免重复计费。
	seen := make(map[string]struct{})
	done := []byte(`{"type":"response.output_item.done","item":{"type":"web_search_call"}}`)
	completed := []byte(`{"type":"response.completed","response":{"output":[{"type":"web_search_call"}]}}`)
	require.Equal(t, 1, countGrokNativeSearchCallsInSSEDataDedup(done, seen))
	require.Equal(t, 0, countGrokNativeSearchCallsInSSEDataDedup(completed, seen))
}

func TestCountGrokNativeSearchCallsInSSEDataDedup_MultipleNoIDCalls(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{})
	firstDone := []byte(`{"type":"response.output_item.done","item":{"type":"web_search_call"}}`)
	secondDone := []byte(`{"type":"response.output_item.done","item":{"type":"web_search_call"}}`)
	completed := []byte(`{"type":"response.completed","response":{"output":[{"type":"web_search_call"},{"type":"web_search_call"}]}}`)
	require.Equal(t, 1, countGrokNativeSearchCallsInSSEDataDedup(firstDone, seen))
	require.Equal(t, 1, countGrokNativeSearchCallsInSSEDataDedup(secondDone, seen))
	require.Equal(t, 0, countGrokNativeSearchCallsInSSEDataDedup(completed, seen))
}

func stringsJoin(lines ...string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n\n"
	}
	return out
}
