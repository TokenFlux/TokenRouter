package openai

import (
	"testing"
)

func TestCreateOpenAICompactionTestPayload_NativeV2Shape(t *testing.T) {
	payload := CompactionTestPayload("gpt-5.6-sol", true)
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

	legacy := LegacyCompactionTestPayload("gpt-5.6-sol")
	legacyInput, ok := legacy["input"].([]any)
	if !ok || len(legacyInput) != 1 {
		t.Fatalf("legacy test must not inject a V2 trigger: %#v", legacyInput)
	}
}

func TestOpenAICompactionTestHasOutput(t *testing.T) {
	sse := []byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"id\":\"cmp_1\"}}\n\n")
	if !CompactionTestHasOutput(sse) {
		t.Fatal("SSE compaction item should mark native V2 support")
	}
	if CompactionTestHasOutput([]byte(`{"output":[{"type":"message"}]}`)) {
		t.Fatal("ordinary output item must not mark native V2 support")
	}
}
