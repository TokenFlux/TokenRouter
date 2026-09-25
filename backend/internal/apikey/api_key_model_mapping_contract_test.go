package apikey_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/stretchr/testify/require"
)

// 以下合同直接验证所属模块，保留原输入与断言。
func TestNormalizeAPIKeyModelMapping(t *testing.T) {
	normalized, err := apikey.NormalizeAPIKeyModelMapping(map[string]string{
		" codex-auto-review ": " gpt-5.6-luna ",
		"claude-*":            "claude-sonnet-4-6",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"codex-auto-review": "gpt-5.6-luna",
		"claude-*":          "claude-sonnet-4-6",
	}, normalized)
}

func TestNormalizeAPIKeyModelMappingRejectsInvalidRules(t *testing.T) {
	tests := map[string]map[string]string{
		"空来源":    {" ": "target"},
		"空目标":    {"source": " "},
		"自身映射":   {"source": "source"},
		"来源中间通配": {"source*-x": "target"},
		"来源多个通配": {"source**": "target"},
		"目标通配":   {"source": "target*"},
		"去空格后重复": {"source": "one", " source ": "two"},
		"来源过长":   {strings.Repeat("源", apikey.MaxAPIKeyModelNameRunes+1): "target"},
		"目标过长":   {"source": strings.Repeat("目", apikey.MaxAPIKeyModelNameRunes+1)},
	}
	for name, mapping := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := apikey.NormalizeAPIKeyModelMapping(mapping)
			require.Error(t, err)
			require.True(t, errors.Is(err, apikey.ErrInvalidAPIKeyModelMapping))
		})
	}

	tooMany := make(map[string]string, apikey.MaxAPIKeyModelMappingRules+1)
	for i := 0; i <= apikey.MaxAPIKeyModelMappingRules; i++ {
		tooMany[fmt.Sprintf("source-%d", i)] = fmt.Sprintf("target-%d", i)
	}
	_, err := apikey.NormalizeAPIKeyModelMapping(tooMany)
	require.Error(t, err)
	require.True(t, errors.Is(err, apikey.ErrInvalidAPIKeyModelMapping))
}

func TestResolveAPIKeyModelMappingPriorityAndSinglePass(t *testing.T) {
	mapping := map[string]string{
		"codex-*":           "wildcard-short",
		"codex-auto-*":      "wildcard-long",
		"codex-auto-review": "gpt-5.6-luna",
		"gpt-5.6-luna":      "must-not-chain",
		"Codex-*":           "case-sensitive",
	}

	model, matched := apikey.ResolveModelMapping(mapping, "codex-auto-review")
	require.True(t, matched)
	require.Equal(t, "gpt-5.6-luna", model)

	model, matched = apikey.ResolveModelMapping(mapping, "codex-auto-fix")
	require.True(t, matched)
	require.Equal(t, "wildcard-long", model)

	model, matched = apikey.ResolveModelMapping(mapping, "Codex-review")
	require.True(t, matched)
	require.Equal(t, "case-sensitive", model)

	model, matched = apikey.ResolveModelMapping(mapping, "CODEX-review")
	require.False(t, matched)
	require.Equal(t, "CODEX-review", model)
}

func TestAppendAPIKeyModelAliases(t *testing.T) {
	models := apikey.AppendAPIKeyModelAliases(
		[]string{"gpt-5.6-luna", "claude-sonnet-4-6", "gpt-5.6-luna"},
		map[string]string{
			"z-review": "gpt-5.6-luna",
			"a-review": "gpt-5.6-luna",
			"missing":  "not-requestable",
			"wild-*":   "gpt-5.6-luna",
		},
	)
	require.Equal(t, []string{
		"gpt-5.6-luna",
		"claude-sonnet-4-6",
		"a-review",
		"z-review",
	}, models)
}
