package qoder

import (
	"testing"
)

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

func TestParseSSELineReasoning(t *testing.T) {
	line := `data: {"body": "{\"choices\":[{\"delta\":{\"reasoning_content\":\"Let me think...\"}}]}"}`
	events, err := ParseSSELine(line)
	if err != nil {
		t.Fatalf("ParseSSELine: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	if events[0].Text != "Let me think..." {
		t.Errorf("event text = %q, want Let me think...", events[0].Text)
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

func TestLocalAuthInfoToIdentity(t *testing.T) {
	info := &AuthInfo{
		Name:              "test user",
		UID:               "uid123",
		SecurityOauthToken: "dt-token",
		RefreshToken:      "drt-refresh",
		UserType:          "personal_standard",
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
