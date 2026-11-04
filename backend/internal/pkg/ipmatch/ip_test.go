package ipmatch

import (
	"net"
	"testing"
)

// TestCompiledRulesPreserveInvalidPatternCount compiles invalid, trimmed IPv4,
// and IPv6 CIDR patterns. Invalid entries must retain their supplied count,
// compiled entries must match, and the single-pattern helper must reject
// whitespace.
func TestCompiledRulesPreserveInvalidPatternCount(t *testing.T) {
	rules := CompileIPRules([]string{"invalid", ""})
	if rules.PatternCount != 2 || MatchesCompiledRules(net.ParseIP("203.0.113.2"), rules) {
		t.Fatal("invalid patterns must remain present without matching an address")
	}
	rules = CompileIPRules([]string{"2001:db8::/32", " 203.0.113.2 "})
	if !MatchesCompiledRules(net.ParseIP("2001:db8::1"), rules) || !MatchesCompiledRules(net.ParseIP("203.0.113.2"), rules) {
		t.Fatal("compiled IPv4/IPv6 rules must match")
	}
	if MatchesPattern("203.0.113.2", " 203.0.113.2 ") {
		t.Fatal("single-pattern helper must retain its existing strict input behavior")
	}
}

// TestIsNonPublicClassifiesEgressTargets checks addresses an egress check must
// refuse, including reserved and IPv4-embedding ranges, against ordinary public
// addresses. Misclassifying either side would allow SSRF or break real upstreams.
func TestIsNonPublicClassifiesEgressTargets(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1": true, "10.0.0.1": true, "169.254.169.254": true, "0.0.0.0": true,
		"100.64.0.1": true, "192.0.0.1": true, "198.18.0.1": true, "240.0.0.1": true, "224.0.0.1": true,
		"::1": true, "fd00::1": true, "::ffff:10.0.0.1": true, "ff02::1": true,
		"64:ff9b::a00:1": true, "64:ff9b:1::1": true, "2001::1": true, "2002:a00:1::": true,
		"8.8.8.8": false, "100.128.0.1": false, "198.20.0.1": false,
		"2606:4700:4700::1111": false, "2001:4860:4860::8888": false,
	}
	for address, want := range cases {
		if got := IsNonPublic(net.ParseIP(address)); got != want {
			t.Errorf("IsNonPublic(%s) = %v, want %v", address, got, want)
		}
	}
}
