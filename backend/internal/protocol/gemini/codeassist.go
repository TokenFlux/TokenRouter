package gemini

import (
	"bytes"
	"encoding/json"
	"strings"
)

// codeassist.go decodes Code Assist tier identities and represents project
// discovery, eligibility diagnostics, and onboarding operations.
// Code Assist discovery can return both a paid tier and a current tier, while
// tier identifiers and companion projects can arrive as either strings or
// objects containing an id field. Authorization prefers a nonempty paid
// identifier before the current tier, inspects allowed onboarding tiers when
// neither registered tier supplies an identifier, and reads ineligible-tier
// reasons and validation links when enrollment is blocked.
//
// TierInfo and LoadCodeAssistResponse normalize string and object wire shapes
// into plain string fields during JSON decoding, retaining existing receiver
// state for empty or null input and publishing updates only after decoding
// succeeds. OnboardUserResponse exposes an explicit ProjectID accessor that
// extracts completed onboarding projects from string or object results while
// leaving incomplete operations empty for GetOperation polling.

// LoadCodeAssistRequest supplies project hints and client metadata to the
// internal Code Assist discovery endpoint.
type LoadCodeAssistRequest struct {
	CloudAICompanionProject string                 `json:"cloudaicompanionProject,omitempty"` // optional caller-supplied GCP project ID
	Mode                    string                 `json:"mode,omitempty"`                    // optional discovery mode selector
	Metadata                LoadCodeAssistMetadata `json:"metadata"`                          // requesting client metadata
}

// LoadCodeAssistMetadata identifies the IDE, platform, plugin, and project hint
// requesting Code Assist discovery or enrollment.
type LoadCodeAssistMetadata struct {
	IDEType     string `json:"ideType"`               // client IDE identifier
	Platform    string `json:"platform"`              // client platform identifier
	PluginType  string `json:"pluginType"`            // client plugin identifier
	DuetProject string `json:"duetProject,omitempty"` // optional caller-supplied companion project ID
}

// TierInfo stores the tier identifier decoded from a string or object.
type TierInfo struct {
	ID   string `json:"id"`             // upstream tier identifier
	Name string `json:"name,omitempty"` // optional tier display name
}

// IneligibleTier describes an unavailable Code Assist tier and any upstream
// account-validation links required to unlock it.
type IneligibleTier struct {
	// Tier, TierID, and TierName carry alternative upstream tier identifiers:
	// newer responses populate top-level tierId and tierName, while older
	// responses nest a tier object. ResolvedTierID reads TierID before Tier.ID.
	Tier       *TierInfo `json:"tier,omitempty"`       // optional nested unavailable tier
	TierID     string    `json:"tierId,omitempty"`     // top-level unavailable tier ID
	TierName   string    `json:"tierName,omitempty"`   // top-level unavailable tier name
	ReasonCode string    `json:"reasonCode,omitempty"` // machine-readable rejection reason
	// ReasonMessage and ValidationErrorMessage carry upstream human-readable
	// rejection text; ValidationURL and ValidationLearnMoreURL carry optional
	// remediation links when ReasonCode reports VALIDATION_REQUIRED.
	ReasonMessage          string `json:"reasonMessage,omitempty"`          // human-readable rejection message
	ValidationErrorMessage string `json:"validationErrorMessage,omitempty"` // validation prompt message
	ValidationURL          string `json:"validationUrl,omitempty"`          // interactive account-validation URL
	ValidationLearnMoreURL string `json:"validationLearnMoreUrl,omitempty"` // validation documentation URL
}

