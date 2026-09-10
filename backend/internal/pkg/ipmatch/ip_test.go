package ipmatch

import (
	"net"
	"testing"
)

// TestCompiledRulesPreserveInvalidPatternCount 保留 ACL 区分空白名单和全部无效白名单所需的事实。
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
