package qoder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Captured requests verify that Gateway transport URLs and signing paths use
// different representations. Header fixtures distinguish global and CN machine
// shapes, and SSE/error fixtures retain identity, usage, completion, and numeric
// retry codes while checking redaction of credential text.

// testSession returns new identity and machine structs with fixed global header
// values. Encrypted fields are fixture strings for signing probes; tests can
// modify the returned machine and identity independently.
func testSession() *SessionContext {
	return &SessionContext{
		CosyKey: "test-cosy-key",
		Info:    "test-info",
		Identity: &AuthIdentity{
			Name:           "test",
			UID:            "u-123",
			AID:            "a-456",
			OrganizationID: "org-789",
		},
		Machine: &MachineIdentity{
			MachineID:    "mid-abc",
			MachineToken: "mytoken",
			MachineType:  "5",
		},
	}
}

// TestCNClientUsesGatewayEndpointVersionAndCanonicalSignaturePath captures a CN
// inference request with fixed machine metadata. It checks endpoint and version
// fields, empty CN machine values, and a signature recomputed from the logical
// path.
func TestCNClientUsesGatewayEndpointVersionAndCanonicalSignaturePath(t *testing.T) {
	profile := MustProfileForSite(SiteCN)
	profile.GatewayBaseURL = "https://gateway.example"
	client := NewClientForProfile(profile)
	client.MachineOS = "aarch64_darwin"
	session := testSession()
	session.Site = SiteCN
	session.ClientVersion = CNClientVersion
	var captured *http.Request
	var encodedBody string
	doer := func(req *http.Request) (*http.Response, error) {
		captured = req
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		encodedBody = string(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
			Request:    req,
		}, nil
	}

	resp, err := client.StreamRequestContextWithDoer(context.Background(), session, "", []byte(`{"model":"auto"}`), nil, doer)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "gateway.example", captured.URL.Host)
	require.Equal(t, "/algo/api/v2/service/pro/sse/agent_chat_generation", captured.URL.Path)
	require.Equal(t, "1.1.86-hotfix.5.3", captured.Header.Get("Cosy-Version"))
	require.Equal(t, "aarch64_darwin", captured.Header.Get("Cosy-Machineos"))
	require.Equal(t, "mid-abc", captured.Header.Get("Cosy-Machineid"))
	require.Equal(t, []string{""}, captured.Header.Values("Cosy-Machinetoken"))
	require.Equal(t, []string{""}, captured.Header.Values("Cosy-Machinetype"))
	require.Equal(t, []string{""}, captured.Header.Values("Cosy-Machinecode"))

	authorization := strings.TrimPrefix(captured.Header.Get("Authorization"), "Bearer COSY.")
	parts := strings.Split(authorization, ".")
	require.Len(t, parts, 2)
	expectedSignature := SignQoderRequest(
		parts[0],
		session.CosyKey,
		captured.Header.Get("Cosy-Date"),
		encodedBody,
		"/api/v2/service/pro/sse/agent_chat_generation",
	)
	require.Equal(t, expectedSignature, parts[1])
}

// TestJSONRequestAddsEncodeQueryWithoutSigningIt sends encoded Gateway JSON with
// an existing region query. It checks the transport Encode marker and recomputes
// authorization with the path excluding both query parameters.
func TestJSONRequestAddsEncodeQueryWithoutSigningIt(t *testing.T) {
	profile := MustProfileForSite(SiteCN)
	profile.GatewayBaseURL = "https://gateway.example"
	client := NewClientForProfile(profile)
	session := testSession()
	session.Site = SiteCN
	var captured *http.Request
	var encodedBody string
	doer := func(req *http.Request) (*http.Response, error) {
		captured = req
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		encodedBody = string(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    req,
		}, nil
	}

	err := client.JSONRequestContextWithDoer(
		context.Background(),
		http.MethodPost,
		session,
		"/api/v3/user/status?region=cn",
		[]byte(`{"userId":"user-1"}`),
		nil,
		doer,
		&map[string]any{},
	)
	require.NoError(t, err)
	require.Equal(t, "/algo/api/v3/user/status", captured.URL.Path)
	require.Equal(t, "1", captured.URL.Query().Get("Encode"))
	require.Equal(t, "cn", captured.URL.Query().Get("region"))

	authorization := strings.TrimPrefix(captured.Header.Get("Authorization"), "Bearer COSY.")
	parts := strings.Split(authorization, ".")
	require.Len(t, parts, 2)
	expectedSignature := SignQoderRequest(
		parts[0],
		session.CosyKey,
		captured.Header.Get("Cosy-Date"),
		encodedBody,
		"/api/v3/user/status",
	)
	require.Equal(t, expectedSignature, parts[1])
}