// LoadCodeAssistResponse holds registered tiers, project identity, onboarding
// choices, and unavailable-tier diagnostics.
type LoadCodeAssistResponse struct {
	// CurrentTier and PaidTier identify registered tiers. GetTier prefers
	// PaidTier when its ID is nonempty, then returns CurrentTier's ID; absent
	// objects remain nil so authorization can inspect allowed onboarding tiers.
	CurrentTier *TierInfo `json:"currentTier,omitempty"` // current registered tier
	PaidTier    *TierInfo `json:"paidTier,omitempty"`    // paid registered tier
	// CloudAICompanionProject stores the discovered GCP project ID normalized
	// from either a JSON string or an object containing an id field.
	CloudAICompanionProject string `json:"cloudaicompanionProject,omitempty"`
	// AllowedTiers supplies onboarding choices when registered tiers provide
	// no ID. ExtractTierIDFromAllowedTiers selects a nonblank default, then scans
	// for the first nonblank ID if that selection remains legacy.
	AllowedTiers    []AllowedTier    `json:"allowedTiers,omitempty"`    // eligible onboarding tiers
	IneligibleTiers []IneligibleTier `json:"ineligibleTiers,omitempty"` // rejected tiers and reasons
}

// AllowedTier describes an onboarding tier advertised by the Code Assist response.
type AllowedTier struct {
	ID          string `json:"id"`                    // upstream onboarding tier ID
	Name        string `json:"name,omitempty"`        // optional tier display name
	Description string `json:"description,omitempty"` // optional tier description
	// IsDefault marks a preferred onboarding choice. ExtractTierIDFromAllowedTiers
	// considers nonblank default IDs first, then scans other IDs if the selection
	// remains LEGACY, including when a default tier itself has that literal ID.
	IsDefault bool `json:"isDefault,omitempty"`
	// UserDefinedCloudaicompanionProject and HasOnboardedPreviously preserve
	// optional upstream flags as pointers so callers can distinguish false from omitted.
	UserDefinedCloudaicompanionProject *bool `json:"userDefinedCloudaicompanionProject,omitempty"` // whether caller must supply a GCP project ID
	HasOnboardedPreviously             *bool `json:"hasOnboardedPreviously,omitempty"`             // whether the account completed onboarding earlier
}

// OnboardUserRequest supplies a selected tier, optional project ID, and client
// metadata for Code Assist enrollment.
type OnboardUserRequest struct {
	TierID string `json:"tierId"` // selected onboarding tier
	// CloudAICompanionProject supplies an explicit GCP project ID for paid or
	// standard tiers; callers omit it on free-tier to avoid upstream precondition errors.
	CloudAICompanionProject string                 `json:"cloudaicompanionProject,omitempty"`
	Metadata                LoadCodeAssistMetadata `json:"metadata"` // requesting client metadata
}

// OnboardUserResponse holds long-running operation completion state and the
// optional discovered project result.
type OnboardUserResponse struct {
	// Done, Name, and Response coordinate long-running onboarding: when Done is
	// false and Name is nonblank, callers poll GetOperation; ProjectID reads
	// Response only after Done becomes true.
	Done     bool                   `json:"done"`               // upstream completion flag
	Response *OnboardUserResultData `json:"response,omitempty"` // optional onboarding result
	Name     string                 `json:"name,omitempty"`     // upstream operation resource name
}

// OnboardUserResultData holds the polymorphic project value returned after enrollment.
type OnboardUserResultData struct {
	// CloudAICompanionProject can decode as a project string or an object
	// containing an id field; OnboardUserResponse.ProjectID normalizes both forms.
	CloudAICompanionProject any `json:"cloudaicompanionProject,omitempty"`
}

// UnmarshalJSON decodes a tier from a JSON string or an object containing id.
// Empty and null input leaves the receiver unchanged; a successfully decoded
// empty string replaces ID and clears Name, as does an object without those fields.
//
// Both accepted representations replace the receiver after successful
// decoding, so an invalid value returns its decoding error without publishing
// partial tier state. The local alias avoids invoking this decoder recursively
// for object input. Callers supply a writable receiver and retain responsibility
// for resolving paid versus current tier precedence.
func (t *TierInfo) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		// Legacy tier responses encode the identifier as a string. Decode that
		// representation before the object path so both forms expose the same ID field.
		var id string
		if err := json.Unmarshal(data, &id); err != nil {
			return err
		}
		*t = TierInfo{ID: id}
		return nil
	}
	// alias retains TierInfo's fields while bypassing its custom JSON decoder.
	type alias TierInfo
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*t = TierInfo(decoded)
	return nil
}

