package openai

import (
	"strings"
	"testing"
)

// firstLine returns the first line of trimmed embedded prompt text. Missing
// newlines return the complete trimmed value, keeping failure output short for
// both one-line and multiline fixtures.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestCodexBaseInstructionsForModel checks Codex-containing names, dedicated
// version prefixes, casing, whitespace, and unknown models. Every case must
// return nonblank instructions with the expected embedded prompt heading.
func TestCodexBaseInstructionsForModel(t *testing.T) {
	cases := []struct {
		model    string // requested model name fixture
		wantHead string // expected embedded prompt prefix
	}{
		{"gpt-5-codex", "You are Codex, based on GPT-5"},
		{"gpt-5.3-codex", "You are Codex, based on GPT-5"},
		{"gpt-5.3-codex-spark", "You are Codex, based on GPT-5"},
		{"gpt-5.1-codex-max", "You are Codex, based on GPT-5"},
		{"gpt-5.2-codex", "You are Codex, based on GPT-5"},
		{"gpt-5.5", "You are Codex, a coding agent based on GPT-5"},
		{" GPT-5.5 ", "You are Codex, a coding agent based on GPT-5"},
		{"gpt-5.2", "You are GPT-5.2 running in the Codex CLI"},
		{"gpt-5.1", "You are GPT-5.1 running in the Codex CLI"},
		{"gpt-5", "You are Codex, a coding agent based on GPT-5"},   // Use the latest embedded prompt (GPT-5.5).
		{"gpt-5.4", "You are Codex, a coding agent based on GPT-5"}, // No dedicated prompt.
		{"gpt-5.3", "You are Codex, a coding agent based on GPT-5"}, // No dedicated prompt.
		{"some-unknown-model", "You are Codex, a coding agent based on GPT-5"},
		{"", "You are Codex, a coding agent based on GPT-5"}, // Unknown models use the latest embedded prompt.
	}
	for _, c := range cases {
		got := strings.TrimSpace(CodexBaseInstructionsForModel(c.model))
		if got == "" {
			t.Errorf("model %q: got empty instructions", c.model)
			continue
		}
		if !strings.HasPrefix(got, c.wantHead) {
			t.Errorf("model %q: got prefix %q, want %q", c.model, firstLine(got), c.wantHead)
		}
	}
}

// TestGPT6CodexInstructions exercises the OAuth transform with GPT-6 model
// names. It checks that aliases receive the intended prompt, unsupported
// variants retain the fallback, and explicit caller instructions survive.
func TestGPT6CodexInstructions(t *testing.T) {
	cases := []struct {
		model string // model name sent upstream
		want  string // expected instruction text
	}{
		{"gpt-6", instructionsGPT6Astra},
		{"gpt-6-astra", instructionsGPT6Astra},
		{" OpenAI/GPT-6-ASTRA-2026-09-01 ", instructionsGPT6Astra},
		{"gpt-6.1-sol", instructionsGPT61Sol},
		{"openai/gpt-6.1-sol-high", instructionsGPT61Sol},
		{"openai/gpt-6.1_sol-openai-compact", instructionsGPT61Sol},
		{"gpt-6-astral", instructionsGPT55},
		{"gpt-6-sol", instructionsGPT55},
		{"gpt-6.1-terra", instructionsGPT61Terra},
		{" OpenAI/GPT-6.1-TERRA-HIGH ", instructionsGPT61Terra},
		{"gpt-6.1-terra-minimal", instructionsGPT61Terra},
		{"gpt-6.1-terra-openai-compact", instructionsGPT61Terra},
		{"gpt-6.1-terra-preview", instructionsGPT55},
		{"gpt-6.1-sol-preview", instructionsGPT55},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			body := map[string]any{"model": tc.model, "input": "hello"}
			result := ApplyCodexOAuthTransformWithOptions(body, CodexOAuthTransformOptions{})
			if result.Error != nil {
				t.Fatal(result.Error)
			}
			if got := body["instructions"]; got != strings.TrimSpace(tc.want) || got == "" {
				t.Fatal("OAuth transform did not insert the expected model instructions")
			}

			body["instructions"] = " Client instructions. "
			result = ApplyCodexOAuthTransformWithOptions(body, CodexOAuthTransformOptions{})
			if result.Error != nil {
				t.Fatal(result.Error)
			}
			if got := body["instructions"]; got != " Client instructions. " {
				t.Fatal("OAuth transform changed explicit instructions")
			}
		})
	}
}
