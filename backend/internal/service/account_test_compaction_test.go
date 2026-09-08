package service

import (
	"testing"
)

func TestNormalizeAccountTestMode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "", want: AccountTestModeDefault},
		{input: "default", want: AccountTestModeDefault},
		{input: " compact ", want: AccountTestModeCompact},
		{input: "COMPACT", want: AccountTestModeCompact},
		{input: " legacy_compact ", want: AccountTestModeLegacyCompact},
		{input: "unknown", want: AccountTestModeDefault},
	}

	for _, tt := range tests {
		if got := normalizeAccountTestMode(tt.input); got != tt.want {
			t.Fatalf("normalizeAccountTestMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestResolveAccountTestModeAndType(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		testTypes []string
		wantMode  string
		wantType  string
		explicit  bool
	}{
		{name: "explicit image", mode: "default", testTypes: []string{"image"}, wantMode: AccountTestModeDefault, wantType: AccountTestTypeImage, explicit: true},
		{name: "explicit text", mode: "default", testTypes: []string{"text"}, wantMode: AccountTestModeDefault, wantType: AccountTestTypeText, explicit: true},
		{name: "legacy compact", mode: "compact", wantMode: AccountTestModeCompact, wantType: "", explicit: false},
		{name: "mode alias", mode: "image", wantMode: AccountTestModeDefault, wantType: AccountTestTypeImage, explicit: true},
		{name: "swapped new call", mode: "image", testTypes: []string{"compact"}, wantMode: AccountTestModeCompact, wantType: AccountTestTypeImage, explicit: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, testType, explicit := resolveAccountTestModeAndType(tt.mode, tt.testTypes...)
			if mode != tt.wantMode || testType != tt.wantType || explicit != tt.explicit {
				t.Fatalf("resolveAccountTestModeAndType(%q, %#v) = (%q, %q, %v), want (%q, %q, %v)", tt.mode, tt.testTypes, mode, testType, explicit, tt.wantMode, tt.wantType, tt.explicit)
			}
		})
	}
}

func TestCreateOpenAICompactionTestPayload_NativeV2Shape(t *testing.T) {
	payload := createOpenAICompactionTestPayload("gpt-5.6-sol", true)
	if payload["stream"] != true || payload["store"] != false {
		t.Fatalf("OAuth V2 test payload must be streaming with store:false: %#v", payload)
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("expected message and compaction_trigger input items: %#v", payload["input"])
	}
	last, ok := input[len(input)-1].(map[string]any)
	if !ok || last["type"] != "compaction_trigger" {
		t.Fatalf("last input item must be compaction_trigger: %#v", input[len(input)-1])
	}

	legacy := createOpenAILegacyCompactionTestPayload("gpt-5.6-sol")
	legacyInput, ok := legacy["input"].([]any)
	if !ok || len(legacyInput) != 1 {
		t.Fatalf("legacy test must not inject a V2 trigger: %#v", legacyInput)
	}
}

func TestOpenAICompactionTestHasOutput(t *testing.T) {
	sse := []byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"id\":\"cmp_1\"}}\n\n")
	if !openAICompactionTestHasOutput(sse) {
		t.Fatal("SSE compaction item should mark native V2 support")
	}
	if openAICompactionTestHasOutput([]byte(`{"output":[{"type":"message"}]}`)) {
		t.Fatal("ordinary output item must not mark native V2 support")
	}
}
