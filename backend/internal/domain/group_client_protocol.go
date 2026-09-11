package domain

import "github.com/TokenFlux/TokenRouter/internal/routing/capability"

// SupportedGroupClientProtocols 委托新能力目录。
func SupportedGroupClientProtocols(platform string) []ProtocolID {
	return capability.SupportedGroupClientProtocols(platform)
}

// DefaultGroupClientProtocols 委托新能力目录。
func DefaultGroupClientProtocols(platform string) []ProtocolID {
	return capability.DefaultGroupClientProtocols(platform)
}

// ValidateGroupClientProtocols 委托新能力目录。
func ValidateGroupClientProtocols(platform string, protocols []ProtocolID) ([]ProtocolID, error) {
	return capability.ValidateGroupClientProtocols(platform, protocols)
}

// SetGroupClientProtocol 委托新能力目录。
func SetGroupClientProtocol(protocols []ProtocolID, target ProtocolID, enabled bool) []ProtocolID {
	return capability.SetGroupClientProtocol(protocols, target, enabled)
}

// DefaultProtocolFallbacks 委托新能力目录。
func DefaultProtocolFallbacks(platform string) map[ProtocolID]ProtocolID {
	return capability.DefaultProtocolFallbacks(platform)
}
