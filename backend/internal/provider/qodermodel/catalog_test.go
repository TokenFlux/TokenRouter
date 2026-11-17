package qodermodel

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// TestAccountPublicationsKeepIndependentControls resolves a reused key under two
// different publications. Copying and modifying observations must preserve the
// index, and a request cache must distinguish both account and content updates.
func TestAccountPublicationsKeepIndependentControls(t *testing.T) {
	first := Model{Key: "shared", Name: "First", Alias: "readable", Reasoning: new(true), Efforts: []string{"high", "max"}, InputLimit: 1000000}
	second := Model{Key: "shared", Name: "Second", Reasoning: new(false), Efforts: []string{}, InputLimit: 200000}
	a := NewCatalog("1:v1", []Model{first})
	b := NewCatalog("2:v1", []Model{second})
	updated := NewCatalog("1:v2", []Model{second})
	target := modelrules.Target{Platform: "qoder", Model: a.RouteKey(" READABLE "), ProviderModels: a}
	view := modelrules.View{}
	result := view.Resolve(target, modelcatalog.Attributes{})
	require.Equal(t, new(1000000), result.Attributes.InputLimit)
	require.Equal(t, []string{"high", "max"}, result.Attributes.Efforts())
	observation, ok := a.Lookup("readable")
	require.True(t, ok)
	*observation.Reasoning = false
	observation.Efforts[0] = "other"
	*result.Attributes.Reasoning = false
	require.Equal(t, new(true), view.Resolve(target, modelcatalog.Attributes{}).Attributes.Reasoning)
	again, ok := a.Lookup("shared")
	require.True(t, ok)
	require.Equal(t, []string{"high", "max"}, again.Efforts)
	for _, other := range []*Catalog{b, updated} {
		alternate := target
		alternate.ProviderModels = other
		require.NotEqual(t, target.Key(), alternate.Key())
		resolution := view.Resolve(alternate, modelcatalog.Attributes{})
		require.Equal(t, new(200000), resolution.Attributes.InputLimit)
		require.Equal(t, new(false), resolution.Attributes.Reasoning)
	}
}

// TestNativeRestrictionsPreserveExplicitEmptyModalities covers allowed, excessive
// and cleared modality declarations. Effort choices are intersected with the
// actual native route even when the profile borrows another route's attributes.
func TestNativeRestrictionsPreserveExplicitEmptyModalities(t *testing.T) {
	index := NewCatalog("fixture", []Model{
		{Key: "wire", CanDisable: new(false), Efforts: []string{"high"}},
		{Key: "source", CanDisable: new(true), Efforts: []string{"low", "high", "max"}},
	})
	target := modelrules.Target{Model: "wire", ProviderModels: index}
	for _, modalities := range [][]string{{"text", "image"}, {"image"}, {}} {
		resolution := modelrules.View{}.ResolveReference(target, "source", modelcatalog.Attributes{InputModalities: &modalities})
		if len(modalities) == 2 {
			require.Equal(t, []string{"text"}, *resolution.Attributes.InputModalities)
		} else {
			require.Empty(t, *resolution.Attributes.InputModalities)
		}
		require.Equal(t, []string{"high"}, resolution.Attributes.Efforts())
		require.False(t, resolution.Attributes.HasReasoningOption("toggle"))
		require.Equal(t, "wire", resolution.Target.Model)
	}
}
