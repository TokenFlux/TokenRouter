package qoder

import (
	"fmt"
	"net"
	"runtime"
	"strings"
)

// site.go resolves Qoder site profiles and formats machine platform and address
// metadata.
// Global and CN credentials select different authorization and Gateway endpoints
// even when the client versions match. Profile normalization starts from one
// validated site and fills omitted overrides, letting tests replace endpoints
// while retaining the site's OAuth client identity and version.

// Site names and refresh modes distinguish credential workflows; endpoint,
// version, and OAuth constants supply production defaults for each site.
const (
	// SiteGlobal and SiteCN name the supported credential sites. ParseSite selects
	// the international site for empty legacy values before endpoint and model
	// lookup.
	SiteGlobal Site = "global"
	// SiteCN is the China site.
	SiteCN Site = "cn"

	// RefreshModeCosy and RefreshModeQoderCN20 distinguish imported COSY and CN
	// OpenAPI token workflows. ParseRefreshMode defaults empty credentials to COSY
	// before refresh exchange selection.
	RefreshModeCosy = "cosy"
	// RefreshModeQoderCN20 identifies credentials containing a QoderCN20 refresh
	// token.
	RefreshModeQoderCN20 = "qodercn20"
	// RefreshModeDeviceToken selects the desktop OpenAPI refresh flow on either site.
	RefreshModeDeviceToken = "device_token"

	// Browser, OpenAPI, Center, and Gateway endpoint constants supply each site's
	// default Profile. CN uses OpenAPI and Gateway while its default Center
	// endpoint remains empty.
	GlobalDeviceAuthorizationURL = "https://qoder.com/device/selectAccounts"
	// CNDeviceAuthorizationURL is the CN browser authorization URL.
	CNDeviceAuthorizationURL = "https://qoder.cn/device/selectAccounts"
	// GlobalOpenAPIBaseURL is the global OpenAPI base URL.
	GlobalOpenAPIBaseURL = "https://openapi.qoder.sh"
	// CNOpenAPIBaseURL is the CN OpenAPI base URL.
	CNOpenAPIBaseURL = "https://openapi.qoder.com.cn"
	// GlobalCenterBaseURL is the global Center base URL.
	GlobalCenterBaseURL = "https://center.qoder.sh"
	// GlobalGatewayBaseURL is the global inference gateway URL.
	GlobalGatewayBaseURL = "https://api2.qoder.sh"
	// CNGatewayBaseURL is the CN inference gateway URL.
	CNGatewayBaseURL = "https://gateway.qoder.com.cn"
	// GlobalClientVersion and CNClientVersion supply version text for OpenAPI
	// User-Agent, machine headers, encrypted payload headers, and business metadata
	// when no profile override is supplied.
	GlobalClientVersion = "1.1.86-hotfix.5.3"
	// CNClientVersion is the COSY version sent to the CN site.
	CNClientVersion = "1.1.86-hotfix.5.3"
	// DesktopClientType and DesktopProduct identify the app workload selected by
	// both desktop distributions, independently of legacy IDE credential exchanges.
	DesktopClientType = "10"
	DesktopProduct    = "app"
	// GlobalOAuthClientID and CNOAuthClientID identify public OAuth clients in
	// their respective browser authorization URLs. Profile normalization retains
	// that identity alongside the site's token endpoints.
	GlobalOAuthClientID = "732aef47-9cf2-46a2-95fe-4cebb5d0d1fa"
	// CNOAuthClientID is the CN public OAuth client ID.
	CNOAuthClientID = "732aef47-9cf2-46a2-95fe-4cebb5d0d1fa"
	// GlobalOpenAPIProductName and CNOpenAPIProductName distinguish site labels in
	// OpenAPI User-Agent text. Profile.OpenAPIUserAgent joins the selected product
	// label with its normalized client version.
	GlobalOpenAPIProductName = "Qoder"
	// CNOpenAPIProductName is the product name in the CN OpenAPI User-Agent.
	CNOpenAPIProductName = "Qoder CN"
)

// Site identifies the credential site used to select Qoder protocol behavior.
type Site string

