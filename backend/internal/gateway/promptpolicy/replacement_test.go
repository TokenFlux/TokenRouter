package promptpolicy

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestApplyCompiledUserPromptReplacementRules_DefaultEnvironmentContext
// supplies a UTC evening instant and one environment block. Built-in rules
// must select Asia/Tokyo and advance its current date to the following day.
func TestApplyCompiledUserPromptReplacementRules_DefaultEnvironmentContext(t *testing.T) {
	cfg := DefaultUserPromptReplacementConfig()
	compiled, err := compileUserPromptReplacementConfig(cfg, time.Minute)
	require.NoError(t, err)

	input := `<environment_context>
  <current_date>2026-06-30</current_date>
  <timezone>Asia/Shanghai</timezone>
</environment_context>`
	now := time.Date(2026, 6, 30, 18, 0, 0, 0, time.FixedZone("UTC", 0))

	got := applyCompiledUserPromptReplacementRules(input, compiled.rules, now)

	require.Contains(t, got, "<timezone>Asia/Tokyo</timezone>")
	require.Contains(t, got, "<current_date>2026-07-01</current_date>")
}

// TestApplyCompiledUserPromptReplacementRules_DefaultDoesNotReplaceOutsideEnvironmentContext
// supplies date and timezone markers without an enclosing environment block.
// Built-in scoped rules must retain that supplied text unchanged.
func TestApplyCompiledUserPromptReplacementRules_DefaultDoesNotReplaceOutsideEnvironmentContext(t *testing.T) {
	cfg := DefaultUserPromptReplacementConfig()
	compiled, err := compileUserPromptReplacementConfig(cfg, time.Minute)
	require.NoError(t, err)

	input := `<current_date>2026-06-30</current_date><timezone>Asia/Shanghai</timezone>`

	got := applyCompiledUserPromptReplacementRules(input, compiled.rules, time.Now())

	require.Equal(t, input, got)
}

// TestApplyCompiledUserPromptReplacementRules_DefaultDoesNotCrossEnvironmentContextBlocks
// splits a custom rule's date and timezone evidence across two complete
// blocks. Scoped application must leave the date unchanged rather than combine
// those blocks into one match.
func TestApplyCompiledUserPromptReplacementRules_DefaultDoesNotCrossEnvironmentContextBlocks(t *testing.T) {
	cfg := &UserPromptReplacementConfig{
		Enabled: true,
		Rules: []UserPromptReplacementRule{
			{
				ID:              "environment-context-scoped-marker",
				Name:            "environment_context scoped marker",
				Enabled:         true,
				Pattern:         `(?s)(<environment_context\b[^>]*>.*?<current_date>)([^<]*)(</current_date>.*?<timezone>Asia/Shanghai</timezone>.*?</environment_context>)`,
				TargetGroup:     2,
				ReplacementType: UserPromptReplacementTypeStatic,
				Scope:           UserPromptReplacementScopeEnvironmentContext,
				StaticText:      "scoped",
			},
		},
	}
	compiled, err := compileUserPromptReplacementConfig(cfg, time.Minute)
	require.NoError(t, err)

	input := `<environment_context>
  <current_date>2026-06-30</current_date>
</environment_context>
<environment_context>
  <timezone>Asia/Shanghai</timezone>
</environment_context>`

	got := applyCompiledUserPromptReplacementRules(input, compiled.rules, time.Now())

	require.Contains(t, got, "<current_date>2026-06-30</current_date>")
	require.NotContains(t, got, "<current_date>scoped</current_date>")
}

// TestApplyUserPromptReplacementMessages_DefaultHandlesLongerAndShorterJSONReplacement
// writes built-in replacements into a JSON user string containing escaped
// newlines. Decoded content must contain the Tokyo date and timezone and
// remove the earlier timezone text.
func TestApplyUserPromptReplacementMessages_DefaultHandlesLongerAndShorterJSONReplacement(t *testing.T) {
	cfg := DefaultUserPromptReplacementConfig()
	compiled, err := compileUserPromptReplacementConfig(cfg, time.Minute)
	require.NoError(t, err)

	body := []byte(`{"messages":[{"role":"user","content":"<environment_context>\n  <current_date>2026-06-30</current_date>\n  <timezone>Asia/Shanghai</timezone>\n</environment_context>"}]}`)
	got := string(applyUserPromptReplacementMessages(body, compiled.rules, time.Date(2026, 6, 30, 18, 0, 0, 0, time.UTC)))
	content := gjson.Get(got, "messages.0.content").String()

	require.Contains(t, content, `<current_date>2026-07-01</current_date>`)
	require.Contains(t, content, `<timezone>Asia/Tokyo</timezone>`)
	require.NotContains(t, content, "Asia/Shanghai")
}

// TestApplyUserPromptReplacementMessages_OnlyUserText supplies matching text
// under system, assistant, user-string, and user-block content. Only the two
// user text selections must receive the configured literal replacement.
func TestApplyUserPromptReplacementMessages_OnlyUserText(t *testing.T) {
	cfg := &UserPromptReplacementConfig{
		Enabled: true,
		Rules: []UserPromptReplacementRule{
			{
				ID:              "replace-secret",
				Name:            "replace secret",
				Enabled:         true,
				Pattern:         `(secret)`,
				TargetGroup:     1,
				ReplacementType: UserPromptReplacementTypeStatic,
				StaticText:      "public",
			},
		},
	}
	compiled, err := compileUserPromptReplacementConfig(cfg, time.Minute)
	require.NoError(t, err)

	body := []byte(`{"messages":[{"role":"system","content":"secret"},{"role":"assistant","content":"secret"},{"role":"user","content":"secret"},{"role":"user","content":[{"type":"text","text":"secret"}]}]}`)

	got := string(applyUserPromptReplacementMessages(body, compiled.rules, time.Now()))

	require.Contains(t, got, `{"role":"system","content":"secret"}`)
	require.Contains(t, got, `{"role":"assistant","content":"secret"}`)
	require.Equal(t, 2, strings.Count(got, "public"))
}

// TestApplyUserPromptReplacementMessages_MultiHitPreservesLiteralAngleBrackets
// applies default environment replacements across multiple user messages in one
// payload. All user blocks update in a single pass while retaining literal XML
// angle brackets without HTML Unicode escaping.
func TestApplyUserPromptReplacementMessages_MultiHitPreservesLiteralAngleBrackets(t *testing.T) {
	cfg := DefaultUserPromptReplacementConfig()
	compiled, err := compileUserPromptReplacementConfig(cfg, time.Minute)
	require.NoError(t, err)

	body := []byte(`{"messages":[` +
		`{"role":"user","content":"<environment_context><current_date>2026-06-30</current_date><timezone>Asia/Shanghai</timezone></environment_context>"},` +
		`{"role":"assistant","content":"ack"},` +
		`{"role":"user","content":[{"type":"text","text":"<environment_context><current_date>2026-06-30</current_date><timezone>Asia/Shanghai</timezone></environment_context>"}]}` +
		`]}`)
	out := applyUserPromptReplacementMessages(body, compiled.rules, time.Date(2026, 6, 30, 18, 0, 0, 0, time.UTC))
	require.True(t, gjson.ValidBytes(out))
	require.NotContains(t, string(out), `\u003c`)
	require.Equal(t, 2, strings.Count(string(out), "Asia/Tokyo"))
	require.Equal(t, 2, strings.Count(string(out), "2026-07-01"))
}