// TestSignatureJSONRequestUsesAppcodeHeadersWithoutAuthorization sends CN status
// with no completed identity. It checks Appcode signing and machine metadata
// while rejecting encrypted-session headers from this pre-login request.
func TestSignatureJSONRequestUsesAppcodeHeadersWithoutAuthorization(t *testing.T) {
	profile := MustProfileForSite(SiteCN)
	profile.GatewayBaseURL = "https://gateway.example"
	client := NewClientForProfile(profile)
	client.ClientIP = "172.18.0.1"
	session := testSession()
	session.Identity = nil
	session.Site = SiteCN
	var captured *http.Request
	doer := func(req *http.Request) (*http.Response, error) {
		captured = req
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    req,
		}, nil
	}

	err := client.SignatureJSONRequestContextWithDoer(
		context.Background(),
		http.MethodPost,
		session,
		AuthStatusPath,
		[]byte(`{"userId":"user-1"}`),
		nil,
		doer,
		&map[string]any{},
	)

	require.NoError(t, err)
	require.Equal(t, "1", captured.URL.Query().Get("Encode"))
	require.Equal(t, "0", captured.Header.Get("Cosy-Clienttype"))
	require.NotEmpty(t, captured.Header.Get("Date"))
	require.Equal(t, AppCode, captured.Header.Get("Appcode"))
	require.NotEmpty(t, captured.Header.Get("Signature"))
	require.NotContains(t, captured.Header, "Authorization")
	require.NotContains(t, captured.Header, "Cosy-Key")
	require.NotContains(t, captured.Header, "Cosy-User")
	require.NotContains(t, captured.Header, "Cosy-Date")
	require.NotContains(t, captured.Header, "Cosy-Data-Policy")
	require.NotContains(t, captured.Header, "Cosy-Organization-Id")
	require.NotContains(t, captured.Header, "Cosy-Organization-Tags")
}

// TestBearerJSONRequestUsesSecurityOAuthToken captures an imported CN quota
// request with a security token. It checks the /algo URL and token Bearer value
// without COSY key/date headers.
func TestBearerJSONRequestUsesSecurityOAuthToken(t *testing.T) {
	profile := MustProfileForSite(SiteCN)
	profile.GatewayBaseURL = "https://gateway.example"
	client := NewClientForProfile(profile)
	session := testSession()
	session.Site = SiteCN
	session.Identity.SecurityOauthToken = "security-token"
	var captured *http.Request
	doer := func(req *http.Request) (*http.Response, error) {
		captured = req
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    req,
		}, nil
	}

	err := client.BearerJSONRequestContextWithDoer(
		context.Background(),
		http.MethodGet,
		session,
		QuotaUsagePath,
		nil,
		nil,
		doer,
		&map[string]any{},
	)

	require.NoError(t, err)
	require.Equal(t, "/algo"+QuotaUsagePath, captured.URL.Path)
	require.Equal(t, "Bearer security-token", captured.Header.Get("Authorization"))
	require.NotContains(t, captured.Header, "Cosy-Key")
	require.NotContains(t, captured.Header, "Cosy-Date")
}

// getHeaders builds global signed headers around the fixed test session and
// encoded body. It returns the request-owned header map for field assertions,
// with generated date and signature values left available.
func getHeaders(t *testing.T) http.Header {
	t.Helper()
	c := NewClient("https://test.qoder.sh")
	req, _ := http.NewRequest(http.MethodPost, "https://test.qoder.sh/test", nil)
	c.setHeaders(req, testSession(), "/test", "encoded-body")
	return req.Header
}

// TestHeadersClientIPIsMachineID constructs global headers from a fixed session
// machine ID. It checks that the client-IP field carries that machine identifier
// for the global request shape.
func TestHeadersClientIPIsMachineID(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-clientip") != "mid-abc" {
		t.Errorf("cosy-clientip = %q, want mid-abc", h.Get("cosy-clientip"))
	}
}

