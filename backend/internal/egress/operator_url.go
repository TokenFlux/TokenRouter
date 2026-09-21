package egress

// OperatorURLPolicy 保留运营方自定义出站地址的白名单和格式规则，不包含平台默认地址。
type OperatorURLPolicy struct {
	Enabled           bool
	AllowInsecureHTTP bool
	AllowPrivateHosts bool
	UpstreamHosts     []string
}

func (p OperatorURLPolicy) Validate(raw string) (string, error) {
	if !p.Enabled {
		return ValidateURLFormat(raw, p.AllowInsecureHTTP)
	}
	return ValidateHTTPSURL(raw, ValidationOptions{AllowedHosts: p.UpstreamHosts, RequireAllowlist: true, AllowPrivate: p.AllowPrivateHosts})
}
