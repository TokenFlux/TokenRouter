package service

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

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

func TestApplyCompiledUserPromptReplacementRules_DefaultDoesNotReplaceOutsideEnvironmentContext(t *testing.T) {
	cfg := DefaultUserPromptReplacementConfig()
	compiled, err := compileUserPromptReplacementConfig(cfg, time.Minute)
	require.NoError(t, err)

	input := `<current_date>2026-06-30</current_date><timezone>Asia/Shanghai</timezone>`

	got := applyCompiledUserPromptReplacementRules(input, compiled.rules, time.Now())

	require.Equal(t, input, got)
}

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
