package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

// TLS 技术投影往返保持全部身份字段及 nil/空切片，不复用调用方可变容器。
func TestEgressTLSProfileRoundTripAndIsolation(t *testing.T) {
	source := &tlsfingerprint.Profile{Name: "cache-identity", EnableGREASE: true, CipherSuites: []uint16{1}, Curves: []uint16{}, PointFormats: []uint16{2}, SignatureAlgorithms: []uint16{3}, ALPNProtocols: []string{"h2", "http/1.1"}, SupportedVersions: []uint16{4}, KeyShareGroups: []uint16{5}, PSKModes: []uint16{6}, Extensions: []uint16{7}}
	policy := egress.RequestPolicy(egress.RequestPolicyInput{TLSProfile: FromTLSProfile(source)})
	result := ToTLSProfile(policy.TLSProfile)
	require.Equal(t, source, result)
	result.CipherSuites[0] = 9
	result.Extensions[0] = 9
	require.Equal(t, uint16(1), source.CipherSuites[0])
	require.Equal(t, uint16(7), policy.TLSProfile.Extensions[0])
	require.Nil(t, FromTLSProfile(nil))
	require.Nil(t, ToTLSProfile(nil))
}

// Header 应用保留原大小写变体删除和平台 wire casing，不能改其它认证头。
func TestEgressHeadersPreserveWireCasingAndUnrelatedValues(t *testing.T) {
	headers := http.Header{"x-allowed": {"one"}, "X-Allowed": {"two"}, "AUTHORIZATION": {"credential"}}
	policy := egress.RequestPolicy(egress.RequestPolicyInput{Headers: map[string]string{"X-Allowed": "override", "Authorization": "forbidden"}})
	ApplyRequestHeaders(headers, policy, func(name string) string {
		if name == "x-allowed" {
			return "X-ALLOWED"
		}
		return name
	})
	require.Equal(t, http.Header{"X-ALLOWED": {"override"}, "AUTHORIZATION": {"credential"}}, headers)
}
