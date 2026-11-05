package anthropic

import (
	"log/slog"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
)

// cli_version.go resolves the Claude CLI version from deployment overrides or
// the built-in pin.
// Startup configuration selects the CLI version before client construction for
// User-Agent defaults and billing templates. Explicit configuration selects a
// supported release, while invalid overrides return the built-in pin and
// produce a warning without printing rejected input.

// resolvedCLIVersion stores the startup-selected release so separately
// constructed User-Agent and billing values use one process-wide version.
var resolvedCLIVersion = CLICurrentVersion

// CLIVersion returns the process-wide version resolved during package
// configuration.
func CLIVersion() string {
	return resolvedCLIVersion
}

// IsSupportedCLIVersion delegates strict release-version validation against
// CLICurrentVersion. It accepts complete three-part releases at or above that
// pin and rejects prerelease, build metadata, and shortened version spellings.
func IsSupportedCLIVersion(version string) bool {
	return clientmeta.IsSupportedClaudeCLIVersion(version, CLICurrentVersion)
}

// resolveCLIVersion trims and validates an operational override, returning the
// built-in pin when absent or invalid. A nonempty invalid override logs its
// configuration key and selected built-in version without echoing input.
func resolveCLIVersion(raw string) string {
	version := strings.TrimSpace(raw)
	if version == "" {
		return CLICurrentVersion
	}
	// Rejected operational text cannot enter the initialized CLI Header templates,
	// so return the built-in release after identifying the rejected configuration field.
	if !IsSupportedCLIVersion(version) {
		slog.Warn("ignoring invalid Claude CLI version override; falling back to the built-in pin",
			"key", "provider.anthropic.claude_cli_version",
			"builtin", CLICurrentVersion,
			"requirement", "strict three-part semver, not older than the built-in pin")
		return CLICurrentVersion
	}
	return version
}

// ConfigureCLIVersion selects the CLI identity before application construction.
// Empty or unsupported versions restore the built-in pin. Call once during
// startup, before requests read CLIVersion or build client identity headers.
func ConfigureCLIVersion(raw string) {
	resolvedCLIVersion = resolveCLIVersion(raw)
	// Header defaults and billing templates must use the same configured identity
	// before request preparation starts reading the shared fingerprint map.
	DefaultHeaders["User-Agent"] = "claude-cli/" + resolvedCLIVersion + " (external, cli)"
}