// Profile holds endpoint overrides and OAuth identifiers for one validated Qoder
// site.
type Profile struct {
	// Site selects defaults for DeviceAuthorizationURL, OpenAPIBaseURL,
	// CenterBaseURL, GatewayBaseURL, ClientVersion, and OAuthClientID.
	// NormalizeProfile fills omitted values from that site and applies nonblank
	// overrides together so request endpoints retain the selected client identity.
	Site                   Site   // selected credential site
	DeviceAuthorizationURL string // browser authorization endpoint
	OpenAPIBaseURL         string // OpenAPI endpoint base
	CenterBaseURL          string // Center endpoint base
	GatewayBaseURL         string // Gateway endpoint base
	ClientVersion          string // protocol client version
	OAuthClientID          string // public OAuth identifier
}

// ParseSite validates a trimmed, case-insensitive credential site and returns
// its canonical name. An empty value selects global for legacy providers, and an
// unsupported value returns an error for provider validation.
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

// ParseRefreshMode validates the trimmed, case-insensitive refresh workflow
// saved with credentials. An empty value selects COSY for legacy providers;
// unsupported modes return an error before authorization or refresh
// construction.
func ParseRefreshMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", RefreshModeCosy:
		return RefreshModeCosy, nil
	case RefreshModeQoderCN20:
		return RefreshModeQoderCN20, nil
	case RefreshModeDeviceToken:
		return RefreshModeDeviceToken, nil
	default:
		return "", fmt.Errorf("qoder: unsupported refresh_mode %q", strings.TrimSpace(value))
	}
}

// ProfileForSite returns a value containing the normalized site's protocol
// defaults. Empty site selects global and unknown sites return an error; CN has
// no Center endpoint because its flows use OpenAPI and Gateway.
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

// MustProfileForSite returns a site's profile and panics when site validation
// fails. Callers use known site constants when initialization or default
// construction requires a profile and cannot recover from an invalid site.
func MustProfileForSite(site Site) Profile {
	profile, err := ProfileForSite(site)
	if err != nil {
		panic(err)
	}
	return profile
}

// NormalizeProfile fills omitted fields from validated site defaults while
// applying supplied overrides. Nonempty endpoint overrides are trimmed and lose
// trailing slashes; version and OAuth client ID overrides are trimmed, and
// invalid sites return errors.
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

// OpenAPIUserAgent returns the site product name and normalized client version.
// An invalid profile selects global defaults, while a CN profile uses the CN
// product label required by OpenAPI calls.
func (p Profile) OpenAPIUserAgent() string {
	normalized, err := NormalizeProfile(p)
	// User-Agent construction is also used with override profiles. Invalid sites
	// use global defaults so callers still receive a complete product/version
	// value.
	if err != nil {
		normalized = MustProfileForSite(SiteGlobal)
	}
	productName := GlobalOpenAPIProductName
	if normalized.Site == SiteCN {
		productName = CNOpenAPIProductName
	}
	return productName + "/" + normalized.ClientVersion
}

// MachineOS returns the current Go architecture and operating system in COSY
// form.
func MachineOS() string {
	return MachineOSFor(runtime.GOARCH, runtime.GOOS)
}

// MachineOSFor joins normalized architecture and operating-system names for COSY
// headers. It trims and lowercases both values, maps amd64/x86_64 and
// arm64/aarch64 to protocol aliases, and passes other architecture strings
// through.
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

// MachineIP returns the first non-loopback IPv4 address for the CN client
// header. Interface enumeration failure or an address list without an eligible
// IPv4 returns empty text, allowing header construction to send an absent value.
func MachineIP() string {
	addresses, err := net.InterfaceAddrs()
	// Interface discovery is descriptive metadata. Suppress enumeration failure so
	// the request can continue with an empty client IP rather than failing
	// authentication.
	if err != nil {
		return ""
	}
	return machineIPFromAddresses(addresses)
}

// machineIPFromAddresses selects the first non-loopback IPv4 among IPNet address
// entries. Other address representations and IPv6-only entries are skipped; list
// order determines which eligible local interface becomes the client IP.
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
