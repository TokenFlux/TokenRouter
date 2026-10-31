package manifest

import (
	"slices"
	"testing"
)

// TestCheckCompatibilityCombinesIndependentConstraints checks all compatibility
// dimensions against one release. Development versions bypass only version
// bounds and must still satisfy platform, permissions, and protocol support.
func TestCheckCompatibilityCombinesIndependentConstraints(t *testing.T) {
	m := validManifest(t)
	m.MaxHostVersion = "1.0.0"
	host := HostInfo{Version: "v0.6.0", APIVersions: []int{1}, Platform: "linux/amd64", Permissions: Permissions()}
	if got := CheckCompatibility(m, []string{"linux/amd64"}, host); len(got) != 0 {
		t.Fatal(got)
	}
	for version, want := range map[string]string{"0.4.9": "HOST_TOO_OLD", "1.0.0": "HOST_TOO_NEW", "0.0.0-dev": "", "local": ""} {
		host.Version = version
		got := CheckCompatibility(m, []string{"linux/amd64"}, host)
		if want == "" && len(got) != 0 {
			t.Errorf("%s: %v", version, got)
		}
		if want != "" && (len(got) != 1 || got[0].Code != want) {
			t.Errorf("%s: %v", version, got)
		}
	}
	host.Version = "local"
	host.APIVersions = nil
	host.Permissions = nil
	got := CheckCompatibility(m, []string{"any"}, host)
	for _, want := range []string{"API_VERSION", "PERMISSION_UNSUPPORTED", "PLATFORM_MISSING"} {
		if !slices.ContainsFunc(got, func(item Incompatibility) bool { return item.Code == want }) {
			t.Errorf("missing %s: %v", want, got)
		}
	}
	m.Backend = nil
	host.APIVersions = []int{1}
	host.Permissions = Permissions()
	if got := CheckCompatibility(m, []string{"any"}, host); len(got) != 0 {
		t.Fatal(got)
	}
}
