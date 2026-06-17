package service

import "testing"

func TestResolveThinkingProtocol(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  ThinkingProtocol
	}{
		{name: "claude strict", model: "claude-sonnet-4-5", want: ThinkingProtocolAnthropicStrict},
		{name: "opus strict", model: "opus-4-5", want: ThinkingProtocolAnthropicStrict},
		{name: "deepseek passback", model: "deepseek-v4-pro", want: ThinkingProtocolPassbackRequired},
		{name: "kimi passback", model: "kimi-k2.6", want: ThinkingProtocolPassbackRequired},
		{name: "moonshot passback", model: "moonshot-v1-32k", want: ThinkingProtocolPassbackRequired},
		{name: "glm passback", model: "glm-5.1", want: ThinkingProtocolPassbackRequired},
		{name: "minimax passback", model: "MiniMax-M2.7-highspeed", want: ThinkingProtocolPassbackRequired},
		{name: "qwen thinking passback", model: "qwen3-235b-a22b-thinking-2507", want: ThinkingProtocolPassbackRequired},
		{name: "qwen non thinking unknown", model: "qwen3-32b", want: ThinkingProtocolUnknown},
		{name: "gpt unknown", model: "gpt-5.5", want: ThinkingProtocolUnknown},
		{name: "empty unknown", model: "", want: ThinkingProtocolUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveThinkingProtocol(tt.model); got != tt.want {
				t.Fatalf("ResolveThinkingProtocol(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestThinkingProtocolGuards(t *testing.T) {
	if !ShouldPreFilterThinkingBlocks("claude-sonnet-4-5") {
		t.Fatal("Anthropic strict 模型应执行预过滤")
	}
	if ShouldPreFilterThinkingBlocks("deepseek-v4-pro") {
		t.Fatal("passback-required 上游不应执行预过滤")
	}
	if ShouldRectifyThinkingSignatureError("kimi-k2.6") {
		t.Fatal("passback-required 上游不应执行签名整流 retry")
	}
	if ShouldApplyRetryFilters("gpt-5.5") {
		t.Fatal("unknown 上游不应执行 retry 变形")
	}
}
