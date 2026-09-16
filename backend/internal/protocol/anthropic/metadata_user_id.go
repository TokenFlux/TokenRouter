// Claude metadata.user_id 的 wire 解析，保持两种历史格式。
package anthropic

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ParsedMetadataUserID represents the components extracted from a metadata.user_id value.
type ParsedMetadataUserID struct {
	DeviceID    string // 64-char hex (or arbitrary client id)
	AccountUUID string // may be empty
	SessionID   string // UUID
	IsNewFormat bool   // true if the original was JSON format
}

// legacyMetadataUserIDRegex matches the legacy user_id format:
//
//	user_{64hex}_account_{optional_uuid}_session_{uuid}
var legacyMetadataUserIDRegex = regexp.MustCompile(`^user_([a-fA-F0-9]{64})_account_([a-fA-F0-9-]*)_session_([a-fA-F0-9-]{36})$`)

// MetadataUserID is the JSON structure for the new metadata.user_id format.
type MetadataUserID struct {
	DeviceID    string `json:"device_id"`
	AccountUUID string `json:"account_uuid"`
	SessionID   string `json:"session_id"`
}

// ParseMetadataUserID parses a metadata.user_id string in either format.
// Returns nil if the input cannot be parsed.
func ParseMetadataUserID(raw string) *ParsedMetadataUserID {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	// Try JSON format first (starts with '{')
	if raw[0] == '{' {
		var j MetadataUserID
		if err := json.Unmarshal([]byte(raw), &j); err != nil {
			return nil
		}
		if j.DeviceID == "" || j.SessionID == "" {
			return nil
		}
		return &ParsedMetadataUserID{
			DeviceID:    j.DeviceID,
			AccountUUID: j.AccountUUID,
			SessionID:   j.SessionID,
			IsNewFormat: true,
		}
	}

	// Try legacy format
	matches := legacyMetadataUserIDRegex.FindStringSubmatch(raw)
	if matches == nil {
		return nil
	}
	return &ParsedMetadataUserID{
		DeviceID:    matches[1],
		AccountUUID: matches[2],
		SessionID:   matches[3],
		IsNewFormat: false,
	}
}
