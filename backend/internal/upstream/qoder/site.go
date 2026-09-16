package qoder

import (
	"fmt"
	"net"
	"runtime"
	"strings"
)

// Site 表示 Qoder 账号所属站点。
type Site string

const (
	// SiteGlobal 表示 Qoder 国际站，也是旧账号的兼容默认值。
	SiteGlobal Site = "global"
	// SiteCN 表示 Qoder 国内站。
	SiteCN Site = "cn"
)

const (
	// RefreshModeCosy 表示凭据持有最终 COSY 身份。
	RefreshModeCosy = "cosy"
	// RefreshModeQoderCN20 表示凭据持有 QoderCN20 refresh token。
	RefreshModeQoderCN20 = "qodercn20"
)

const (
	// GlobalDeviceAuthorizationURL 是国际站浏览器授权地址。
	GlobalDeviceAuthorizationURL = "https://qoder.com/device/selectAccounts"
	// CNDeviceAuthorizationURL 是国内站浏览器授权地址。
	CNDeviceAuthorizationURL = "https://qoder.com.cn/device/selectAccounts"
	// GlobalOpenAPIBaseURL 是国际站 OpenAPI 地址。
	GlobalOpenAPIBaseURL = "https://openapi.qoder.sh"
	// CNOpenAPIBaseURL 是国内站 OpenAPI 地址。
	CNOpenAPIBaseURL = "https://openapi.qoder.com.cn"
	// GlobalCenterBaseURL 是国际站 Center 地址。
	GlobalCenterBaseURL = "https://center.qoder.sh"
	// GlobalGatewayBaseURL 是国际站推理地址。
	GlobalGatewayBaseURL = "https://api1.qoder.sh"
	// CNGatewayBaseURL 是国内站推理地址。
	CNGatewayBaseURL = "https://gateway.qoder.com.cn"
	// GlobalClientVersion 是国际站当前 COSY 客户端版本。
	GlobalClientVersion = "1.24.2"
	// CNClientVersion 是国内站当前 COSY 客户端版本。
	CNClientVersion = "1.24.2"
	// GlobalOAuthClientID 是国际站公开 OAuth client ID。
	GlobalOAuthClientID = "e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb"
	// CNOAuthClientID 是国内站公开 OAuth client ID。
	CNOAuthClientID = "f5a7f67c-11a8-491e-8b8e-a07f2d0df4b7"
	// GlobalOpenAPIProductName 是国际站客户端在 OpenAPI UA 中使用的产品名。
	GlobalOpenAPIProductName = "Qoder"
	// CNOpenAPIProductName 是国内站客户端在 OpenAPI UA 中使用的产品名。
	CNOpenAPIProductName = "Qoder CN"
)

// Profile 集中保存一个 Qoder 站点使用的协议端点和客户端标识。
// 测试可以复制该结构并覆盖端点，生产调用只使用 ProfileForSite 返回的默认值。
type Profile struct {
	Site                   Site
	DeviceAuthorizationURL string
	OpenAPIBaseURL         string
	CenterBaseURL          string
	GatewayBaseURL         string
	ClientVersion          string
	OAuthClientID          string
}

// ParseSite 严格解析站点；空值按国际站处理以兼容旧账号。
func ParseSite(value string) (Site, error) {
	switch Site(strings.ToLower(strings.TrimSpace(value))) {
	case "", SiteGlobal:
		return SiteGlobal, nil
	case SiteCN:
		return SiteCN, nil
	default:
		return "", fmt.Errorf("qoder: unsupported site %q", strings.TrimSpace(value))
	}
}

// ParseRefreshMode 严格解析刷新模式；空值按旧式 COSY 刷新处理。
func ParseRefreshMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", RefreshModeCosy:
		return RefreshModeCosy, nil
	case RefreshModeQoderCN20:
		return RefreshModeQoderCN20, nil
	default:
		return "", fmt.Errorf("qoder: unsupported refresh_mode %q", strings.TrimSpace(value))
	}
}