// ResolvedTierID returns the trimmed unavailable tier ID from TierID or the
// nested Tier object when TierID is blank.
func (t IneligibleTier) ResolvedTierID() string {
	if id := strings.TrimSpace(t.TierID); id != "" {
		return id
	}
	if t.Tier != nil {
		return strings.TrimSpace(t.Tier.ID)
	}
	return ""
}

// UnmarshalJSON decodes a Code Assist discovery response while accepting
// cloudaicompanionProject as either a string or an object containing an id field.
// Empty and null input leaves the receiver unchanged, and decoding errors leave
// existing receiver fields untouched.
//
// Upstream Code Assist deployments return cloudaicompanionProject as a plain
// string on some tiers and as {"id":"..."} on others. Normalizing both shapes
// into CloudAICompanionProject here keeps downstream discovery logic independent
// of wire-shape variations.
func (r *LoadCodeAssistResponse) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	// wireResponse mirrors LoadCodeAssistResponse while deferring polymorphic
	// project decoding until top-level JSON validation succeeds.
	type wireResponse struct {
		CurrentTier             *TierInfo        `json:"currentTier,omitempty"`
		PaidTier                *TierInfo        `json:"paidTier,omitempty"`
		CloudAICompanionProject json.RawMessage  `json:"cloudaicompanionProject,omitempty"`
		AllowedTiers            []AllowedTier    `json:"allowedTiers,omitempty"`
		IneligibleTiers         []IneligibleTier `json:"ineligibleTiers,omitempty"`
	}
	var raw wireResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	projectID, err := decodeCompanionProjectRaw(raw.CloudAICompanionProject)
	if err != nil {
		return err
	}
	*r = LoadCodeAssistResponse{
		CurrentTier:             raw.CurrentTier,
		PaidTier:                raw.PaidTier,
		CloudAICompanionProject: projectID,
		AllowedTiers:            raw.AllowedTiers,
		IneligibleTiers:         raw.IneligibleTiers,
	}
	return nil
}

// GetTier returns the preferred registered tier ID from a nonnil response.
// A paid tier with a nonempty ID wins; otherwise the current tier's ID is
// returned when that object exists, including its possible empty value.
//
// Missing registered tier objects produce an empty string for authorization's
// allowed-tier or onboarding selection. IDs are returned as supplied rather
// than trimmed, so callers apply their own blank-string policy when interpreting
// registration or persisting the selected tier.
func (r *LoadCodeAssistResponse) GetTier() string {
	if r == nil {
		return ""
	}
	if r.PaidTier != nil && r.PaidTier.ID != "" {
		return r.PaidTier.ID
	}
	if r.CurrentTier != nil {
		return r.CurrentTier.ID
	}
	return ""
}

// ProjectID extracts the trimmed companion project ID from a completed
// onboarding operation. Incomplete operations, nil responses, or missing
// project fields return an empty string so callers can continue polling or fall back.
func (r *OnboardUserResponse) ProjectID() string {
	if r == nil || !r.Done || r.Response == nil {
		return ""
	}
	switch v := r.Response.CloudAICompanionProject.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		if id, ok := v["id"].(string); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

// decodeCompanionProjectRaw decodes a JSON string or {"id":"..."} object into
// a project ID string, returning an empty string for absent or null payloads.
func decodeCompanionProjectRaw(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if raw[0] == '"' {
		// Direct string project IDs remain common across older Code Assist endpoints.
		// Unmarshal the string directly before falling back to object extraction.
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			return "", err
		}
		return id, nil
	}
	// Companion project objects wrap the identifier under "id" alongside optional metadata.
	type projectObj struct {
		ID string `json:"id"`
	}
	var obj projectObj
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", err
	}
	return obj.ID, nil
}
