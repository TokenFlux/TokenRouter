package httpclient

import (
	"errors"
	"net"
	"strings"
	"syscall"
)

// TransportFailure 只描述技术故障，账号摘除、重试和观察仍由调用方决定。
type TransportFailure struct {
	Persistent bool
}

// ClassifyTransportFailure 保留原 errno、DNS 和代理文本匹配的顺序与范围。
func ClassifyTransportFailure(err error) TransportFailure {
	if err == nil {
		return TransportFailure{}
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return TransportFailure{Persistent: true}
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) && dnsError.IsNotFound {
		return TransportFailure{Persistent: true}
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"authentication failed", "proxy authentication required", "connection refused",
		"no route to host", "network is unreachable", "no such host",
	} {
		if strings.Contains(message, marker) {
			return TransportFailure{Persistent: true}
		}
	}
	return TransportFailure{}
}
