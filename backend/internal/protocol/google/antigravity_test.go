package google

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// antigravity_test.go verifies Antigravity discovery wire decoding and request
// serialization for companion projects and validation-required ineligible tiers.

// TestAntigravityLoadCodeAssistResponse_UnmarshalObjectProjectAndValidationURL
// decodes an object-shaped cloudaicompanionProject alongside validation metadata.
// Callers must receive the normalized project ID string and validation URLs.
func TestAntigravityLoadCodeAssistResponse_UnmarshalObjectProjectAndValidationURL(t *testing.T) {
	payload := `{
		"cloudaicompanionProject": {"id": "ag-companion-proj"},
		"paidTier": {"id": "g1-pro-tier", "name": "Google AI Pro"},
		"ineligibleTiers": [
			{
				"reasonCode": "VALIDATION_REQUIRED",
				"reasonMessage": "Complete verification",
				"tierId": "g1-pro-tier",
				"validationUrl": "https://accounts.google.com/verify-ag"
			}
		]
	}`
	var resp AntigravityLoadCodeAssistResponse
	require.NoError(t, json.Unmarshal([]byte(payload), &resp))
	require.Equal(t, "ag-companion-proj", resp.CloudAICompanionProject)
	require.Equal(t, "g1-pro-tier", resp.GetTier())
	require.Len(t, resp.IneligibleTiers, 1)
	require.Equal(t, "https://accounts.google.com/verify-ag", resp.IneligibleTiers[0].ValidationURL)
}
