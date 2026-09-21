//go:build unit

package creative_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/stretchr/testify/require"
)

func TestParseCreativeModelSettingsFailsClosed(t *testing.T) {
	require.Empty(t, creative.ParseCreativeModelSettings("{broken"))
	require.Empty(t, creative.ParseCreativeModelSettings(`[{"group_id":1,"model":"image","operations":[]}]`))

	parsed := creative.ParseCreativeModelSettings(`[{
		"group_id": 7,
		"model": "gpt-image-2",
		"operations": ["edit", "generate"]
	}]`)
	require.Equal(t, []creative.CreativeModelSetting{{
		GroupID:    7,
		Model:      "gpt-image-2",
		Operations: []string{"generate", "edit"},
	}}, parsed)
}

func TestCreativeOperationsForModelIntersectsPlatformSupport(t *testing.T) {
	index := creative.CreativeModelSettingsIndex([]creative.CreativeModelSetting{{
		GroupID:    9,
		Model:      "grok-imagine",
		Operations: []string{"generate", "edit"},
	}})
	operations, configured := creative.CreativeOperationsForModel(index, 9, "grok-imagine", []string{creative.CreativeOperationGenerate})
	require.True(t, configured)
	require.Equal(t, []string{creative.CreativeOperationGenerate}, operations)

	operations, configured = creative.CreativeOperationsForModel(index, 10, "grok-imagine", []string{creative.CreativeOperationGenerate})
	require.False(t, configured)
	require.Empty(t, operations)
}

// TestNormalizeCreativeModelSettingsForSaveByPlatform 校验保存时按实际平台清理能力。
func TestNormalizeCreativeModelSettingsForSaveByPlatform(t *testing.T) {
	svc := newCreativeTestService()
	groupRepo := testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source)
	openai := newCreativeTestGroup()
	openai.ID = 13
	openai.Name = "OpenAI Image"
	openai.Platform = capability.PlatformOpenAI
	groupRepo.byID[13] = openai

	got, err := svc.NormalizeCreativeModelSettingsForSave(context.Background(), []creative.CreativeModelSetting{
		{GroupID: 12, Model: "gemini-3.1-flash-image", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationInpaint}},
		{GroupID: 13, Model: "gpt-image-2", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationInpaint}},
		{GroupID: 12, Model: "gemini-only-inpaint", Operations: []string{creative.CreativeOperationInpaint}},
		{GroupID: 999, Model: "legacy", Operations: []string{creative.CreativeOperationInpaint}},
	})
	require.NoError(t, err)
	require.Equal(t, []creative.CreativeModelSetting{
		{GroupID: 12, Model: "gemini-3.1-flash-image", Operations: []string{creative.CreativeOperationGenerate}},
		{GroupID: 13, Model: "gpt-image-2", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationInpaint}},
		{GroupID: 999, Model: "legacy", Operations: []string{creative.CreativeOperationInpaint}},
	}, got)
}

func TestCreativeModelSettingsEmptyListClosesCreativeDirectory(t *testing.T) {
	svc := newCreativeTestService()
	svc.Settings = &creativeFakeSettingReader{enabled: true, models: []creative.CreativeModelSetting{}}
	models, err := svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, models.Data)
}
