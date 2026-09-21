//go:build unit

package creative

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/stretchr/testify/require"
)

// NormalizeCreativeModelSettings 覆盖白名单字段的校验、去重与稳定排序。
func TestNormalizeCreativeModelSettings(t *testing.T) {
	settings, err := NormalizeCreativeModelSettings([]CreativeModelSetting{{
		GroupID:    12,
		Model:      " gemini-3.1-flash-image ",
		Operations: []string{"inpaint", "generate", "generate"},
	}})
	require.NoError(t, err)
	require.Equal(t, []CreativeModelSetting{{
		GroupID:    12,
		Model:      "gemini-3.1-flash-image",
		Operations: []string{"generate", "inpaint"},
	}}, settings)

	for name, input := range map[string][]CreativeModelSetting{
		"非正整数分组": {{GroupID: 0, Model: "image", Operations: []string{CreativeOperationGenerate}}},
		"空模型":    {{GroupID: 1, Model: " ", Operations: []string{CreativeOperationGenerate}}},
		"空能力":    {{GroupID: 1, Model: "image", Operations: nil}},
		"非法能力":   {{GroupID: 1, Model: "image", Operations: []string{"upscale"}}},
		"重复模型": {
			{GroupID: 1, Model: "image", Operations: []string{CreativeOperationGenerate}},
			{GroupID: 1, Model: "image", Operations: []string{CreativeOperationEdit}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeCreativeModelSettings(input)
			require.Error(t, err)
		})
	}
}

func TestSettingServiceGetCreativeModelSettings(t *testing.T) {
	repo := &creativeRuntimeSettingsFixture{values: map[string]string{
		SettingKeyCreativeModelSettings: `[{"group_id":12,"model":"gpt-image-2","operations":["generate"]}]`,
	}}
	svc := NewRuntimeSettings(repo, settings.ErrSettingNotFound)
	require.Equal(t, []CreativeModelSetting{{
		GroupID:    12,
		Model:      "gpt-image-2",
		Operations: []string{CreativeOperationGenerate},
	}}, svc.GetCreativeModelSettings(context.Background()))

	repo.values[SettingKeyCreativeModelSettings] = "not-json"
	require.Empty(t, svc.GetCreativeModelSettings(context.Background()))
}
