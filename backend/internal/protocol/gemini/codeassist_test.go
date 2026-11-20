package gemini

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// codeassist_test.go verifies Code Assist discovery and onboarding wire shapes.
// Tests cover polymorphic string and object decoding for TierInfo and
// cloudaicompanionProject, ineligible-tier diagnostics, and request serialization.

// TestLoadCodeAssistResponse_UnmarshalProjectAndTiers decodes discovery payloads
// carrying string and object companion projects alongside ineligible tiers.
// Both wire representations must populate CloudAICompanionProject, TierInfo, and
// IneligibleTiers without losing validation URLs or tier flags.
func TestLoadCodeAssistResponse_UnmarshalProjectAndTiers(t *testing.T) {
	t.Run("string project and string tier", func(t *testing.T) {
		payload := `{
			"currentTier": "standard-tier",
			"cloudaicompanionProject": "my-gcp-project"
		}`
		var resp LoadCodeAssistResponse
		require.NoError(t, json.Unmarshal([]byte(payload), &resp))
		require.Equal(t, "my-gcp-project", resp.CloudAICompanionProject)
		require.Equal(t, "standard-tier", resp.GetTier())
	})

	t.Run("object project and ineligible tiers", func(t *testing.T) {
		payload := `{
			"paidTier": {"id": "enterprise-tier", "name": "Enterprise"},
			"cloudaicompanionProject": {"id": "enterprise-proj-123"},
			"allowedTiers": [
				{
					"id": "standard-tier",
					"isDefault": true,
					"userDefinedCloudaicompanionProject": true
				}
			],
			"ineligibleTiers": [
				{
					"reasonCode": "VALIDATION_REQUIRED",
					"reasonMessage": "Verify your account to continue.",
					"tierId": "free-tier",
					"tierName": "Gemini Code Assist",
					"validationUrl": "https://accounts.google.com/verify",
					"validationLearnMoreUrl": "https://support.google.com/gemini"
				}
			]
		}`
		var resp LoadCodeAssistResponse
		require.NoError(t, json.Unmarshal([]byte(payload), &resp))
		require.Equal(t, "enterprise-proj-123", resp.CloudAICompanionProject)
		require.Equal(t, "enterprise-tier", resp.GetTier())
		require.Len(t, resp.AllowedTiers, 1)
		require.NotNil(t, resp.AllowedTiers[0].UserDefinedCloudaicompanionProject)
		require.True(t, *resp.AllowedTiers[0].UserDefinedCloudaicompanionProject)
		require.Len(t, resp.IneligibleTiers, 1)
		require.Equal(t, "free-tier", resp.IneligibleTiers[0].ResolvedTierID())
		require.Equal(t, "https://accounts.google.com/verify", resp.IneligibleTiers[0].ValidationURL)
	})
}

// TestCodeAssistRequests_MarshalProjectHints serializes LoadCodeAssistRequest
// and OnboardUserRequest with optional project hints and duetProject metadata.
// Omitted project fields must stay absent from JSON so free-tier onboarding
// avoids sending an empty cloudaicompanionProject field.
func TestCodeAssistRequests_MarshalProjectHints(t *testing.T) {
	loadReq := LoadCodeAssistRequest{
		CloudAICompanionProject: "my-proj",
		Metadata: LoadCodeAssistMetadata{
			IDEType:     "IDE_UNSPECIFIED",
			Platform:    "PLATFORM_UNSPECIFIED",
			PluginType:  "GEMINI",
			DuetProject: "my-proj",
		},
	}
	loadRaw, err := json.Marshal(loadReq)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"cloudaicompanionProject": "my-proj",
		"metadata": {
			"ideType": "IDE_UNSPECIFIED",
			"platform": "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
			"duetProject": "my-proj"
		}
	}`, string(loadRaw))

	onboardFree := OnboardUserRequest{
		TierID: "free-tier",
		Metadata: LoadCodeAssistMetadata{
			IDEType:    "IDE_UNSPECIFIED",
			Platform:   "PLATFORM_UNSPECIFIED",
			PluginType: "GEMINI",
		},
	}
	freeRaw, err := json.Marshal(onboardFree)
	require.NoError(t, err)
	require.NotContains(t, string(freeRaw), "cloudaicompanionProject")
}

// TestOnboardUserResponse_ProjectID checks project extraction across incomplete,
// string-valued, and object-valued long-running operation payloads.
// Incomplete operations must return an empty string even if response data is present.
func TestOnboardUserResponse_ProjectID(t *testing.T) {
	pending := &OnboardUserResponse{
		Done:     false,
		Name:     "operations/onboard-1",
		Response: &OnboardUserResultData{CloudAICompanionProject: "premature-proj"},
	}
	require.Empty(t, pending.ProjectID())

	fromObj := &OnboardUserResponse{
		Done: true,
		Response: &OnboardUserResultData{
			CloudAICompanionProject: map[string]any{"id": "  managed-proj-42  "},
		},
	}
	require.Equal(t, "managed-proj-42", fromObj.ProjectID())
}
