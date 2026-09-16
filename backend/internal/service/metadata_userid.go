// 兼容 metadata 入口只转交唯一平台规则。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

const NewMetadataFormatMinVersion = native.NewMetadataFormatMinVersion

func FormatMetadataUserID(deviceID, accountUUID, sessionID, uaVersion string) string {
	return native.FormatMetadataUserID(deviceID, accountUUID, sessionID, uaVersion)
}
func IsNewMetadataFormatVersion(version string) bool {
	return native.IsNewMetadataFormatVersion(version)
}
func ExtractCLIVersion(ua string) string { return native.ExtractCLIVersion(ua) }

type ParsedUserID = native.ParsedUserID

func ParseMetadataUserID(raw string) *ParsedUserID { return native.ParseMetadataUserID(raw) }
