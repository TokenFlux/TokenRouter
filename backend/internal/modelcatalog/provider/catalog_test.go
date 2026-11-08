package provider

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// catalog_test.go verifies catalog publication, missing-field supplements, and concurrent reads.
//
// Catalog queries can run while publication replaces the retained price map.
// Readers compare related input and output rates from each returned record,
// while metadata fixtures verify that successful updates replace identities
// and rejected candidates retain the last publication.
//
// The concurrent case registers readers before alternating publications and
// joins their work before stopping the service. Its atomic failure flag records
// any mixed or missing price observed by a reader. Other cases keep canonical
// metadata fallback separate from qualified prices and use declared modalities
// instead of inferring them from model names.

// TestPricingConcurrentReadAndReload registers readers before alternating two
// price publications. Each observed price must retain its input/output
// difference, and Stop must leave the retained publication queryable.
func TestPricingConcurrentReadAndReload(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.json")
	second := filepath.Join(dir, "second.json")
	require.NoError(t, os.WriteFile(first, []byte(`{"providers":{"openai":{"models":{"model":{"cost":{"input":1000000,"output":2000000}}}}}}`), 0o600))
	require.NoError(t, os.WriteFile(second, []byte(`{"providers":{"openai":{"models":{"model":{"cost":{"input":3000000,"output":4000000}}}}}}`), 0o600))
	service := NewService(Options{DataDir: dir}, nil)
	require.NoError(t, service.publishModelsCatalog(readCatalogTestFile(t, first), time.Now(), false))
	var readers sync.WaitGroup
	var inconsistent atomic.Bool
	// Register readers before the alternating publication calls. Their atomic
	// flag records mixed or missing rates if queries run across those
	// replacements, and the fixture joins every registered reader before
	// inspecting the flag.
	for range 4 {
		readers.Go(func() {
			for range 200 {
				price := service.GetModelPricing("model")
				if price == nil || price.OutputCostPerToken-price.InputCostPerToken != 1 {
					inconsistent.Store(true)
				}
				_ = service.GetStatus()
				_ = service.Snapshot()
			}
		})
	}
	for i := range 20 {
		file := first
		if i%2 == 0 {
			file = second
		}
		require.NoError(t, service.publishModelsCatalog(readCatalogTestFile(t, file), time.Now(), false))
	}
	// Join every reader before inspecting its recorded result and stopping the
	// fixture, so the final flag check includes all queries completed by those
	// registered readers.
	readers.Wait()
	require.False(t, inconsistent.Load())
	service.Stop()
	require.NotNil(t, service.GetModelPricing("model"))
}

// TestModelAttributesCanonicalFallbackKeepsPricing supplies services sharing a
// public author identity with different prices. Bare attributes must resolve
// publicly while token prices remain unpriced until qualified, and exact service
// attributes retain their omissions.
func TestModelAttributesCanonicalFallbackKeepsPricing(t *testing.T) {
	remote := &catalogRemoteFixture{body: []byte(`{
		"models":{"openai/gpt-image-2.5-flare":{
			"modalities":{"input":["text","image"],"output":["image"]}
		}},
		"providers":{
			"google-vertex":{"models":{"gpt-image-2.5-flare":{
				"canonical_model_id":"openai/gpt-image-2.5-flare","cost":{"input":1,"output":2}
			}}},
			"ollama-cloud":{"models":{"gpt-image-2.5-flare":{
				"canonical_model_id":"openai/gpt-image-2.5-flare","cost":{"input":3,"output":4}
			}}}
		}
	}`)}
	s := NewService(Options{RemoteURL: "https://models.dev/catalog.json", DataDir: t.TempDir()}, remote)
	require.NoError(t, s.ForceUpdate())
	attributes := s.ModelAttributes("gpt-image-2.5-flare")
	require.Equal(t, []string{"text", "image"}, *attributes.InputModalities)
	require.Equal(t, []string{"image"}, *attributes.OutputModalities)
	price := s.GetModelPricing("gpt-image-2.5-flare")
	require.Equal(t, "unpriced", price.Source)
	require.True(t, price.TokenPricingAbsent)
	require.False(t, price.InputPricePresent)
	require.Nil(t, s.ModelAttributes("google-vertex/gpt-image-2.5-flare").InputModalities)
	require.InDelta(t, 1e-6, s.GetModelPricing("google-vertex/gpt-image-2.5-flare").InputCostPerToken, 1e-12)
}