// TestHeadersMachineTypeIs5 constructs global headers with the fixture's saved
// machine type. The assertion checks that its protocol value five reaches the
// outgoing machine-type header.
func TestHeadersMachineTypeIs5(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-machinetype") != "5" {
		t.Errorf("cosy-machinetype = %q, want 5", h.Get("cosy-machinetype"))
	}
}

// TestHeadersUsePersistedMachineToken constructs global headers with a saved
// machine-token fixture. It checks that the outgoing token field uses the
// persisted value available on the session machine.
func TestHeadersUsePersistedMachineToken(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-machinetoken") != "mytoken" {
		t.Errorf("cosy-machinetoken = %q, want mytoken", h.Get("cosy-machinetoken"))
	}
}

// TestHeadersDataPolicyIsDisagree constructs a global signed request with the
// standard fixture identity. It checks the exact lowercase data-policy value
// selected for that site's header shape.
func TestHeadersDataPolicyIsDisagree(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-data-policy") != "disagree" {
		t.Errorf("cosy-data-policy = %q, want disagree", h.Get("cosy-data-policy"))
	}
}

// TestHeadersUseGlobalSiteVersion constructs the default global client and
// session headers. It checks the exact version string emitted when the request
// uses the global integration defaults.
func TestHeadersUseGlobalSiteVersion(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-version") != "1.1.86-hotfix.5.3" {
		t.Errorf("cosy-version = %q, want 1.1.86-hotfix.5.3", h.Get("cosy-version"))
	}
}

// TestHeadersIncludeMachineOSAndLegacyFallbacks removes saved global machine
// token and type values before signing. It checks ID-based token fallback,
// client type five, and the injected platform string.
func TestHeadersIncludeMachineOSAndLegacyFallbacks(t *testing.T) {
	session := testSession()
	session.Machine.MachineToken = ""
	session.Machine.MachineType = ""
	client := NewClient("https://test.qoder.sh")
	client.MachineOS = "aarch64_darwin"
	req, _ := http.NewRequest(http.MethodPost, "https://test.qoder.sh/test", nil)
	client.setHeaders(req, session, "/test", "encoded-body")
	require.Equal(t, session.Machine.MachineID, req.Header.Get("cosy-machinetoken"))
	require.Equal(t, "5", req.Header.Get("cosy-machinetype"))
	require.Equal(t, "aarch64_darwin", req.Header.Get("cosy-machineos"))
}

// TestCNHeadersIgnoreLegacyRandomMachineFields signs a CN request using a
// session with legacy random machine fields. It checks cleared fields, CN
// client/IP/policy values, and absence of global business headers.
func TestCNHeadersIgnoreLegacyRandomMachineFields(t *testing.T) {
	session := testSession()
	session.Site = SiteCN
	client := NewClientForProfile(MustProfileForSite(SiteCN))
	client.ClientIP = "172.18.0.1"
	req, _ := http.NewRequest(http.MethodPost, "https://gateway.qoder.com.cn/test", nil)

	client.setHeaders(req, session, "/test", "encoded-body")

	require.Equal(t, "mid-abc", req.Header.Get("cosy-machineid"))
	require.Equal(t, []string{""}, req.Header.Values("cosy-machinetoken"))
	require.Equal(t, []string{""}, req.Header.Values("cosy-machinetype"))
	require.Equal(t, []string{""}, req.Header.Values("cosy-machinecode"))
	require.Equal(t, "172.18.0.1", req.Header.Get("cosy-clientip"))
	require.Equal(t, "0", req.Header.Get("cosy-clienttype"))
	require.Equal(t, "DISAGREE", req.Header.Get("cosy-data-policy"))
	require.Equal(t, []string{""}, req.Header.Values("cosy-organization-tags"))
	require.NotContains(t, req.Header, "Cosy-Scene")
	require.NotContains(t, req.Header, "Cosy-Business-Product")
	require.NotContains(t, req.Header, "Cosy-Business-Type")
}

