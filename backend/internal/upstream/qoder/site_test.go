package qoder

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// Site fixtures pin endpoints, OAuth identities, and model controls to the
// integration's verified profile values. They compare shared and site-specific
// route snapshots, preserving published alias coverage while checking how
// unknown raw routes and missing legacy credential fields are interpreted.

// TestMachineIPFromAddressesUsesFirstNonLoopbackIPv4 supplies loopback, IPv6,
// and two eligible IPv4 addresses in order. It checks selection of the first
// usable IPv4 from that list.
func TestMachineIPFromAddressesUsesFirstNonLoopbackIPv4(t *testing.T) {
	addresses := []net.Addr{
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.ParseIP("172.18.0.1"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("192.168.5.36"), Mask: net.CIDRMask(24, 32)},
	}

	require.Equal(t, "172.18.0.1", machineIPFromAddresses(addresses))
}

// TestParseSiteAndRefreshModeCompatibility parses empty, recognized, and unknown
// credential values. It checks global/COSY legacy defaults, CN/QoderCN20
// recognition, and errors for unsupported site or mode strings.
func TestParseSiteAndRefreshModeCompatibility(t *testing.T) {
	site, err := ParseSite("")
	require.NoError(t, err)
	require.Equal(t, SiteGlobal, site)

	site, err = ParseSite("cn")
	require.NoError(t, err)
	require.Equal(t, SiteCN, site)
	_, err = ParseSite("unknown")
	require.ErrorContains(t, err, "unsupported site")

	mode, err := ParseRefreshMode("")
	require.NoError(t, err)
	require.Equal(t, RefreshModeCosy, mode)
	mode, err = ParseRefreshMode(RefreshModeQoderCN20)
	require.NoError(t, err)
	require.Equal(t, RefreshModeQoderCN20, mode)
	_, err = ParseRefreshMode("unknown")
	require.ErrorContains(t, err, "unsupported refresh_mode")
}

// TestProfileForSiteUsesFrozenProductionValues resolves both site defaults and
// their User-Agent labels. It checks endpoints, client IDs, version strings, and
// the absent CN Center endpoint.
func TestProfileForSiteUsesFrozenProductionValues(t *testing.T) {
	global, err := ProfileForSite(SiteGlobal)
	require.NoError(t, err)
	require.Equal(t, GlobalDeviceAuthorizationURL, global.DeviceAuthorizationURL)
	require.Equal(t, GlobalOpenAPIBaseURL, global.OpenAPIBaseURL)
	require.Equal(t, GlobalCenterBaseURL, global.CenterBaseURL)
	require.Equal(t, GlobalGatewayBaseURL, global.GatewayBaseURL)
	require.Equal(t, "1.1.86-hotfix.5.3", GlobalClientVersion)
	require.Equal(t, "1.1.86-hotfix.5.3", global.ClientVersion)
	require.Equal(t, GlobalOAuthClientID, global.OAuthClientID)
	require.Equal(t, "Qoder/1.1.86-hotfix.5.3", global.OpenAPIUserAgent())

	cn, err := ProfileForSite(SiteCN)
	require.NoError(t, err)
	require.Equal(t, CNDeviceAuthorizationURL, cn.DeviceAuthorizationURL)
	require.Equal(t, CNOpenAPIBaseURL, cn.OpenAPIBaseURL)
	require.Empty(t, cn.CenterBaseURL)
	require.Equal(t, CNGatewayBaseURL, cn.GatewayBaseURL)
	require.Equal(t, "1.1.86-hotfix.5.3", CNClientVersion)
	require.Equal(t, "1.1.86-hotfix.5.3", cn.ClientVersion)
	require.Equal(t, CNOAuthClientID, cn.OAuthClientID)
	require.Equal(t, "Qoder CN/1.1.86-hotfix.5.3", cn.OpenAPIUserAgent())
}

// TestNormalizeProfileAllowsTestEndpointInjection overrides CN OpenAPI and
// Gateway with local URLs ending in slashes. It checks normalized endpoints and
// retention of the site client version.
func TestNormalizeProfileAllowsTestEndpointInjection(t *testing.T) {
	profile, err := NormalizeProfile(Profile{
		Site:           SiteCN,
		OpenAPIBaseURL: "http://127.0.0.1:18080/",
		GatewayBaseURL: "http://127.0.0.1:18081/",
	})
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:18080", profile.OpenAPIBaseURL)
	require.Equal(t, "http://127.0.0.1:18081", profile.GatewayBaseURL)
	require.Equal(t, CNClientVersion, profile.ClientVersion)
}

// TestMachineOSForNormalizesArchitectureAliases supplies ARM and x86
// architecture spellings with two operating systems. It checks the COSY
// architecture aliases joined to the supplied OS names.
func TestMachineOSForNormalizesArchitectureAliases(t *testing.T) {
	require.Equal(t, "aarch64_darwin", MachineOSFor("arm64", "darwin"))
	require.Equal(t, "aarch64_linux", MachineOSFor("aarch64", "linux"))
	require.Equal(t, "x86_64_linux", MachineOSFor("amd64", "linux"))
	require.Equal(t, "x86_64_darwin", MachineOSFor("x86_64", "darwin"))
}
