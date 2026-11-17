package routing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestResolveMarketplaceGroupsGrokModalities reproduces a text model exposed
// through text and image entries. Adapter restrictions leave their output
// intersection empty, while the marketplace must retain the text output and
// accurately describe the image entry's empty output list.
func TestResolveMarketplaceGroupsGrokModalities(t *testing.T) {
	service := ModelAttributeService{Catalog: ModelAttributeCatalog{View: testkit.OfflineView}}
	models := map[int64][]RequestableModel{1: {{ID: "alias", AttributeTargets: []modelrules.Target{
		{Platform: "grok", ProviderType: "apikey", Model: "grok-4.7", ClientProtocol: "openai_responses", UpstreamProtocol: "openai_responses", BaseURL: "https://private.example/secret"},
		{Platform: "grok", ProviderType: "apikey", Model: "grok-4.7", ClientProtocol: "openai_images_generations", UpstreamProtocol: "openai_images_generations", BaseURL: "https://private.example/secret"},
	}}}}
	result, err := service.ResolveMarketplaceGroups(context.Background(), models)
	require.NoError(t, err)
	got := result[1]["alias"]
	require.Contains(t, *got.Presentation.OutputModalities, "text")
	require.Equal(t, []string{"text"}, *got.Protocols[capability.ProtocolID("openai_responses")].OutputModalities)
	require.Empty(t, *got.Protocols[capability.ProtocolID("openai_images_generations")].OutputModalities)
	conservative, err := service.ResolveGroups(context.Background(), models)
	require.NoError(t, err)
	require.Empty(t, *conservative[1]["alias"].OutputModalities)
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private.example")
	require.NotContains(t, string(encoded), "apikey")
	require.NotContains(t, string(encoded), "base_url")
}

// TestResolveMarketplaceGroupsBatchAndProtocolUnions resolves equivalent relay
// targets and scoped overrides in multiple groups. One publication and batch
// read must serve all summaries, and protocol unions must reuse their group's
// resolved values without changing client export ceilings.
func TestResolveMarketplaceGroupsBatchAndProtocolUnions(t *testing.T) {
	no, yes := false, true
	large, small, higher := 200, 50, 500
	repo := &attributeRepoFixture{groups: map[int64]*ModelAttributeConfig{1: {Status: StatusActive, Rules: []ModelAttributeRule{{Models: []string{"final"}, Match: &ModelAttributeMatch{ClientProtocol: "openai_responses"}, Attributes: modelcatalog.Attributes{OutputLimit: &small, ToolCall: &no}}}}}}
	views, lookups := 0, 0
	service := ModelAttributeService{Repo: repo, Catalog: ModelAttributeCatalog{Lookup: func(name string) modelcatalog.Attributes {
		lookups++
		if name == "alternate" {
			return modelcatalog.Attributes{OutputLimit: &higher, ToolCall: &no}
		}
		return modelcatalog.Attributes{OutputLimit: &large, ToolCall: &yes}
	}}}
	target := modelrules.Target{Model: "final", ClientProtocol: "openai_chat_completions", UpstreamProtocol: "openai_chat_completions", BaseURL: "https://relay-a.example"}
	other := target
	other.BaseURL = "https://relay-b.example"
	response := target
	response.ClientProtocol = "openai_responses"
	alternate := target
	alternate.Model = "alternate"
	models := map[int64][]RequestableModel{1: {{ID: "alias", AttributeTargets: []modelrules.Target{target, other, target, response, alternate}}}, 2: {{ID: "final"}}, 3: {}}
	got, err := service.ResolveMarketplaceGroups(context.Background(), models)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, 1, repo.batchCalls)
	require.Equal(t, 0, repo.singleCalls)
	require.Equal(t, 4, lookups)
	require.Equal(t, 500, *got[1]["alias"].Presentation.OutputLimit)
	require.Equal(t, 500, *got[1]["alias"].Protocols[capability.ProtocolID("openai_chat_completions")].OutputLimit)
	require.True(t, *got[1]["alias"].Protocols[capability.ProtocolID("openai_chat_completions")].ToolCall)
	require.Equal(t, 50, *got[1]["alias"].Protocols[capability.ProtocolID("openai_responses")].OutputLimit)
	require.False(t, *got[1]["alias"].Protocols[capability.ProtocolID("openai_responses")].ToolCall)
	require.True(t, *got[1]["alias"].Presentation.ToolCall)
	require.Empty(t, got[2]["final"].Protocols)
	published := ModelAttributeService{Catalog: ModelAttributeCatalog{View: func() modelrules.View { views++; return testkit.OfflineView() }}}
	_, err = published.ResolveMarketplaceGroups(context.Background(), models)
	require.NoError(t, err)
	require.Equal(t, 1, views)
	repo.err = errors.New("unavailable")
	_, err = service.ResolveMarketplaceGroups(context.Background(), models)
	require.ErrorContains(t, err, "unavailable")
	empty, err := service.ResolveMarketplaceGroups(context.Background(), map[int64][]RequestableModel{1: {}})
	require.NoError(t, err)
	require.Empty(t, empty)
	require.Equal(t, 2, repo.batchCalls)
}
