package anthropic

import (
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
)

// NewMetadataFormatMinVersion is the minimum Claude Code version that uses
// JSON-formatted metadata.user_id instead of the legacy concatenated string.
const NewMetadataFormatMinVersion = "2.1.78"

// FormatMetadataUserID builds a metadata.user_id string in the format
// appropriate for the given CLI version. Components are the rewritten values
// (not necessarily the originals).
func FormatMetadataUserID(deviceID, accountUUID, sessionID, uaVersion string) string {
	if IsNewMetadataFormatVersion(uaVersion) {
		b, _ := json.Marshal(jsonUserID{
			DeviceID:    deviceID,
			AccountUUID: accountUUID,
			SessionID:   sessionID,
		})
		return string(b)
	}
	// Legacy format
	return "user_" + deviceID + "_account_" + accountUUID + "_session_" + sessionID
}

// IsNewMetadataFormatVersion returns true if the given CLI version uses the
// new JSON metadata.user_id format (>= 2.1.78).
func IsNewMetadataFormatVersion(version string) bool {
	if version == "" {
		return false
	}
	return clientmeta.CompareVersions(version, NewMetadataFormatMinVersion) >= 0
}

// ExtractCLIVersion extracts the Claude Code version from a User-Agent string.
// Returns "" if the UA doesn't match the expected pattern.
func ExtractCLIVersion(ua string) string { return clientmeta.ExtractClaudeCLIVersion(ua) }

// 旧入口只转交协议值，不持有第二份解析规则。
type ParsedUserID = wire.ParsedMetadataUserID
type jsonUserID = wire.MetadataUserID

func ParseMetadataUserID(raw string) *ParsedUserID { return wire.ParseMetadataUserID(raw) }
