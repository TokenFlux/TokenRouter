// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

import (
	"context"
	"fmt"
	"strings"
)

// CRSProxySpec 是导入文件中的代理身份，不包含运行客户端或账号。
type CRSProxySpec struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// CRSProxyStore 保留 CRS 对活动代理的身份匹配及无探测创建路径。
type CRSProxyStore interface {
	ListActive(context.Context) ([]Proxy, error)
	Create(context.Context, *Proxy) error
}

// MatchOrCreateCRSProxy 保留原 socks 别名、活动身份复用与逐项创建，不触发管理探测。
func MatchOrCreateCRSProxy(ctx context.Context, store CRSProxyStore, enabled bool, cached *[]Proxy, src *CRSProxySpec, defaultName string) (*int64, error) {
	if !enabled || src == nil {
		return nil, nil
	}
	protocol := strings.ToLower(strings.TrimSpace(src.Protocol))
	switch protocol {
	case "socks":
		protocol = "socks5"
	case "socks5h":
		protocol = "socks5"
	}
	host := strings.TrimSpace(src.Host)
	port := src.Port
	username := strings.TrimSpace(src.Username)
	password := strings.TrimSpace(src.Password)

	if protocol == "" || host == "" || port <= 0 {
		return nil, nil
	}
	if protocol != "http" && protocol != "https" && protocol != "socks5" {
		return nil, nil
	}

	// 仅复用预先读取的活动代理。
	for _, p := range *cached {
		if strings.EqualFold(p.Protocol, protocol) &&
			p.Host == host &&
			p.Port == port &&
			p.Username == username &&
			p.Password == password {
			id := p.ID
			return &id, nil
		}
	}

	// 原 CRS 创建不启动管理端延迟探测。
	proxy := &Proxy{
		Name:     CRSDefaultProxyName(defaultName, protocol, host, port),
		Protocol: protocol,
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
		Status:   StatusActive,
	}
	if err := store.Create(ctx, proxy); err != nil {
		return nil, err
	}

	*cached = append(*cached, *proxy)
	id := proxy.ID
	return &id, nil
}
func CRSDefaultProxyName(base, protocol, host string, port int) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "crs"
	}
	return fmt.Sprintf("%s (%s://%s:%d)", base, protocol, host, port)
}