// TestHeadersOrganizationID constructs global headers from the fixture
// identity's organization metadata. It checks that the selected organization
// identifier reaches the outgoing COSY organization-ID header.
func TestHeadersOrganizationID(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-organization-id") != "org-789" {
		t.Errorf("cosy-organization-id = %q, want org-789", h.Get("cosy-organization-id"))
	}
}

// TestHeadersOrganizationTags constructs global headers with the standard
// session fixture. It checks the Normal organization-tags value selected by the
// global request-header builder for this site.
func TestHeadersOrganizationTags(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-organization-tags") != "Normal" {
		t.Errorf("cosy-organization-tags = %q, want Normal", h.Get("cosy-organization-tags"))
	}
}

// TestHeadersScene constructs the global inference header set around fixed
// session metadata. It checks the assistant scene value used with that site's
// CLI agent business headers.
func TestHeadersScene(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-scene") != "assistant" {
		t.Errorf("cosy-scene = %q, want assistant", h.Get("cosy-scene"))
	}
}

// TestHeadersBusinessProduct constructs the global inference header set around
// fixed session metadata. It checks the cli business product value selected
// alongside the global scene and business type.
func TestHeadersBusinessProduct(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-business-product") != "cli" {
		t.Errorf("cosy-business-product = %q, want cli", h.Get("cosy-business-product"))
	}
}

// TestHeadersBusinessType constructs the global inference header set around
// fixed session metadata. It checks the agent business type emitted for that
// site's CLI request representation.
func TestHeadersBusinessType(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-business-type") != "agent" {
		t.Errorf("cosy-business-type = %q, want agent", h.Get("cosy-business-type"))
	}
}

// TestHeadersNoHardcodedIP inspects the global client-IP value from the fixed
// machine fixture. It rejects the specific 169.254.198.161 value, protecting
// that request's machine-derived header representation.
func TestHeadersNoHardcodedIP(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-clientip") == "169.254.198.161" {
		t.Error("cosy-clientip should not be hardcoded 169.254.198.161")
	}
}

// TestQoderGatewayShouldFailoverRetryableUpstreamErrors passes quota,
// entitlement, server, authentication, and ordinary failures to compatible retry
// policy. It accepts retryable vendor/HTTP cases and rejects 401 plus unwrapped
// errors.
func TestQoderGatewayShouldFailoverRetryableUpstreamErrors(t *testing.T) {
	require.True(t, MaySwitchCompatibleAttempt(&APIError{StatusCode: http.StatusTooManyRequests}))
	require.True(t, MaySwitchCompatibleAttempt(&APIError{StatusCode: http.StatusBadGateway, Code: "115"}))
	require.True(t, MaySwitchCompatibleAttempt(&APIError{StatusCode: http.StatusForbidden, Code: "115"}))
	require.True(t, MaySwitchCompatibleAttempt(&APIError{StatusCode: http.StatusForbidden, Code: "112"}))
	require.True(t, MaySwitchCompatibleAttempt(&APIError{StatusCode: http.StatusInternalServerError}))
	require.False(t, MaySwitchCompatibleAttempt(&APIError{StatusCode: http.StatusUnauthorized}))
	require.False(t, MaySwitchCompatibleAttempt(fmt.Errorf("plain error")))
}

// TestParseSSELineTextDelta parses a wrapped choice containing one visible
// content fragment. It checks exactly one text_delta event with the original
// Hello text.
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

// TestParseSSELineDone parses the standalone data DONE marker emitted at stream
// completion. It requires one completion event so consumers stop scanning that
// representation.
func TestParseSSELineDone(t *testing.T) {
	events, err := ParseSSELine(`data: [DONE]`)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 || !events[0].IsDone {
		t.Error("expected IsDone event")
	}
}

// TestParseSSELineDoneInBody parses the DONE marker inside a Qoder wrapper body.
// It requires one completion event, giving wrapped and standalone terminal forms
// matching observable behavior.
func TestParseSSELineDoneInBody(t *testing.T) {
	events, err := ParseSSELine(`data: {"body": "[DONE]"}`)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 || !events[0].IsDone {
		t.Error("expected IsDone event")
	}
}

