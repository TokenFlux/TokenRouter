package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// TestAPIKeyFromService_MapsLastUsedAt converts a Key with usage time, IP,
// owner lock and live concurrency observations. It checks those response fields
// retain the supplied values through the fixture mapper.
func TestAPIKeyFromService_MapsLastUsedAt(t *testing.T) {
	lastUsed := time.Now().UTC().Truncate(time.Second)
	lastUsedIP := "203.0.113.10"
	src := &apikey.APIKey{
		ID:                 1,
		UserID:             2,
		TeamOwnerDisabled:  true,
		ForceClaude1hCache: true,
		Key:                "sk-map-last-used",
		Name:               "Mapper",
		Status:             billing.StatusActive,
		LastUsedAt:         &lastUsed,
		LastUsedIP:         &lastUsedIP,
		CurrentConcurrency: 3,
	}

	out := APIKeyFromService(src)
	require.NotNil(t, out)
	require.NotNil(t, out.LastUsedAt)
	require.WithinDuration(t, lastUsed, *out.LastUsedAt, time.Second)
	require.NotNil(t, out.LastUsedIP)
	require.Equal(t, lastUsedIP, *out.LastUsedIP)
	require.True(t, out.TeamOwnerDisabled)
	require.True(t, out.ForceClaude1hCache)
	require.Equal(t, 3, out.CurrentConcurrency)
}

// TestAPIKeyFromService_MapsNilLastUsedAt converts a credential that has no
// usage time or address. It checks the response exists while both optional
// usage observation pointers remain nil.
func TestAPIKeyFromService_MapsNilLastUsedAt(t *testing.T) {
	src := &apikey.APIKey{
		ID:     1,
		UserID: 2,
		Key:    "sk-map-last-used-nil",
		Name:   "MapperNil",
		Status: billing.StatusActive,
	}

	out := APIKeyFromService(src)
	require.NotNil(t, out)
	require.Nil(t, out.LastUsedAt)
	require.Nil(t, out.LastUsedIP)
}

// APIKeyFromService maps a copied Key into a DTO while omitting every group
// object. Nil input remains nil; the fixture delegates CopyAPIKey's documented
// sharing and the response mapper's retained pointer behavior.
func APIKeyFromService(v *apikey.APIKey) *APIKey[json.RawMessage] {
	return APIKeyFromKey(apikey.CopyAPIKey(v), func(*routing.Group) *json.RawMessage { return nil })
}
