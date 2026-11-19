package creative

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A selected concrete model can be validated without enumerating unrelated
// configured candidates. Counting fixtures distinguish direct model checks
// from inventory reads, and wildcard settings exercise both public listing and
// submission rejection through the same model query path.

// patternCreativeSettings supplies wildcard model names from saved settings.
type patternCreativeSettings struct {
	*targetQueryFixture        // shared query counter fixture
	model               string // configured wildcard model name
}

// GetCreativeModelSettings returns one generated allowlist entry using the
// fixture model. The returned entry targets group one and permits generation
// so wildcard rejection can be exercised by the caller.
func (s patternCreativeSettings) GetCreativeModelSettings(context.Context) []CreativeModelSetting {
	return []CreativeModelSetting{{GroupID: 1, Model: s.model, Operations: []string{CreativeOperationGenerate}}}
}

// TestCreativeRejectsPatternModels configures names containing star or
// question-mark wildcards. Listing stays empty, submission returns
// CREATIVE_INVALID_MODEL, and the fixture records neither support checks nor
// inventory reads.
func TestCreativeRejectsPatternModels(t *testing.T) {
	for _, model := range []string{"gpt-image-2*", "gpt-image-2?"} {
		t.Run(model, func(t *testing.T) {
			fixture := &targetQueryFixture{}
			service := newTargetQueryPublic(fixture)
			service.Settings = patternCreativeSettings{targetQueryFixture: fixture, model: model}
			models, err := service.ListModels(context.Background(), 1)
			require.NoError(t, err)
			require.Empty(t, models.Data)
			_, err = service.ValidateCreateParams(context.Background(), 1, &CreateCreativeRunParamsPublic{GroupID: 1, Model: model, Operation: CreativeOperationGenerate, Prompt: "test"})
			require.ErrorIs(t, err, ErrCreativeInvalidModel)
			require.Empty(t, fixture.checked)
			require.Zero(t, fixture.inventoryReads)
		})
	}
}

// TestCreativeTargetValidationDoesNotEnumerate validates one configured GPT
// Image model with counting providers. It checks the resolved model and one
// provider read while candidate inventory reads remain zero.
func TestCreativeTargetValidationDoesNotEnumerate(t *testing.T) {
	fixture := &targetQueryFixture{}
	service := newTargetQueryPublic(fixture)
	result, err := service.ValidateCreateParams(context.Background(), 1, &CreateCreativeRunParamsPublic{GroupID: 1, Model: "gpt-image-2", Operation: CreativeOperationGenerate, Prompt: "test"})
	require.NoError(t, err)
	require.Equal(t, "gpt-image-2", result.FinalModel)
	require.Equal(t, []string{"gpt-image-2"}, fixture.checked)
	require.Zero(t, fixture.inventoryReads)
	require.Equal(t, 1, fixture.providerReads)
}