// TestParseSSELineEmptyBody parses a valid wrapper carrying an empty body
// string. It requires no error and no events so the scanner can continue to
// later content.
func TestParseSSELineEmptyBody(t *testing.T) {
	events, err := ParseSSELine(`data: {"body": ""}`)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

// TestParseSSELineToolCall parses a wrapped nested function call named bash. It
// checks one tool_call_delta and the retained tool name used by later invocation
// assembly.
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

// TestParseSSELineToolCallPreservesIndexTypeAndObjectArguments parses index zero
// with function type and object arguments. It checks explicit index presence,
// type, and serialized JSON argument text.
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

// TestParseSSELineToolCallSyntheticIndexSkipsEmptyPlaceholders parses empty
// entries before three argument-bearing calls without indices. It requires
// compact positions zero through two and retains each distinct argument object.
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

// TestParseSSELineToolUseEnvelopeEventsSkipTypeOnlyPlaceholder parses a tool-use
// delta carrying only the default function type. It requires no normalized event
// so empty metadata cannot invent a call.
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

// TestParseSSELineFlatToolCall parses flat call-ID, name, and object-argument
// wire fields. It checks normalized function type and the retained identifier,
// Bash name, and argument JSON.
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

// TestParseSSELineToolUseEnvelopeEvents parses a tool-use start followed by its
// argument delta. It checks matching call identity and name plus the
// continuation's complete argument text.
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

// TestParseSSELineToolUseEnvelopeEventsPreserveIndex parses start and
// continuation envelopes carrying index two. It checks index presence on both
// and keeps the named start associated with the argument fragment.
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

// TestParseSSELineFinalMessageToolCalls parses final-message calls with a finish
// reason. It checks the invocation data and completion event order after the
// retained tool call.
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

// TestParseSSELineReasoning parses a wrapped reasoning_content fragment from a
// choice delta. It checks one reasoning_delta with the original text for
// thinking-capable protocol conversion.
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

// TestParseSSELineContentAndReasoningAreSeparate parses visible and reasoning
// content in the same choice. It requires separate text then reasoning events,
// preserving their distinct downstream roles.
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

// TestParseSSELineUsage parses primary prompt, completion, and total token
// counts from wrapped usage. It checks one observed usage event containing all
// three supplied numeric counts.
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

// TestParseSSELineUsageAcceptsAnthropicNames parses input_tokens and
// output_tokens without a total field. It checks alias selection and the derived
// total in the normalized usage observation.
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

// TestParseSSELineWrappedUsagePreservesDetails parses cache and reasoning detail
// objects with primary token totals. It checks both detail pointers, including
// an explicit zero reasoning count.
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

// TestParseSSELineUsageAcceptsNumericStringsAndFloats mixes string and floating
// token values across totals and details. It checks integer normalization of
// prompt, completion, cached, cacheable, and reasoning counts.
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

// TestParseSSELineMalformedWrapper passes a data line whose outer payload is
// invalid JSON. It requires an error before any wrapped body can reach event
// conversion.
func TestParseSSELineMalformedWrapper(t *testing.T) {
	_, err := ParseSSELine("data: not json")
	if err == nil {
		t.Error("expected error for malformed wrapper")
	}
}

// TestParseSSELineMalformedBody supplies a valid outer wrapper containing
// malformed inner JSON. It requires an error so invalid content cannot be
// treated as an empty successful event.
func TestParseSSELineMalformedBody(t *testing.T) {
	_, err := ParseSSELine(`data: {"body": "not json"}`)
	if err == nil {
		t.Error("expected error for malformed inner JSON")
	}
}

// TestParseSSELineUpstreamErrorWrapper parses HTTP 403 with vendor code 101 in
// an SSE wrapper. It checks absent events, typed APIError fields, and the
// formatted vendor message.
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
	if apiErr.StatusCode != http.StatusForbidden {
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

// TestParseSSELineUpstreamErrorWrapperUsesStatusCodeNameFallback omits numeric
// wrapper status and supplies FORBIDDEN text. It checks effective HTTP 403 and
// retained vendor code in the resulting APIError.
func TestParseSSELineUpstreamErrorWrapperUsesStatusCodeNameFallback(t *testing.T) {
	line := `data: {"headers":{"Content-Type":["application/json"]},"body":"{\"code\":\"101\",\"message\":\"Signature invalid\"}","statusCode":"FORBIDDEN"}`
	_, err := ParseSSELine(line)
	if err == nil {
		t.Fatal("expected upstream API error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("status code = %d, want 403", apiErr.StatusCode)
	}
	if apiErr.Code != "101" {
		t.Fatalf("code = %q, want 101", apiErr.Code)
	}
}

// TestParseAPIErrorBodyRedactsSensitiveFields supplies nested token and identity
// secrets inside error JSON and message text. It checks their removal from
// body/message while retaining vendor code 101.
func TestParseAPIErrorBodyRedactsSensitiveFields(t *testing.T) {
	body := `{"code":"101","message":"Authorization: Bearer secret-token securityOauthToken=dt-secret refreshToken=drt-secret cookie=session=abc uid=user-secret aid=provider-secret","data":{"securityOauthToken":"nested-secret","refreshToken":"nested-refresh","uid":"nested-user","aid":"nested-account"}}`
	apiErr := ParseAPIErrorBody(http.StatusForbidden, body)

	if strings.Contains(apiErr.Body, "secret-token") ||
		strings.Contains(apiErr.Body, "dt-secret") ||
		strings.Contains(apiErr.Body, "drt-secret") ||
		strings.Contains(apiErr.Body, "session=abc") ||
		strings.Contains(apiErr.Body, "user-secret") ||
		strings.Contains(apiErr.Body, "account-secret") ||
		strings.Contains(apiErr.Body, "nested-secret") ||
		strings.Contains(apiErr.Body, "nested-refresh") ||
		strings.Contains(apiErr.Body, "nested-user") ||
		strings.Contains(apiErr.Body, "nested-account") ||
		strings.Contains(apiErr.Message, "secret-token") ||
		strings.Contains(apiErr.Message, "dt-secret") ||
		strings.Contains(apiErr.Message, "drt-secret") ||
		strings.Contains(apiErr.Message, "session=abc") ||
		strings.Contains(apiErr.Message, "user-secret") ||
		strings.Contains(apiErr.Message, "account-secret") {
		t.Fatalf("sensitive value leaked: body=%s message=%s", apiErr.Body, apiErr.Message)
	}
	if apiErr.Code != "101" {
		t.Fatalf("code = %q, want 101", apiErr.Code)
	}
}

// TestRedactSensitiveTextPreservesQoderNumericErrorCodes covers JSON
// string/number and plain code representations with a secret token. It checks
// retained code spelling and removed secret text in every case.
func TestRedactSensitiveTextPreservesQoderNumericErrorCodes(t *testing.T) {
	tests := []struct {
		name     string // fixture case label
		input    string // error text input
		contains string // expected retained code
	}{
		{
			name:     "json string code",
			input:    `{"code":"115","message":"securityOauthToken=secret"}`,
			contains: `"code":"115"`,
		},
		{
			name:     "json numeric code",
			input:    `{"code":115,"message":"securityOauthToken=secret"}`,
			contains: `"code":115`,
		},
		{
			name:     "plain equals code",
			input:    `code=115 securityOauthToken=secret`,
			contains: `code=115`,
		},
		{
			name:     "plain colon code",
			input:    `code: 115 securityOauthToken=secret`,
			contains: `code: 115`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redacted := RedactSensitiveText(tt.input)
			if !strings.Contains(redacted, tt.contains) {
				t.Fatalf("redacted = %q, want to contain %q", redacted, tt.contains)
			}
			if strings.Contains(redacted, "secret") {
				t.Fatalf("secret leaked: %q", redacted)
			}
		})
	}
}

// TestRedactSensitiveTextRedactsCNAccessToken covers JSON and plain CN token
// fields using different delimiters. It checks that each secret disappears and a
// redaction marker remains.
func TestRedactSensitiveTextRedactsCNAccessToken(t *testing.T) {
	tests := []struct {
		name  string // fixture case label
		input string // error text input
	}{
		{
			name:  "JSON 字段",
			input: `{"token":"cn-json-secret","message":"invalid"}`,
		},
		{
			name:  "非结构化等号字段",
			input: `token=cn-plain-secret message=invalid`,
		},
		{
			name:  "非结构化冒号字段",
			input: `token: cn-colon-secret`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redacted := RedactSensitiveText(tt.input)
			if strings.Contains(redacted, "secret") {
				t.Fatalf("secret leaked: %q", redacted)
			}
			if !strings.Contains(redacted, "***") {
				t.Fatalf("redacted = %q, want redaction marker", redacted)
			}
		})
	}
}

// TestParseSSELineAgentLimitError reads a reset timestamp from JSON-valued
// vendor message text. It checks code 115, typed reset time, and the exact
// Asia/Shanghai error presentation.
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

// TestParseSSELineAgentLimitErrorNumericCode parses numeric code 115 with reset
// metadata and a secret token. It checks quota classification, retained numeric
// code text, and redacted body.
func TestParseSSELineAgentLimitErrorNumericCode(t *testing.T) {
	line := `data: {"headers":{"Content-Type":["application/json"]},"body":"{\"code\":115,\"message\":\"agent limited\",\"agentLimitResetTime\":1783841289162,\"securityOauthToken\":\"secret\"}","statusCodeValue":429,"statusCode":"TOO_MANY_REQUESTS"}`
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
	if !apiErr.IsAgentLimit() {
		t.Fatal("expected numeric code=115 to be treated as agent limit")
	}
	if apiErr.AgentLimitResetTime != 1783841289162 {
		t.Fatalf("agentLimitResetTime = %d", apiErr.AgentLimitResetTime)
	}
	if strings.Contains(apiErr.Body, "secret") {
		t.Fatalf("secret leaked in body: %s", apiErr.Body)
	}
	if !strings.Contains(apiErr.Body, `"code":115`) {
		t.Fatalf("body = %q, want numeric code preserved", apiErr.Body)
	}
}

// TestParseAPIErrorBodyAgentLimitDirectBody supplies a positive reset timestamp
// without a vendor code. It checks direct metadata extraction into APIError so
// limit classification can use reset evidence.
func TestParseAPIErrorBodyAgentLimitDirectBody(t *testing.T) {
	apiErr := ParseAPIErrorBody(429, `{"agentLimitResetTime":1783841289162}`)
	if apiErr.AgentLimitResetTime != 1783841289162 {
		t.Fatalf("agentLimitResetTime = %d", apiErr.AgentLimitResetTime)
	}
}

// TestParseAPIErrorBodyAgentLimitNumericCode parses a numeric agent-limit code
// with credential text in its error body. It checks quota classification,
// numeric-code preservation, and secret removal.
func TestParseAPIErrorBodyAgentLimitNumericCode(t *testing.T) {
	apiErr := ParseAPIErrorBody(429, `{"code":115,"message":"agent limited","securityOauthToken":"secret"}`)
	if apiErr.Code != "115" {
		t.Fatalf("code = %q, want 115", apiErr.Code)
	}
	if !apiErr.IsAgentLimit() {
		t.Fatal("expected numeric code=115 to be treated as agent limit")
	}
	if strings.Contains(apiErr.Body, "secret") || strings.Contains(apiErr.Message, "secret") {
		t.Fatalf("secret leaked: body=%s message=%s", apiErr.Body, apiErr.Message)
	}
	if !strings.Contains(apiErr.Body, `"code":115`) {
		t.Fatalf("body = %q, want numeric code preserved", apiErr.Body)
	}
}

// TestParseSSELineEmptyData consumes empty upstream heartbeat payloads without
// attempting JSON decoding. Whitespace-only data lines must yield neither
// events nor errors, leaving later business frames available for normal parsing.
func TestParseSSELineEmptyData(t *testing.T) {
	for _, line := range []string{"data:", "data: \t\r\n"} {
		events, err := ParseSSELine(line)
		if err != nil || len(events) != 0 {
			t.Fatalf("empty heartbeat produced events=%v err=%v", events, err)
		}
	}
}

// TestParseAPIErrorBodyNestedGatewayRejection retains the vendor's nested
// intention rejection code and classification. This request-specific failure
// must not be mistaken for authentication expiry or model entitlement denial.
func TestParseAPIErrorBodyNestedGatewayRejection(t *testing.T) {
	err := ParseAPIErrorBody(416, `{"error":{"code":406,"message":"","type":"intention_rejected"}}`)
	require.Equal(t, "406", err.Code)
	require.Equal(t, "intention_rejected", err.Message)
	require.False(t, MayRefreshAttempt(err))
	require.False(t, err.IsEntitlementDenied())
}
