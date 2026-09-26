//go:build unit

package routing_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestGetModelPricing(t *testing.T) {
	ch := &routing.PricingConfig{
		ModelPricing: []routing.ModelPricingEntry{
			{ID: 1, Models: []string{"claude-sonnet-4"}, BillingMode: routing.BillingModeToken, InputPrice: new(float64(3e-6))},
			{ID: 3, Models: []string{"gpt-5.1"}, BillingMode: routing.BillingModePerRequest},
		},
	}

	tests := []struct {
		name    string
		model   string
		wantID  int64
		wantNil bool
	}{
		{"exact match", "claude-sonnet-4", 1, false},
		{"case insensitive", "Claude-Sonnet-4", 1, false},
		{"not found", "gemini-3.1-pro", 0, true},
		{"wildcard pattern not matched", "claude-opus-4-20250514", 0, true},
		{"per_request model", "gpt-5.1", 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ch.GetModelPricing(tt.model)
			if tt.wantNil {
				require.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			require.Equal(t, tt.wantID, result.ID)
		})
	}
}

func TestGetModelPricing_ReturnsCopy(t *testing.T) {
	ch := &routing.PricingConfig{
		ModelPricing: []routing.ModelPricingEntry{
			{ID: 1, Models: []string{"claude-sonnet-4"}, InputPrice: new(float64(3e-6))},
		},
	}

	result := ch.GetModelPricing("claude-sonnet-4")
	require.NotNil(t, result)

	// Modify the returned copy's slice — original should be unchanged
	result.Models = append(result.Models, "hacked")

	// Original should be unchanged
	require.Equal(t, 1, len(ch.ModelPricing[0].Models))
}

func TestGetModelPricing_EmptyPricing(t *testing.T) {
	ch := &routing.PricingConfig{ModelPricing: nil}
	require.Nil(t, ch.GetModelPricing("any-model"))

	ch2 := &routing.PricingConfig{ModelPricing: []routing.ModelPricingEntry{}}
	require.Nil(t, ch2.GetModelPricing("any-model"))
}

func TestPricingConfigClone(t *testing.T) {
	original := &routing.PricingConfig{
		ID:       1,
		Name:     "test",
		GroupIDs: []int64{10, 20},
		ModelPricing: []routing.ModelPricingEntry{
			{
				ID:         100,
				Models:     []string{"model-a"},
				InputPrice: new(float64(5e-6)),
			},
		},
	}

	cloned := original.Clone()
	require.NotNil(t, cloned)
	require.Equal(t, original.ID, cloned.ID)
	require.Equal(t, original.Name, cloned.Name)

	// Modify clone slices — original should not change
	cloned.GroupIDs[0] = 999
	require.Equal(t, int64(10), original.GroupIDs[0])

	cloned.ModelPricing[0].Models[0] = "hacked"
	require.Equal(t, "model-a", original.ModelPricing[0].Models[0])
}

func TestPricingConfigClone_Nil(t *testing.T) {
	var ch *routing.PricingConfig
	require.Nil(t, ch.Clone())
}

func TestPricingConfigIsActive(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{"active", "active", true},
		{"disabled", "disabled", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &routing.PricingConfig{Status: tt.status}
			require.Equal(t, tt.want, ch.IsActive())
		})
	}
}

func TestPricingConfigClone_EdgeCases(t *testing.T) {
	t.Run("nil model mapping", func(t *testing.T) {
		original := &routing.GroupRoutingPolicy{ModelMapping: nil}
		cloned := original.Clone()
		require.Nil(t, cloned.ModelMapping)
	})

	t.Run("nil model pricing", func(t *testing.T) {
		original := &routing.PricingConfig{ID: 1, ModelPricing: nil}
		cloned := original.Clone()
		require.Nil(t, cloned.ModelPricing)
	})

	t.Run("deep copy model mapping", func(t *testing.T) {
		original := &routing.GroupRoutingPolicy{
			ModelMapping: map[string]map[string]string{
				"openai": {"gpt-4": "gpt-4-turbo"},
			},
		}
		cloned := original.Clone()

		// Modify the cloned nested map
		cloned.ModelMapping["openai"]["gpt-4"] = "hacked"

		// Original must remain unchanged
		require.Equal(t, "gpt-4-turbo", original.ModelMapping["openai"]["gpt-4"])
	})
}