// ProfileForSite 返回指定站点的只读默认 profile。
func ProfileForSite(site Site) (Profile, error) {
	parsed, err := ParseSite(string(site))
	if err != nil {
		return Profile{}, err
	}
	switch parsed {
	case SiteCN:
		return Profile{
			Site:                   SiteCN,
			DeviceAuthorizationURL: CNDeviceAuthorizationURL,
			OpenAPIBaseURL:         CNOpenAPIBaseURL,
			GatewayBaseURL:         CNGatewayBaseURL,
			ClientVersion:          CNClientVersion,
			OAuthClientID:          CNOAuthClientID,
		}, nil
	default:
		return Profile{
			Site:                   SiteGlobal,
			DeviceAuthorizationURL: GlobalDeviceAuthorizationURL,
			OpenAPIBaseURL:         GlobalOpenAPIBaseURL,
			CenterBaseURL:          GlobalCenterBaseURL,
			GatewayBaseURL:         GlobalGatewayBaseURL,
			ClientVersion:          GlobalClientVersion,
			OAuthClientID:          GlobalOAuthClientID,
		}, nil
	}
}

// MustProfileForSite 返回站点 profile，传入非法站点时 panic，仅供常量化内部调用。
func MustProfileForSite(site Site) Profile {
	profile, err := ProfileForSite(site)
	if err != nil {
		panic(err)
	}
	return profile
}

// NormalizeProfile 补全测试 profile 中未覆盖的字段，并校验站点。
func NormalizeProfile(profile Profile) (Profile, error) {
	base, err := ProfileForSite(profile.Site)
	if err != nil {
		return Profile{}, err
	}
	if strings.TrimSpace(profile.DeviceAuthorizationURL) != "" {
		base.DeviceAuthorizationURL = strings.TrimRight(strings.TrimSpace(profile.DeviceAuthorizationURL), "/")
	}
	if strings.TrimSpace(profile.OpenAPIBaseURL) != "" {
		base.OpenAPIBaseURL = strings.TrimRight(strings.TrimSpace(profile.OpenAPIBaseURL), "/")
	}
	if strings.TrimSpace(profile.CenterBaseURL) != "" {
		base.CenterBaseURL = strings.TrimRight(strings.TrimSpace(profile.CenterBaseURL), "/")
	}
	if strings.TrimSpace(profile.GatewayBaseURL) != "" {
		base.GatewayBaseURL = strings.TrimRight(strings.TrimSpace(profile.GatewayBaseURL), "/")
	}
	if strings.TrimSpace(profile.ClientVersion) != "" {
		base.ClientVersion = strings.TrimSpace(profile.ClientVersion)
	}
	if strings.TrimSpace(profile.OAuthClientID) != "" {
		base.OAuthClientID = strings.TrimSpace(profile.OAuthClientID)
	}
	return base, nil
}

// OpenAPIUserAgent 返回官方站点客户端用于 OpenAPI 请求的 User-Agent。
func (p Profile) OpenAPIUserAgent() string {
	normalized, err := NormalizeProfile(p)
	if err != nil {
		normalized = MustProfileForSite(SiteGlobal)
	}
	productName := GlobalOpenAPIProductName
	if normalized.Site == SiteCN {
		productName = CNOpenAPIProductName
	}
	return productName + "/" + normalized.ClientVersion
}

// MachineOS 返回 COSY 协议使用的“架构_系统”标识。
func MachineOS() string {
	return MachineOSFor(runtime.GOARCH, runtime.GOOS)
}

// MachineOSFor 将 Go 平台名称规范化为 Qoder 协议名称。
func MachineOSFor(arch, goos string) string {
	arch = strings.ToLower(strings.TrimSpace(arch))
	goos = strings.ToLower(strings.TrimSpace(goos))
	switch arch {
	case "amd64", "x86_64":
		arch = "x86_64"
	case "arm64", "aarch64":
		arch = "aarch64"
	}
	return arch + "_" + goos
}

// MachineIP 返回官方客户端用于 Cosy-ClientIp 的首个非回环 IPv4 地址。
func MachineIP() string {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	return machineIPFromAddresses(addresses)
}

func machineIPFromAddresses(addresses []net.Addr) string {
	for _, address := range addresses {
		ipNet, ok := address.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if ipv4 := ipNet.IP.To4(); ipv4 != nil {
			return ipv4.String()
		}
	}
	return ""
}
