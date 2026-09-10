// Package clientip 拥有 HTTP 请求的可信代理选择与转发头快照。
package clientip

import (
	"net"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ipmatch"

	"github.com/gin-gonic/gin"
)

const forwardedIPSettingsKey = "tokenrouter.forwarded_ip_settings"

type forwardedIPSettings struct {
	trustForwarded bool
	headers        []string
}

// SetForwardedIPSettings 为当前请求保存转发 IP 模式和自定义请求头列表的快照。
func SetForwardedIPSettings(c *gin.Context, enabled bool, headers []string) {
	if c == nil {
		return
	}
	c.Set(forwardedIPSettingsKey, forwardedIPSettings{
		trustForwarded: enabled,
		headers:        append([]string(nil), headers...),
	})
}

// SetLegacyForwardedIPTrust 记录当前请求是否由原始转发头覆盖 Gin 的可信代理链。
func SetLegacyForwardedIPTrust(c *gin.Context, enabled bool) {
	SetForwardedIPSettings(c, enabled, nil)
}

func requestForwardedIPSettings(c *gin.Context) (forwardedIPSettings, bool) {
	if c == nil {
		return forwardedIPSettings{}, false
	}
	value, ok := c.Get(forwardedIPSettingsKey)
	if !ok {
		return forwardedIPSettings{}, false
	}
	settings, ok := value.(forwardedIPSettings)
	return settings, ok
}

func requestUsesLegacyForwardedIPTrust(c *gin.Context) bool {
	settings, ok := requestForwardedIPSettings(c)
	return !ok || settings.trustForwarded
}

// GetClientIP 按可信代理加固前的转发头优先级解析客户端地址。
// 该方法用于兼容请求元数据、用量和错误日志；安全敏感调用应使用
// GetTrustedClientIP 或 GetSecurityClientIP。
func GetClientIP(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if !requestUsesLegacyForwardedIPTrust(c) {
		return GetTrustedClientIP(c)
	}

	settings, _ := requestForwardedIPSettings(c)
	customIP, customFallback := resolveCustomForwardedClientIP(c, settings.headers)
	if customIP != "" {
		return customIP
	}

	// 保留现有反向代理部署依赖的历史优先级；当 XFF 中存在公网地址时，
	// 跳过代理写入 X-Real-IP 的 Docker/Nginx 内部网桥地址。
	legacyIP, legacyFallback := resolveLegacyForwardedHeaderIP(c)
	if legacyIP != "" {
		return legacyIP
	}
	if customFallback != "" {
		return customFallback
	}
	if legacyFallback != "" {
		return legacyFallback
	}
	return ipmatch.NormalizeIP(c.ClientIP())
}

func resolveCustomForwardedClientIP(c *gin.Context, headers []string) (string, string) {
	if c == nil {
		return "", ""
	}
	var fallback string
	for _, header := range headers {
		for _, value := range c.Request.Header.Values(header) {
			for _, candidate := range strings.Split(value, ",") {
				parsed := net.ParseIP(strings.TrimSpace(candidate))
				if parsed == nil {
					continue
				}
				normalized := parsed.String()
				if isPrivateIP(normalized) {
					if fallback == "" {
						fallback = normalized
					}
					continue
				}
				return normalized, fallback
			}
		}
	}
	return "", fallback
}

func resolveLegacyForwardedHeaderIP(c *gin.Context) (string, string) {
	var fallback string
	if forwarded := normalizeValidIP(c.GetHeader("CF-Connecting-IP")); forwarded != "" {
		fallback = forwarded
		if !isPrivateIP(forwarded) {
			return forwarded, fallback
		}
	}
	if realIP := normalizeValidIP(c.GetHeader("X-Real-IP")); realIP != "" {
		if fallback == "" {
			fallback = realIP
		}
		if !isPrivateIP(realIP) {
			return realIP, fallback
		}
	}
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		for _, candidate := range ips {
			candidate = normalizeValidIP(candidate)
			if candidate != "" && !isPrivateIP(candidate) {
				return candidate, fallback
			}
		}
		if fallback == "" {
			for _, candidate := range ips {
				if candidate = normalizeValidIP(candidate); candidate != "" {
					fallback = candidate
					break
				}
			}
		}
	}
	return "", fallback
}

// GetTrustedClientIP 从 Gin 的可信代理解析链提取客户端 IP。
// 该方法依赖 gin.Engine.SetTrustedProxies 配置，不会优先直接信任原始转发头值。
// 适用于 ACL / 风控等安全敏感场景。
func GetTrustedClientIP(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return ipmatch.NormalizeIP(c.ClientIP())
}

// GetSecurityClientIP 返回安全敏感链路使用的客户端地址。
// 兼容模式开启时由原始转发头接管解析；关闭时以 Gin 的
// server.trusted_proxies 可信代理链为唯一权威来源。
// @project-doc docs/operations/edge_security.md#trusted_client_ip
func GetSecurityClientIP(c *gin.Context, trustForwarded bool) string {
	if requestSettings, ok := requestForwardedIPSettings(c); ok {
		trustForwarded = requestSettings.trustForwarded
	}
	if trustForwarded {
		return GetClientIP(c)
	}
	return GetTrustedClientIP(c)
}

// normalizeValidIP 规范化并验证代理头中的候选值，避免把 unknown、主机名等非法值传给安全服务。
func normalizeValidIP(value string) string {
	normalized := ipmatch.NormalizeIP(value)
	parsed := net.ParseIP(normalized)
	if parsed == nil {
		return ""
	}
	return parsed.String()
}

var privateNets []*net.IPNet

func init() {
	for _, cidr := range []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"::1/128",
		"fc00::/7",
	} {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("invalid CIDR: " + cidr)
		}
		privateNets = append(privateNets, block)
	}
}

func isPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, block := range privateNets {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}
