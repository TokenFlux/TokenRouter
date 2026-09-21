//go:build unit

package apikey

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ipmatch"

	"github.com/stretchr/testify/require"
)

func TestCheckIPRestrictionWithCompiledRules(t *testing.T) {
	whitelist := ipmatch.CompileIPRules([]string{"10.0.0.0/8", "192.168.1.2"})
	blacklist := ipmatch.CompileIPRules([]string{"10.1.1.1"})

	allowed, reason := CheckIPRestrictionWithCompiledRules("10.2.3.4", whitelist, blacklist)
	require.True(t, allowed)
	require.Equal(t, "", reason)

	allowed, reason = CheckIPRestrictionWithCompiledRules("10.1.1.1", whitelist, blacklist)
	require.False(t, allowed)
	require.Equal(t, "access denied", reason)
}

func TestCheckIPRestrictionWithCompiledRules_InvalidWhitelistStillDenies(t *testing.T) {
	// 与旧实现保持一致：白名单有配置但全无效时，最终应拒绝访问。
	invalidWhitelist := ipmatch.CompileIPRules([]string{"not-a-valid-pattern"})
	allowed, reason := CheckIPRestrictionWithCompiledRules("8.8.8.8", invalidWhitelist, nil)
	require.False(t, allowed)
	require.Equal(t, "access denied", reason)
}
