// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package ip

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ipmatch"
	foundation "github.com/TokenFlux/TokenRouter/internal/server/clientip"
	gin "github.com/gin-gonic/gin"
)

// SetForwardedIPSettings 兼容旧入口；仅转发到目标实现。
func SetForwardedIPSettings(c *gin.Context, enabled bool, headers []string) {
	foundation.SetForwardedIPSettings(c, enabled, headers)
}

// SetLegacyForwardedIPTrust 兼容旧入口；仅转发到目标实现。
func SetLegacyForwardedIPTrust(c *gin.Context, enabled bool) {
	foundation.SetLegacyForwardedIPTrust(c, enabled)
}

// GetClientIP 兼容旧入口；仅转发到目标实现。
func GetClientIP(c *gin.Context) string {
	return foundation.GetClientIP(c)
}

// GetTrustedClientIP 兼容旧入口；仅转发到目标实现。
func GetTrustedClientIP(c *gin.Context) string {
	return foundation.GetTrustedClientIP(c)
}

// GetSecurityClientIP 兼容旧入口；仅转发到目标实现。
func GetSecurityClientIP(c *gin.Context, trustForwarded bool) string {
	return foundation.GetSecurityClientIP(c, trustForwarded)
}

// CheckIPRestriction 委托 Key 访问策略。
func CheckIPRestriction(clientIP string, whitelist, blacklist []string) (bool, string) {
	return apikey.CheckIPRestriction(clientIP, whitelist, blacklist)
}

// CheckIPRestrictionWithCompiledRules 委托 Key 访问策略。
func CheckIPRestrictionWithCompiledRules(clientIP string, whitelist, blacklist *CompiledIPRules) (bool, string) {
	return apikey.CheckIPRestrictionWithCompiledRules(clientIP, whitelist, blacklist)
}

// CompiledIPRules 兼容旧 ACL 调用方，纯匹配实现由 ipmatch 拥有。
type CompiledIPRules = ipmatch.CompiledIPRules

// CompileIPRules 兼容旧入口，调用纯匹配实现。
func CompileIPRules(patterns []string) *CompiledIPRules {
	return ipmatch.CompileIPRules(patterns)
}

// MatchesPattern 兼容旧入口，调用纯匹配实现。
func MatchesPattern(clientIP, pattern string) bool {
	return ipmatch.MatchesPattern(clientIP, pattern)
}

// MatchesAnyPattern 兼容旧入口，调用纯匹配实现。
func MatchesAnyPattern(clientIP string, patterns []string) bool {
	return ipmatch.MatchesAnyPattern(clientIP, patterns)
}

// ValidateIPPattern 兼容旧入口，调用纯匹配实现。
func ValidateIPPattern(pattern string) bool {
	return ipmatch.ValidateIPPattern(pattern)
}

// ValidateIPPatterns 兼容旧入口，调用纯匹配实现。
func ValidateIPPatterns(patterns []string) []string {
	return ipmatch.ValidateIPPatterns(patterns)
}
