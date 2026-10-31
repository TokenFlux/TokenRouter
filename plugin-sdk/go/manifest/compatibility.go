package manifest

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

// compatibility.go compares a validated release with the capabilities of a
// running host. Compatibility is a conjunction of protocol support, host
// version bounds, implemented permissions, and artifact availability; no one
// successful check can substitute for another. Reporting every failed check
// lets the market explain an unavailable version without selecting a package
// that the runtime could never start.
//
// Development builds skip only version bounds because their build strings do
// not identify a released host contract. They still require an implemented API
// major, permissions, and a suitable artifact. The special any platform is
// available only to releases without a backend executable, preventing a
// platform-neutral label from admitting an incompatible native binary.

// HostInfo describes the host release and the capabilities it implements.
type HostInfo struct {
	Version     string   `json:"version"`     // host release build version
	APIVersions []int    `json:"apiVersions"` // supported protocol major versions
	Platform    string   `json:"platform"`    // operating-system and architecture pair
	Permissions []string `json:"permissions"` // implemented host permission names
}

// Incompatibility explains one independent reason a release cannot be installed.
type Incompatibility struct {
	Code   string `json:"code"`   // stable compatibility reason code
	Detail string `json:"detail"` // administrator-readable failure explanation
}

// CheckCompatibility reports all incompatibilities for a validated manifest.
// Artifact keys use os/arch or any; a static plugin may use any, while a backend
// requires the exact host platform. The returned empty slice means compatible.
//
// A release build must satisfy both host version bounds and the supported API
// list. Development builds bypass only the version comparison, and the host is
// responsible for warning once when it exercises that exception. Permission
// checks still reject capabilities absent from the supplied host implementation.
func CheckCompatibility(m *Manifest, platforms []string, host HostInfo) []Incompatibility {
	result := make([]Incompatibility, 0)
	if !slices.Contains(host.APIVersions, m.APIVersion) {
		result = append(result, Incompatibility{"API_VERSION", fmt.Sprintf("plugin requires API version %d", m.APIVersion)})
	}
	version := strings.TrimPrefix(host.Version, "v")
	// Development identifiers cannot establish release bounds; all remaining compatibility checks continue for these hosts with identical rules.
	if version != "0.0.0-dev" && ValidVersion(version) {
		if semver.Compare("v"+version, "v"+m.MinHostVersion) < 0 {
			result = append(result, Incompatibility{"HOST_TOO_OLD", "requires host " + m.MinHostVersion + " or newer"})
		}
		if m.MaxHostVersion != "" && semver.Compare("v"+version, "v"+m.MaxHostVersion) >= 0 {
			result = append(result, Incompatibility{"HOST_TOO_NEW", "requires host older than " + m.MaxHostVersion})
		}
	}
	for _, permission := range m.Permissions {
		if !slices.Contains(host.Permissions, permission) {
			result = append(result, Incompatibility{"PERMISSION_UNSUPPORTED", permission})
		}
	}
	if !slices.Contains(platforms, host.Platform) && (m.Backend != nil || !slices.Contains(platforms, "any")) {
		result = append(result, Incompatibility{"PLATFORM_MISSING", host.Platform})
	}
	return result
}
