package qoder

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func readAllString(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []string{
		"hello world",
		"test",
		"hello world!!!",
	}

	for _, tc := range cases {
		encoded := EncodeString(tc)
		if encoded == tc {
			t.Errorf("encode(%q) = %q, expected different", tc, encoded)
		}
		decoded, err := DecodeString(encoded)
		if err != nil {
			t.Errorf("decode(encode(%q)) error: %v", tc, err)
		}
		if decoded != tc {
			t.Errorf("decode(encode(%q)) = %q, want %q", tc, decoded, tc)
		}
	}
}

func TestEncodeNotStandardBase64(t *testing.T) {
	encoded := EncodeString("test")
	if encoded == "dGVzdA==" {
		t.Error("encoded result should not be standard base64")
	}
}

func TestSignCenterRequest(t *testing.T) {
	sig := SignCenterRequest("test_date")
	if len(sig) != 32 {
		t.Errorf("signature length = %d, want 32", len(sig))
	}
}

func TestExchangePATPostsSignedCenterRequest(t *testing.T) {
	var capturedHeader http.Header
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Clone()
		decoded, err := DecodeString(readAllString(t, r))
		if err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if err := json.Unmarshal([]byte(decoded), &capturedBody); err != nil {
			t.Fatalf("parse body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                 "user-1",
			"name":               "User",
			"userType":           "personal_standard",
			"securityOauthToken": "token-1",
			"refreshToken":       "refresh-1",
		})
	}))
	defer server.Close()

	identity, err := ExchangePAT("pat-1", &MachineIdentity{
		MachineID:    "machine-1",
		MachineToken: "machine-token",
		MachineType:  "5",
	}, server.URL)
	if err != nil {
		t.Fatalf("ExchangePAT: %v", err)
	}
	if identity.SecurityOauthToken != "token-1" {
		t.Fatalf("token = %q, want token-1", identity.SecurityOauthToken)
	}
	if capturedHeader.Get("signature") == "" {
		t.Fatal("signature header is empty")
	}
	payload, _ := capturedBody["payload"].(string)
	var inner map[string]any
	if err := json.Unmarshal([]byte(payload), &inner); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if inner["personalToken"] != "pat-1" {
		t.Fatalf("personalToken = %v, want pat-1", inner["personalToken"])
	}
	if inner["needRefresh"] != false {
		t.Fatalf("needRefresh = %v, want false", inner["needRefresh"])
	}
}

