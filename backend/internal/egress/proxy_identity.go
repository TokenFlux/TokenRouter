// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

type ProxyConnectionIdentity struct {
	Protocol string
	Host     string
	Port     int
	Username string
	Password string
	Status   string
}

func ProxyConnectionIdentityFromProxy(proxyIn *Proxy) ProxyConnectionIdentity {
	return ProxyConnectionIdentity{
		Protocol: proxyIn.Protocol,
		Host:     proxyIn.Host,
		Port:     proxyIn.Port,
		Username: proxyIn.Username,
		Password: proxyIn.Password,
		Status:   proxyIn.Status,
	}
}
