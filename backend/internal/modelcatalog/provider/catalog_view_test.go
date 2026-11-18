package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// Captured model and price views remain available while a service publishes
// newer supplement data. Removing disk inputs and returning invalid remote
// bytes exercises their independence from subsequent I/O and failed update
// attempts.

// TestPublishedCatalogViewsTrackSupplementsAndPreserveOldData updates a
// supplement after capturing index and price views. Old views must retain their
// values, queries perform no remote reads, and an invalid update retains the
// latest published attributes and error status.
func TestPublishedCatalogViewsTrackSupplementsAndPreserveOldData(t *testing.T) {
	dir := t.TempDir()
	supplement := filepath.Join(dir, "supplement.json")
	require.NoError(t, os.WriteFile(supplement, []byte(`{}`), 0o600))
	remote := &catalogRemoteFixture{body: []byte(`{"openai":{"id":"openai","models":{"one":{"id":"one","type":"chat","capabilities":{},"name":"First","limit":{"context":1000},"cost":{"text":{"input":{"price":2},"output":{"price":3}}}}}}}`)}
	service := NewService(Options{DataDir: dir, RemoteURL: "https://models.dev/catalog.json", FallbackFile: supplement}, remote)
	require.NoError(t, service.ForceUpdate())
	oldIndex, oldStatus := service.AttributeIndex()
	oldPrices := service.PricingView()
	price := oldPrices.GetModelPricing("one")
	require.NotNil(t, price)
	price.InputCostPerToken = 99
	require.InDelta(t, 0.000002, oldPrices.GetModelPricing("one").InputCostPerToken, 1e-12)
	require.Nil(t, oldIndex.Search(modelcatalog.AttributeQuery{Search: "one", Limit: 1}).Items[0].Attributes.InputLimit)
	require.NoError(t, os.WriteFile(supplement, []byte(`{"one":{"provider":"openai","attributes":{"input_limit":100}}}`), 0o600))
	require.NoError(t, service.ForceUpdate())
	newIndex, newStatus := service.AttributeIndex()
	require.NotEqual(t, oldStatus.Version, newStatus.Version)
	require.Equal(t, 100, *newIndex.Search(modelcatalog.AttributeQuery{Search: "one", Limit: 1}).Items[0].Attributes.InputLimit)
	require.Nil(t, oldIndex.Search(modelcatalog.AttributeQuery{Search: "one", Limit: 1}).Items[0].Attributes.InputLimit)
	reads := len(remote.validators)
	require.NoError(t, os.Remove(service.catalogFilePath()))
	require.NoError(t, os.Remove(supplement))
	for range 3 {
		index, status := service.AttributeIndex()
		require.Equal(t, newStatus.Version, status.Version)
		require.NotEmpty(t, index.Search(modelcatalog.AttributeQuery{Limit: 1}).Items)
		require.NotEmpty(t, service.ModelIDs())
		require.NotNil(t, service.ModelAttributes("one").InputLimit)
		require.NotNil(t, service.PricingView().GetModelPricing("one"))
	}
	require.Len(t, remote.validators, reads)
	remote.body = []byte(`invalid`)
	require.Error(t, service.ForceUpdate())
	failedIndex, failedStatus := service.AttributeIndex()
	require.Equal(t, newStatus.Version, failedStatus.Version)
	require.NotEmpty(t, failedStatus.LastError)
	require.Equal(t, 100, *failedIndex.Search(modelcatalog.AttributeQuery{Search: "one", Limit: 1}).Items[0].Attributes.InputLimit)
	require.InDelta(t, 0.000002, oldPrices.GetModelPricing("one").InputCostPerToken, 1e-12)
}
