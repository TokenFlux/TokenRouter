// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package ip

import (
	"net"

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

// CheckIPRestriction 检查 IP 是否被 API Key 的 IP 限制允许。
// 返回值：(是否允许, 拒绝原因)
// 逻辑：
// 1. 先检查黑名单，如果在黑名单中则直接拒绝
// 2. 如果白名单不为空，IP 必须在白名单中
// 3. 如果白名单为空，允许访问（除非被黑名单拒绝）
func CheckIPRestriction(clientIP string, whitelist, blacklist []string) (bool, string) {
	return CheckIPRestrictionWithCompiledRules(
		clientIP,
		CompileIPRules(whitelist),
		CompileIPRules(blacklist),
	)
}

// CheckIPRestrictionWithCompiledRules 使用预编译规则检查 IP 是否允许访问。
func CheckIPRestrictionWithCompiledRules(clientIP string, whitelist, blacklist *CompiledIPRules) (bool, string) {
	// 规范化 IP
	clientIP = normalizeIP(clientIP)
	if clientIP == "" {
		return false, "access denied"
	}
	parsedIP := net.ParseIP(clientIP)
	if parsedIP == nil {
		return false, "access denied"
	}

	// 1. 检查黑名单
	if blacklist != nil && blacklist.PatternCount > 0 && matchesCompiledRules(parsedIP, blacklist) {
		return false, "access denied"
	}

	// 2. 检查白名单（如果设置了白名单，IP 必须在其中）
	if whitelist != nil && whitelist.PatternCount > 0 && !matchesCompiledRules(parsedIP, whitelist) {
		return false, "access denied"
	}

	return true, ""
}

// CompiledIPRules 兼容旧 ACL 调用方，纯匹配实现由 ipmatch 拥有。
type CompiledIPRules = ipmatch.CompiledIPRules

// CompileIPRules 兼容旧入口，调用纯匹配实现。
func CompileIPRules(patterns []string) *CompiledIPRules {
	return ipmatch.CompileIPRules(patterns)
}

// matchesCompiledRules 兼容旧入口，调用纯匹配实现。
func matchesCompiledRules(ip net.IP, rules *CompiledIPRules) bool {
	return ipmatch.MatchesCompiledRules(ip, rules)
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

// normalizeIP 兼容 ACL 中的地址规范化，纯实现归 ipmatch。
func normalizeIP(value string) string {
	return ipmatch.NormalizeIP(value)
}
