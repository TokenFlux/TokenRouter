package anthropic

import (
	"strings"
	"testing"
)

// TestIsSupportedCLIVersion covers pinned, newer, older, shortened, suffixed,
// and malformed version values. Complete releases at or above the pin pass,
// including large numeric releases that this validator does not bound.
func TestIsSupportedCLIVersion(t *testing.T) {
	cases := []struct {
		name    string // case display name
		version string // candidate CLI version
		want    bool   // expected comparison value
	}{
		{"内置基线本身", CLICurrentVersion, true},
		{"高于基线的补丁位", "2.1.251", true},
		{"高于基线的次版本", "2.2.0", true},
		{"高于基线的主版本", "3.0.0", true},
		{"低于基线", "2.1.219", false},
		{"远低于基线", "1.0.0", false},
		{"空值", "", false},
		{"只有两段", "2.2", false},
		{"带 v 前缀", "v2.2.0", false},
		{"预发布后缀", "2.2.0-local", false},
		{"构建元数据", "2.2.0+build1", false},
		{"哨兵版本带后缀", "999.0.0-local", false},
		{"纯数字哨兵仍然合法（形态没问题，运维自负）", "999.0.0", true},
		{"非数字", "abc", false},
		{"多余的段", "2.2.0.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSupportedCLIVersion(tc.version); got != tc.want {
				t.Fatalf("IsSupportedCLIVersion(%q) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}

// TestResolveCLIVersion covers absent, padded, invalid, older, and valid
// operational overrides. Valid text is trimmed and selected, while absent or
// rejected values return the built-in pin.
func TestResolveCLIVersion(t *testing.T) {
	cases := []struct {
		name string // case display name
		raw  string // raw parser input
		want string // expected comparison value
	}{
		{"未配置时用内置基线", "", CLICurrentVersion},
		{"只有空白等同未配置", "   ", CLICurrentVersion},
		{"合法覆盖生效", "2.1.251", "2.1.251"},
		{"两侧空白被裁掉", "  2.1.251  ", "2.1.251"},
		{"非法值回落基线", "not-a-version", CLICurrentVersion},
		{"向下覆盖被拒", "2.0.0", CLICurrentVersion},
		{"预发布后缀被拒（会毒化提供商指纹）", "2.2.0-local", CLICurrentVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveCLIVersion(tc.raw); got != tc.want {
				t.Fatalf("resolveCLIVersion(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestDefaultHeadersUserAgentMatchesCLIVersion reads the initialized default
// User-Agent and effective CLI version. The Header contains that exact version
// with the configured external CLI suffix, keeping template construction
// consistent.
func TestDefaultHeadersUserAgentMatchesCLIVersion(t *testing.T) {
	want := "claude-cli/" + CLIVersion() + " (external, cli)"
	if got := DefaultHeaders["User-Agent"]; got != want {
		t.Fatalf("DefaultHeaders[User-Agent] = %q, want %q", got, want)
	}
}

// TestCLIVersionDefaultsToBuiltinPin checks initialization in a test process
// expected to have no version override. The effective version equals the
// built-in pin, so an injected environment override would fail this assertion.
func TestCLIVersionDefaultsToBuiltinPin(t *testing.T) {
	if got := CLIVersion(); got != CLICurrentVersion {
		t.Fatalf("CLIVersion() = %q, want built-in pin %q (测试进程未设置 %s)", got, CLICurrentVersion, "provider.anthropic.claude_cli_version")
	}
}

// TestConfigureCLIVersion changes the process default before client construction.
// A supported override becomes visible and an empty value restores the bundled
// pin, with cleanup restoring the identity used by other serial tests.
func TestConfigureCLIVersion(t *testing.T) {
	previous := resolvedCLIVersion
	t.Cleanup(func() { ConfigureCLIVersion(previous) })
	ConfigureCLIVersion("3.0.0")
	if !strings.Contains(DefaultHeaders["User-Agent"], "3.0.0") {
		t.Fatal("header identity did not follow configured CLI version")
	}
	if got := CLIVersion(); got != "3.0.0" {
		t.Fatalf("CLI version = %s", got)
	}
	ConfigureCLIVersion("")
	if got := CLIVersion(); got != CLICurrentVersion {
		t.Fatalf("CLI default = %s", got)
	}
}
