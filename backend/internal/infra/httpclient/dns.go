// 本文件执行解析后 IP 检查，保留原有独立解析、五秒超时和错误语义。
package httpclient

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ipmatch"
)

// ValidateResolvedIP 验证 DNS 解析后的 IP 地址是否安全
// 用于防止 DNS Rebinding 攻击：在实际 HTTP 请求时调用此函数验证解析后的 IP
func ValidateResolvedIP(host string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("dns resolution failed: %w", err)
	}

	for _, ip := range ips {
		if ipmatch.IsNonPublic(ip) {
			return fmt.Errorf("resolved ip %s is not allowed", ip.String())
		}
	}
	return nil
}