// TestDirectoryReaderTracksPublishedVersion publishes another model identity
// and then submits invalid bytes. Successful updates must replace names and
// version, while rejection retains the last version and its model metadata.
func TestDirectoryReaderTracksPublishedVersion(t *testing.T) {
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture), etag: "v1"}
	service := NewService(Options{RemoteURL: "https://models.dev/catalog.json", DataDir: t.TempDir()}, remote)
	require.NoError(t, service.ForceUpdate())
	version := service.ModelVersion()
	require.Contains(t, service.ModelIDs(), "claude-test")
	require.Equal(t, "Claude", *service.ModelEntry("claude-test").Attributes.DisplayName)
	require.Nil(t, service.ModelEntry("unknown-model").Attributes.DisplayName)
	remote.mu.Lock()
	// Replace response bytes and validator together before the next
	// synchronous update reads them, keeping the fixture's downloaded identity
	// tied to that response version.
	remote.body = []byte(`{"providers":{"anthropic":{"models":{"claude-new":{"name":"New name"}}}}}`)
	remote.etag = "v2"
	remote.mu.Unlock()
	require.NoError(t, service.ForceUpdate())
	require.NotEqual(t, version, service.ModelVersion())
	require.Contains(t, service.ModelIDs(), "claude-new")
	require.NotContains(t, service.ModelIDs(), "claude-test")
	version = service.ModelVersion()
	remote.mu.Lock()
	remote.body = []byte(`invalid`)
	remote.mu.Unlock()
	require.Error(t, service.ForceUpdate())
	require.Equal(t, version, service.ModelVersion())
	require.Equal(t, "New name", *service.ModelEntry("claude-new").Attributes.DisplayName)
}

// TestOutputModalityMetadata uses explicit image and text declarations on
// arbitrarily named models. Returned modalities must follow catalog fields,
// and a record with no declaration must retain an unknown output list.
func TestOutputModalityMetadata(t *testing.T) {
	remote := &catalogRemoteFixture{body: []byte(`{"providers":{"google":{"models":{"arbitrary-output":{"name":"Image","modalities":{"output":["image"]}},"gemini-text-image-name":{"name":"Text","modalities":{"output":["text"]}},"unknown":{"name":"Unknown"}}}}}`)}
	service := NewService(Options{RemoteURL: "https://models.dev/catalog.json", DataDir: t.TempDir()}, remote)
	require.NoError(t, service.ForceUpdate())
	require.Equal(t, []string{"image"}, *service.ModelEntry("arbitrary-output").Attributes.OutputModalities)
	require.Equal(t, []string{"text"}, *service.ModelEntry("gemini-text-image-name").Attributes.OutputModalities)
	require.Nil(t, service.ModelEntry("unknown").Attributes.OutputModalities)
}

// TestImage26Supplements supplies an offline catalog without the new models.
// Both complete model identities retain official metadata and distinct image-token
// prices, while subsequent authoritative prices preserve explicit zero overrides.
func TestImage26Supplements(t *testing.T) {
	service := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, service.publishModelsCatalog([]byte(`{"providers":{"openai":{"models":{"gpt-test":{"cost":{"input":1,"output":2}}}}}}`), time.Now(), false))
	for _, model := range []string{"gpt-image-2.6-sunburst", "gpt-image-2.6-flare"} {
		price := service.GetModelPricing(model)
		require.NotNil(t, price)
		require.InDelta(t, 5e-6, price.InputCostPerToken, 1e-12)
		require.InDelta(t, 1.25e-6, price.CacheReadInputTokenCost, 1e-12)
		require.InDelta(t, 8e-6, price.InputCostPerImageToken, 1e-12)
		require.InDelta(t, 2e-6, price.CacheReadInputImageTokenCost, 1e-12)
		require.InDelta(t, 30e-6, price.OutputCostPerImageToken, 1e-12)
		attrs := service.ModelAttributes(model)
		require.NotNil(t, attrs.OutputModalities)
		require.Equal(t, []string{"image"}, *attrs.OutputModalities)
	}
	require.NoError(t, service.publishModelsCatalog([]byte(`{"providers":{"openai":{"models":{"gpt-image-2.6-sunburst":{"cost":{"input":0,"output":0}}}}}}`), time.Now(), false))
	require.Zero(t, service.GetModelPricing("gpt-image-2.6-sunburst").InputCostPerToken)
}
