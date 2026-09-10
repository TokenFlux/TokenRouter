// 本文件保留上游池代理标识的规范化规则，与普通客户端池的原始 key 分开。
package proxy

import (
	"net"
	"net/url"
	"strings"
)

func NormalizePoolKey(raw string) (string, *url.URL, error) {
	_, parsed, err := Parse(raw)
	if err != nil {
		return "", nil, err
	}
	if parsed == nil {
		return "direct", nil, nil
	}
	// 规范化：小写 scheme/host，去除路径和查询参数
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.ForceQuery = false
	if hostname := parsed.Hostname(); hostname != "" {
		port := parsed.Port()
		if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
			port = ""
		}
		hostname = strings.ToLower(hostname)
		if port != "" {
			parsed.Host = net.JoinHostPort(hostname, port)
		} else {
			parsed.Host = hostname
		}
	}
	return parsed.String(), parsed, nil
}