func TestExchangePATContextUsesProvidedDoerAndContext(t *testing.T) {
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("marker"), "ctx-value")
	called := false
	doer := func(req *http.Request) (*http.Response, error) {
		called = true
		if req.Context().Value(contextKey("marker")) != "ctx-value" {
			t.Fatalf("request context marker = %v, want ctx-value", req.Context().Value(contextKey("marker")))
		}
		if req.URL.String() != "https://center.example/algo/api/v3/user/jobToken?Encode=1" {
			t.Fatalf("request URL = %s", req.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"id":"user-1",
				"name":"User",
				"userType":"personal_standard",
				"securityOauthToken":"token-1",
				"refreshToken":"refresh-1"
			}`)),
			Request: req,
		}, nil
	}

	identity, err := ExchangePATContext(ctx, "pat-1", &MachineIdentity{
		MachineID:    "machine-1",
		MachineToken: "machine-token",
		MachineType:  "5",
	}, "https://center.example", doer)

	if err != nil {
		t.Fatalf("ExchangePATContext: %v", err)
	}
	if !called {
		t.Fatal("provided doer was not called")
	}
	if identity.SecurityOauthToken != "token-1" {
		t.Fatalf("token = %q, want token-1", identity.SecurityOauthToken)
	}
}

func TestRefreshSessionPostsRefreshPayload(t *testing.T) {
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decoded, err := DecodeString(readAllString(t, r))
		if err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if err := json.Unmarshal([]byte(decoded), &capturedBody); err != nil {
			t.Fatalf("parse body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                 "user-2",
			"name":               "User 2",
			"userType":           "personal_pro",
			"securityOauthToken": "new-token",
			"refreshToken":       "new-refresh",
		})
	}))
	defer server.Close()

	identity, err := RefreshSession("old-refresh", "old-token", &MachineIdentity{
		MachineID:    "machine-1",
		MachineToken: "machine-token",
		MachineType:  "5",
	}, server.URL)
	if err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}
	if identity.SecurityOauthToken != "new-token" {
		t.Fatalf("token = %q, want new-token", identity.SecurityOauthToken)
	}
	if identity.RefreshToken != "new-refresh" {
		t.Fatalf("refresh token = %q, want new-refresh", identity.RefreshToken)
	}
	payload, _ := capturedBody["payload"].(string)
	var inner map[string]any
	if err := json.Unmarshal([]byte(payload), &inner); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if inner["refreshToken"] != "old-refresh" {
		t.Fatalf("refreshToken = %v, want old-refresh", inner["refreshToken"])
	}
	if inner["securityOauthToken"] != "old-token" {
		t.Fatalf("securityOauthToken = %v, want old-token", inner["securityOauthToken"])
	}
	if inner["needRefresh"] != true {
		t.Fatalf("needRefresh = %v, want true", inner["needRefresh"])
	}
}

func TestRefreshSessionContextPropagatesCanceledContextToDoer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	doer := func(req *http.Request) (*http.Response, error) {
		called = true
		if req.Context().Err() != context.Canceled {
			t.Fatalf("request context err = %v, want context.Canceled", req.Context().Err())
		}
		return nil, req.Context().Err()
	}

	_, err := RefreshSessionContext(ctx, "old-refresh", "old-token", &MachineIdentity{
		MachineID:    "machine-1",
		MachineToken: "machine-token",
		MachineType:  "5",
	}, "https://center.example", doer)

	if !called {
		t.Fatal("provided doer was not called")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RefreshSessionContext error = %v, want context.Canceled", err)
	}
}

func TestSignQoderRequest(t *testing.T) {
	sig := SignQoderRequest("payload", "key", "date", "body", "/path")
	if len(sig) != 32 {
		t.Errorf("signature length = %d, want 32", len(sig))
	}
}

func TestComposeBearer(t *testing.T) {
	bearer := ComposeBearer("payload_b64", "signature")
	expected := "Bearer COSY.payload_b64.signature"
	if bearer != expected {
		t.Errorf("bearer = %q, want %q", bearer, expected)
	}
}

func TestDefaultModels(t *testing.T) {
	var ids []string
	for _, model := range DefaultModels {
		ids = append(ids, model.ID)
	}
	want := []string{
		"claude-opus-4-6",
		"auto",
		"performance",
		"efficient",
		"lite",
		"qwen3.7-max",
		"qwen3.7-plus",
		"deepseek-v4-pro",
		"deepseek-v4-flash",
		"glm-5.2",
		"kimi-k2.7-code",
		"minimax-m3",
	}
	if len(ids) != len(want) {
		t.Fatalf("default model count = %d, want %d", len(ids), len(want))
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("default model %d = %q, want %q", i, ids[i], want[i])
		}
	}
}

func TestNewSession(t *testing.T) {
	identity := &AuthIdentity{
		Name: "test",
		UID:  "test123",
	}
	machine := &MachineIdentity{
		MachineID:    "test-id",
		MachineToken: "test-token",
		MachineType:  "test-type",
	}

	session, err := NewSessionWithKey(identity, machine, []byte("abcdefghijklmnop"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if session.CosyKey == "" {
		t.Error("expected non-empty cosy_key")
	}
	if session.Info == "" {
		t.Error("expected non-empty info")
	}
	if session.Identity.Name != "test" {
		t.Errorf("identity name = %q, want %q", session.Identity.Name, "test")
	}
}

func TestNewSessionDefaultTempKeyIsASCIIHex(t *testing.T) {
	identity := &AuthIdentity{
		Name: "test",
		UID:  "test123",
	}
	machine := &MachineIdentity{
		MachineID:    "test-id",
		MachineToken: "test-token",
		MachineType:  "test-type",
	}

	session, err := NewSession(identity, machine)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if len(session.TempKey) != 16 {
		t.Fatalf("temp key length = %d, want 16", len(session.TempKey))
	}
	for i, b := range session.TempKey {
		if (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			t.Fatalf("temp key byte %d = %q, want ASCII hex", i, b)
		}
	}
}

func TestAESDecryptRejectsEmptyCiphertext(t *testing.T) {
	_, err := AESDecrypt(nil, []byte("abcdefghijklmnop"))
	if err == nil {
		t.Fatal("expected empty ciphertext error")
	}
	if err.Error() != "qoder: ciphertext is empty" {
		t.Fatalf("error = %q, want empty ciphertext", err.Error())
	}
}

func TestBuildPayloadB64(t *testing.T) {
	b64, err := BuildPayloadB64("test_info", "request123")
	if err != nil {
		t.Fatalf("BuildPayloadB64: %v", err)
	}
	if b64 == "" {
		t.Error("expected non-empty payload")
	}
}

func TestParseSSELineTextDelta(t *testing.T) {
	line := `data: {"body": "{\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	if events[0].Type != "text_delta" {
		t.Errorf("event type = %q, want text_delta", events[0].Type)
	}
	if events[0].Text != "Hello" {
		t.Errorf("event text = %q, want Hello", events[0].Text)
	}
}

func TestParseSSELineDone(t *testing.T) {
	events, err := ParseSSELine(`data: [DONE]`)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 || !events[0].IsDone {
		t.Error("expected IsDone event")
	}
}

func TestParseSSELineDoneInBody(t *testing.T) {
	events, err := ParseSSELine(`data: {"body": "[DONE]"}`)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 || !events[0].IsDone {
		t.Error("expected IsDone event")
	}
}

func TestParseSSELineEmptyBody(t *testing.T) {
	events, err := ParseSSELine(`data: {"body": ""}`)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestParseSSELineToolCall(t *testing.T) {
	line := `data: {"body": "{\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"tc1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"cmd\\\":\\\"ls\\\"}\"}}]}}]}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	if events[0].Type != "tool_call_delta" {
		t.Errorf("event type = %q, want tool_call_delta", events[0].Type)
	}
	if events[0].ToolName != "bash" {
		t.Errorf("tool name = %q, want bash", events[0].ToolName)
	}
}

func TestParseSSELineToolCallPreservesIndexTypeAndObjectArguments(t *testing.T) {
	line := `data: {"body": "{\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"tc1\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":{\"cmd\":\"ls\"}}}]}}]}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	event := events[0]
	if !event.HasToolCallIndex || event.ToolCallIndex != 0 {
		t.Fatalf("tool call index = (%v, %d), want (true, 0)", event.HasToolCallIndex, event.ToolCallIndex)
	}
	if event.ToolType != "function" {
		t.Fatalf("tool type = %q, want function", event.ToolType)
	}
	if event.Arguments != `{"cmd":"ls"}` {
		t.Fatalf("arguments = %q, want object JSON", event.Arguments)
	}
}

func TestParseSSELineToolCallSyntheticIndexSkipsEmptyPlaceholders(t *testing.T) {
	line := `data: {"body": "{\"choices\":[{\"delta\":{\"tool_calls\":[{}, {\"type\":\"function\"}, {\"type\":\"function\",\"function\":{\"arguments\":\"{\\\"command\\\":\\\"pwd\\\"}\"}}, {\"type\":\"function\",\"function\":{\"arguments\":\"{\\\"command\\\":\\\"printf OPENCODE_PARALLEL_OK\\\"}\"}}, {\"type\":\"function\",\"function\":{\"arguments\":\"{\\\"pattern\\\":\\\"docs/*.md\\\"}\"}}]}}]}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events length = %d, want 3: %#v", len(events), events)
	}
	for i, event := range events {
		if !event.HasToolCallIndex || event.ToolCallIndex != i {
			t.Fatalf("event %d index = (%v, %d), want (true, %d): %#v", i, event.HasToolCallIndex, event.ToolCallIndex, i, events)
		}
	}
	if events[0].Arguments != `{"command":"pwd"}` || events[1].Arguments != `{"command":"printf OPENCODE_PARALLEL_OK"}` || events[2].Arguments != `{"pattern":"docs/*.md"}` {
		t.Fatalf("arguments = %#v, want compact synthetic indexes with arguments", events)
	}
}

func TestParseSSELineToolUseEnvelopeEventsSkipTypeOnlyPlaceholder(t *testing.T) {
	line := `data: {"body": "{\"event\":\"tool_use_delta\",\"data\":{\"type\":\"function\"}}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want no event for type-only placeholder", events)
	}
}

func TestParseSSELineFlatToolCall(t *testing.T) {
	line := `data: {"body": "{\"choices\":[{\"delta\":{\"tool_calls\":[{\"tool_call_id\":\"tc1\",\"name\":\"Bash\",\"arguments\":{\"command\":\"pwd\"}}]}}]}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	event := events[0]
	if event.Type != "tool_call_delta" || event.ToolCallID != "tc1" || event.ToolName != "Bash" {
		t.Fatalf("tool event = %#v, want tc1 Bash", event)
	}
	if event.ToolType != "function" {
		t.Fatalf("tool type = %q, want function", event.ToolType)
	}
	if event.Arguments != `{"command":"pwd"}` {
		t.Fatalf("arguments = %q, want object JSON", event.Arguments)
	}
}

func TestParseSSELineToolUseEnvelopeEvents(t *testing.T) {
	start := `data: {"body": "{\"event\":\"tool_use_start\",\"data\":{\"id\":\"tc1\",\"name\":\"Bash\"}}"}`
	delta := `data: {"body": "{\"event\":\"tool_use_delta\",\"data\":{\"tool_call_id\":\"tc1\",\"name\":\"Bash\",\"arguments\":\"{\\\"command\\\":\\\"pwd\\\"}\"}}"}`
	events, err := ParseSSELine(start)
	if err != nil {
		t.Fatalf("ParseSSELine start: %v", err)
	}
	if len(events) != 1 || events[0].ToolCallID != "tc1" || events[0].ToolName != "Bash" {
		t.Fatalf("start events = %#v, want tc1 Bash", events)
	}
	events, err = ParseSSELine(delta)
	if err != nil {
		t.Fatalf("ParseSSELine delta: %v", err)
	}
	if len(events) != 1 || events[0].ToolCallID != "tc1" || events[0].Arguments != `{"command":"pwd"}` {
		t.Fatalf("delta events = %#v, want arguments", events)
	}
}

func TestParseSSELineToolUseEnvelopeEventsPreserveIndex(t *testing.T) {
	start := `data: {"body": "{\"event\":\"tool_use_start\",\"data\":{\"index\":2,\"id\":\"tc1\",\"name\":\"Bash\"}}"}`
	delta := `data: {"body": "{\"event\":\"tool_use_delta\",\"data\":{\"index\":2,\"arguments\":\"{\\\"command\\\":\\\"pwd\\\"}\"}}"}`

	events, err := ParseSSELine(start)
	if err != nil {
		t.Fatalf("ParseSSELine start: %v", err)
	}
	if len(events) != 1 || !events[0].HasToolCallIndex || events[0].ToolCallIndex != 2 || events[0].ToolName != "Bash" {
		t.Fatalf("start events = %#v, want index 2 Bash", events)
	}
	events, err = ParseSSELine(delta)
	if err != nil {
		t.Fatalf("ParseSSELine delta: %v", err)
	}
	if len(events) != 1 || !events[0].HasToolCallIndex || events[0].ToolCallIndex != 2 || events[0].Arguments != `{"command":"pwd"}` {
		t.Fatalf("delta events = %#v, want index 2 arguments", events)
	}
}

func TestParseSSELineFinalMessageToolCalls(t *testing.T) {
	line := `data: {"body": "{\"choices\":[{\"message\":{\"tool_calls\":[{\"id\":\"tc1\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":{\"cmd\":\"ls\"}}}]},\"finish_reason\":\"tool_calls\"}]}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events length = %d, want 2", len(events))
	}
	event := events[0]
	if event.Type != "tool_call_delta" || event.ToolCallID != "tc1" || event.ToolName != "bash" {
		t.Fatalf("tool event = %#v, want tc1 bash", event)
	}
	if event.Arguments != `{"cmd":"ls"}` {
		t.Fatalf("arguments = %q, want object JSON", event.Arguments)
	}
	if !events[1].IsDone {
		t.Fatalf("second event = %#v, want done", events[1])
	}
}

func TestParseSSELineReasoning(t *testing.T) {
	line := `data: {"body": "{\"choices\":[{\"delta\":{\"reasoning_content\":\"Let me think...\"}}]}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	if events[0].Type != "reasoning_delta" {
		t.Errorf("event type = %q, want reasoning_delta", events[0].Type)
	}
	if events[0].Text != "Let me think..." {
		t.Errorf("event text = %q, want Let me think...", events[0].Text)
	}
}

func TestParseSSELineContentAndReasoningAreSeparate(t *testing.T) {
	line := `data: {"body": "{\"choices\":[{\"delta\":{\"reasoning_content\":\"Think\",\"content\":\"Answer\"}}]}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events length = %d, want 2", len(events))
	}
	if events[0].Type != "text_delta" || events[0].Text != "Answer" {
		t.Fatalf("first event = %#v, want text_delta Answer", events[0])
	}
	if events[1].Type != "reasoning_delta" || events[1].Text != "Think" {
		t.Fatalf("second event = %#v, want reasoning_delta Think", events[1])
	}
}

func TestParseSSELineUsage(t *testing.T) {
	line := `data: {"body": "{\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":34,\"total_tokens\":46}}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	if events[0].Type != "usage" || !events[0].HasUsage {
		t.Fatalf("event = %#v, want usage event", events[0])
	}
	if events[0].PromptTokens != 12 {
		t.Fatalf("prompt tokens = %d, want 12", events[0].PromptTokens)
	}
	if events[0].CompletionTokens != 34 {
		t.Fatalf("completion tokens = %d, want 34", events[0].CompletionTokens)
	}
	if events[0].TotalTokens != 46 {
		t.Fatalf("total tokens = %d, want 46", events[0].TotalTokens)
	}
}

func TestParseSSELineUsageAcceptsAnthropicNames(t *testing.T) {
	line := `data: {"body": "{\"usage\":{\"input_tokens\":7,\"output_tokens\":9}}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	if events[0].PromptTokens != 7 || events[0].CompletionTokens != 9 || events[0].TotalTokens != 16 {
		t.Fatalf("usage event = %#v, want 7/9/16", events[0])
	}
}

func TestParseSSELineWrappedUsagePreservesDetails(t *testing.T) {
	line := `data: {"body": "{\"usage\":{\"prompt_tokens\":66637,\"completion_tokens\":6,\"total_tokens\":66643,\"prompt_tokens_details\":{\"cached_tokens\":66612,\"cacheable_tokens\":19},\"completion_tokens_details\":{\"reasoning_tokens\":0}}}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 || !events[0].HasUsage {
		t.Fatalf("events = %#v, want one usage event", events)
	}
	event := events[0]
	if event.PromptTokens != 66637 || event.CompletionTokens != 6 || event.TotalTokens != 66643 {
		t.Fatalf("usage = %#v, want upstream totals", event)
	}
	if event.UsageDetails.PromptTokensDetails == nil {
		t.Fatal("prompt token details missing")
	}
	if event.UsageDetails.PromptTokensDetails.CachedTokens != 66612 {
		t.Fatalf("cached tokens = %d, want 66612", event.UsageDetails.PromptTokensDetails.CachedTokens)
	}
	if event.UsageDetails.PromptTokensDetails.CacheableTokens != 19 {
		t.Fatalf("cacheable tokens = %d, want 19", event.UsageDetails.PromptTokensDetails.CacheableTokens)
	}
	if event.UsageDetails.CompletionTokensDetails == nil {
		t.Fatal("completion token details missing")
	}
	if event.UsageDetails.CompletionTokensDetails.ReasoningTokens != 0 {
		t.Fatalf("reasoning tokens = %d, want 0", event.UsageDetails.CompletionTokensDetails.ReasoningTokens)
	}
}

func TestParseSSELineUsageAcceptsNumericStringsAndFloats(t *testing.T) {
	line := `data: {"body": "{\"usage\":{\"prompt_tokens\":\"66637.0\",\"completion_tokens\":6.0,\"total_tokens\":\"66643\",\"prompt_tokens_details\":{\"cached_tokens\":\"66612.0\",\"cacheable_tokens\":19.0},\"completion_tokens_details\":{\"reasoning_tokens\":\"7.0\"}}}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 || !events[0].HasUsage {
		t.Fatalf("events = %#v, want one usage event", events)
	}
	event := events[0]
	if event.PromptTokens != 66637 || event.CompletionTokens != 6 || event.TotalTokens != 66643 {
		t.Fatalf("usage = %#v, want upstream totals", event)
	}
	if event.UsageDetails.PromptTokensDetails == nil || event.UsageDetails.PromptTokensDetails.CachedTokens != 66612 || event.UsageDetails.PromptTokensDetails.CacheableTokens != 19 {
		t.Fatalf("prompt details = %#v, want cached/cacheable", event.UsageDetails.PromptTokensDetails)
	}
	if event.UsageDetails.CompletionTokensDetails == nil || event.UsageDetails.CompletionTokensDetails.ReasoningTokens != 7 {
		t.Fatalf("completion details = %#v, want reasoning 7", event.UsageDetails.CompletionTokensDetails)
	}
}

func TestParseSSELineMalformedWrapper(t *testing.T) {
	_, err := ParseSSELine("data: not json")
	if err == nil {
		t.Error("expected error for malformed wrapper")
	}
}

func TestParseSSELineMalformedBody(t *testing.T) {
	_, err := ParseSSELine(`data: {"body": "not json"}`)
	if err == nil {
		t.Error("expected error for malformed inner JSON")
	}
}

func TestParseSSELineUpstreamErrorWrapper(t *testing.T) {
	line := `data: {"headers":{"Content-Type":["application/json"]},"body":"{\"code\":\"101\",\"message\":\"Signature invalid\"}","statusCodeValue":403,"statusCode":"FORBIDDEN"}`
	events, err := ParseSSELine(line)
	if err == nil {
		t.Fatal("expected upstream API error")
	}
	if len(events) != 0 {
		t.Fatalf("events length = %d, want 0", len(events))
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.StatusCode != 403 {
		t.Errorf("status code = %d, want 403", apiErr.StatusCode)
	}
	if apiErr.Code != "101" {
		t.Errorf("code = %q, want 101", apiErr.Code)
	}
	if apiErr.Message != "Signature invalid" {
		t.Errorf("message = %q, want Signature invalid", apiErr.Message)
	}
	if got := apiErr.Error(); got != "Qoder upstream error 101: Signature invalid" {
		t.Errorf("error = %q, want Qoder upstream error 101: Signature invalid", got)
	}
}

func TestParseSSELineAgentLimitError(t *testing.T) {
	line := `data: {"headers":{"Content-Type":["application/json"]},"body":"{\"code\":\"115\",\"message\":\"{\\\"agentLimitResetTime\\\":1783841289162}\"}","statusCodeValue":429,"statusCode":"TOO_MANY_REQUESTS"}`
	_, err := ParseSSELine(line)
	if err == nil {
		t.Fatal("expected upstream API error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.Code != "115" {
		t.Fatalf("code = %q, want 115", apiErr.Code)
	}
	if apiErr.AgentLimitResetTime != 1783841289162 {
		t.Fatalf("agentLimitResetTime = %d", apiErr.AgentLimitResetTime)
	}
	resetAt, ok := apiErr.AgentLimitResetAt()
	if !ok {
		t.Fatal("expected reset time")
	}
	if got := resetAt.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04:05"); got != "2026-07-12 15:28:09" {
		t.Fatalf("reset time = %s", got)
	}
	if got := apiErr.Error(); got != "Qoder agent limit reached; resets at 2026-07-12 15:28:09 Asia/Shanghai" {
		t.Fatalf("error = %q", got)
	}
}

func TestParseAPIErrorBodyAgentLimitDirectBody(t *testing.T) {
	apiErr := ParseAPIErrorBody(429, `{"agentLimitResetTime":1783841289162}`)
	if apiErr.AgentLimitResetTime != 1783841289162 {
		t.Fatalf("agentLimitResetTime = %d", apiErr.AgentLimitResetTime)
	}
}

func TestLocalAuthInfoToIdentity(t *testing.T) {
	info := &AuthInfo{
		Name:               "test user",
		UID:                "uid123",
		SecurityOauthToken: "dt-token",
		RefreshToken:       "drt-refresh",
		UserType:           "personal_standard",
	}
	id := info.ToAuthIdentity()
	if id.Name != "test user" {
		t.Errorf("name = %q", id.Name)
	}
	if id.UID != "uid123" {
		t.Errorf("uid = %q", id.UID)
	}
	if id.SecurityOauthToken != "dt-token" {
		t.Errorf("token = %q", id.SecurityOauthToken)
	}
}
