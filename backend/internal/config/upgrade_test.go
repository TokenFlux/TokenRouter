package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestUpgradeConfigFilePreservesBackup exercises durable migration and a second load.
// It protects exact original backup bytes, private backup permissions, source mode,
// comment retention, and the absence of writes for current configuration files.
func TestUpgradeConfigFilePreservesBackup(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("# deployment\ngateway:\n  # request limit\n  max_body_size: 2048 # bytes\ndatabase:\n  password: old\n")
	require.NoError(t, os.WriteFile(file, original, 0o640))
	env := upgradeEnvironment{LookupEnv: func(name string) (string, bool) {
		if name == "DATABASE_PASSWORD" {
			return "private-secret", true
		}
		return "", false
	}}
	doc, err := upgradeConfigFile(file, true, env)
	require.NoError(t, err)
	require.Equal(t, "2", configPath(doc.Content[0], "config_version").Value)
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Contains(t, string(raw), "${DATABASE_PASSWORD}")
	require.NotContains(t, string(raw), "private-secret")
	require.Contains(t, string(raw), "# request limit")
	require.Contains(t, string(raw), "# bytes")
	backup, err := os.ReadFile(file + ".v1.bak")
	require.NoError(t, err)
	require.Equal(t, original, backup)
	info, err := os.Stat(file)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	backupInfo, err := os.Stat(file + ".v1.bak")
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), backupInfo.Mode().Perm())
	_, err = upgradeConfigFile(file, true, env)
	require.NoError(t, err)
	after, err := os.Stat(file)
	require.NoError(t, err)
	require.Equal(t, info.ModTime(), after.ModTime())
}

// TestUpgradeConfigFileMountFallback simulates a mount refusing atomic replacement.
// The fallback must write the upgraded bytes in place while retaining the exact
// original backup as the recovery source for that non-atomic write strategy.
func TestUpgradeConfigFileMountFallback(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(file, []byte("server:\n  port: 8123\n"), 0o600))
	_, err := upgradeConfigFile(file, true, upgradeEnvironment{Rename: func(string, string) error { return syscall.EBUSY }})
	require.NoError(t, err)
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Contains(t, string(raw), "config_version: 2")
	require.FileExists(t, file+".v1.bak")
}

// TestUpgradeConfigFileMissingAndBackupCollision covers absent input and repeated upgrades.
// Environment-only configuration creates a private file, empty deployments create
// nothing, and an existing ordinary backup receives a separate timestamped sibling.
func TestUpgradeConfigFileMissingAndBackupCollision(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	doc, err := upgradeConfigFile(file, false, upgradeEnvironment{})
	require.NoError(t, err)
	require.Nil(t, doc)
	require.NoFileExists(t, file)
	_, err = upgradeConfigFile(file, false, upgradeEnvironment{LookupEnv: func(name string) (string, bool) { return "", name == "SERVER_TRUSTED_PROXIES" }})
	require.NoError(t, err)
	require.FileExists(t, file)
	require.NoFileExists(t, file+".v1.bak")
	require.NoError(t, os.WriteFile(file, []byte("{}\n"), 0o600))
	require.NoError(t, os.WriteFile(file+".v1.bak", []byte("earlier"), 0o600))
	_, err = upgradeConfigFile(file, true, upgradeEnvironment{Now: func() time.Time { return time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC) }})
	require.NoError(t, err)
	require.FileExists(t, file+".v1.20261105T100000Z.bak")
	_, err = upgradeConfigFile(filepath.Join(t.TempDir(), "missing", "config.yaml"), false, upgradeEnvironment{LookupEnv: func(name string) (string, bool) { return "", name == "SERVER_TRUSTED_PROXIES" }})
	require.ErrorIs(t, err, ErrConfigNotWritable)
	require.Contains(t, err.Error(), "-migrate-config")
}

// TestParseConfigDocumentVersions rejects malformed, unsupported, and contradictory input.
// Version diagnostics must not expose neighboring secret values when parsing fails,
// and empty or comment-only sources remain valid legacy migration inputs.
func TestParseConfigDocumentVersions(t *testing.T) {
	for _, raw := range []string{"config_version: 0", "config_version: -1", "config_version: 1.5", "config_version: '2'", "config_version: 3", "CONFIG_VERSION: 3", "null", "[]", "a: 1\na: 2", "a: 1\n---\nb: 2"} {
		t.Run(raw, func(t *testing.T) {
			_, _, err := parseConfigDocument([]byte(raw), "config.yaml")
			require.Error(t, err)
		})
	}
	for _, raw := range []string{"config_version: 2", "config_version: 0x2", "config_version: +2"} {
		_, version, err := parseConfigDocument([]byte(raw), "config.yaml")
		require.NoError(t, err)
		require.Equal(t, 2, version)
	}
	for _, raw := range []string{"", "# comment\n", "{}"} {
		_, version, err := parseConfigDocument([]byte(raw), "config.yaml")
		require.NoError(t, err)
		require.Equal(t, 1, version)
	}
	_, _, err := parseConfigDocument([]byte("database: [private-secret\n"), "config.yaml")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-secret")
}

// TestMigrateConfigOutputsCopy exercises stdout and destination-file migration modes.
// The source stays byte-identical, current-version input preserves formatting, and
// using the source itself as an output is rejected before a write occurs.
func TestMigrateConfigOutputsCopy(t *testing.T) {
	source := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte("config_version: 2\n# preserve\nserver: {port: 8081}\n")
	require.NoError(t, os.WriteFile(source, raw, 0o600))
	var stdout, stderr bytes.Buffer
	require.NoError(t, MigrateConfig(source, "-", &stdout, &stderr))
	require.Equal(t, raw, stdout.Bytes())
	require.Contains(t, stderr.String(), "already at config_version 2")
	out := filepath.Join(t.TempDir(), "copy.yaml")
	require.NoError(t, MigrateConfig(source, out, &stdout, &stderr))
	copied, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, raw, copied)
	require.Error(t, MigrateConfig(source, source, &stdout, &stderr))
	unchanged, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, raw, unchanged)
}

// TestConfigWriteErrorClassifiesPermissions checks actionable error wrapping for mounts.
// Permission failures retain the sentinel and offline instructions while unrelated
// failures preserve their identity instead of being mislabeled as read-only input.
func TestConfigWriteErrorClassifiesPermissions(t *testing.T) {
	for _, cause := range []error{syscall.EACCES, syscall.EROFS, syscall.EPERM} {
		err := configWriteError("config.yaml", 1, cause)
		require.ErrorIs(t, err, ErrConfigNotWritable)
		require.True(t, strings.Contains(err.Error(), "-migrate-config"))
	}
	cause := errors.New("disk unavailable")
	require.ErrorIs(t, configWriteError("config.yaml", 1, cause), cause)
}

// TestUpgradeReadOnlyDirectoryFailsBeforeReplacement removes write access from a legacy directory.
// Startup must retain the original bytes and return the actionable migration
// sentinel, allowing an operator to produce an offline copy before replacement.
func TestUpgradeReadOnlyDirectoryFailsBeforeReplacement(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can bypass filesystem mode restrictions; read-only mounts are covered by deployment verification")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	original := []byte("server:\n  port: 8081\n")
	require.NoError(t, os.WriteFile(file, original, 0o600))
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { require.NoError(t, os.Chmod(dir, 0o700)) })
	_, err := upgradeConfigFile(file, true, upgradeEnvironment{})
	require.ErrorIs(t, err, ErrConfigNotWritable)
	after, readErr := os.ReadFile(file)
	require.NoError(t, readErr)
	require.Equal(t, original, after)
}

// TestUpgradeCommentOnlyFile preserves deployment notes without any original settings.
// The empty legacy document receives the current version marker while its
// comments remain visible and its exact initial bytes remain in the backup.
func TestUpgradeCommentOnlyFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("# operator deployment notes\n")
	require.NoError(t, os.WriteFile(file, original, 0o600))
	_, err := upgradeConfigFile(file, true, upgradeEnvironment{})
	require.NoError(t, err)
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Contains(t, string(raw), "operator deployment notes")
	require.Contains(t, string(raw), "config_version: 2")
}

// TestUpgradeInvalidTransformedFlagsLeavesSourceUntouched supplies malformed historical booleans.
// Consolidating or inverting a flag must not silently discard its decoding
// failure, and diagnostics must identify the key without echoing the supplied value.
func TestUpgradeInvalidTransformedFlagsLeavesSourceUntouched(t *testing.T) {
	for _, body := range []string{
		"gateway:\n  user_message_queue:\n    enabled: sensitive-invalid\n",
		"gateway:\n  openai_proxy_stream_circuit:\n    disabled: sensitive-invalid\n",
		"server:\n  mode: {}\n",
	} {
		file := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(file, []byte(body), 0o600))
		_, err := upgradeConfigFile(file, true, upgradeEnvironment{})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "sensitive-invalid")
		after, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, body, string(after))
		require.NoFileExists(t, file+".v1.bak")
	}
}

// TestUpgradeReadOnlyFileRejectsWritableParent prevents rename from bypassing file permissions.
// A readable legacy file without write access must retain its original bytes
// and require offline migration even when its containing directory is writable.
func TestUpgradeReadOnlyFileRejectsWritableParent(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can bypass file mode restrictions")
	}
	file := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("server:\n  port: 8081\n")
	require.NoError(t, os.WriteFile(file, original, 0o400))
	_, err := upgradeConfigFile(file, true, upgradeEnvironment{})
	require.ErrorIs(t, err, ErrConfigNotWritable)
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, original, raw)
	require.NoFileExists(t, file+".v1.bak")
}

// TestUpgradeMergedVersionsPreserveSource checks effective versions supplied through YAML merges.
// Current documents retain their bytes and credentials, while invalid or future
// markers fail before backups or writes; explicit markers retain YAML precedence.
func TestUpgradeMergedVersionsPreserveSource(t *testing.T) {
	for _, tc := range []struct {
		name    string // version source scenario
		source  string // version marker YAML
		wantErr bool   // expected version rejection
	}{
		{name: "current merge", source: "<<: {config_version: 2}\n"},
		{name: "nested merge", source: "<<: {<<: {config_version: 2}}\n"},
		{name: "sequence precedence", source: "<<: [{config_version: 2}, {config_version: 3}]\n"},
		{name: "explicit precedence", source: "<<: {config_version: 3}\nconfig_version: 2\n"},
		{name: "scalar alias", source: "<<: {config_version: &version 2}\nconfig_version: *version\n"},
		{name: "future merge", source: "<<: {config_version: 3}\n", wantErr: true},
		{name: "string merge", source: "<<: {config_version: '2'}\n", wantErr: true},
		{name: "float merge", source: "<<: {config_version: 2.0}\n", wantErr: true},
		{name: "zero merge", source: "<<: {config_version: 0}\n", wantErr: true},
		{name: "uppercase merge", source: "<<: {CONFIG_VERSION: 2}\n", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "config.yaml")
			raw := []byte(tc.source + "auth:\n  jwt:\n    secret: preserved-config-secret\n")
			require.NoError(t, os.WriteFile(file, raw, 0o600))
			doc, err := upgradeConfigFile(file, true, upgradeEnvironment{})
			if tc.wantErr {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "preserved-config-secret")
			} else {
				require.NoError(t, err)
				require.NotNil(t, doc)
			}
			after, err := os.ReadFile(file)
			require.NoError(t, err)
			require.Equal(t, raw, after)
			entries, err := os.ReadDir(filepath.Dir(file))
			require.NoError(t, err)
			require.Len(t, entries, 1)
		})
	}
}

// TestUpgradePreservesConfigSymlink exercises relative links into a managed configuration directory.
// Both atomic replacement and mount fallback must update the target, preserve
// its permissions and backup bytes, and observe later edits through the original link.
func TestUpgradePreservesConfigSymlink(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprintf("mount_fallback_%t", fallback), func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(dir, "managed"), 0o700))
			target := filepath.Join(dir, "managed", "runtime.yaml")
			file := filepath.Join(dir, "config.yaml")
			raw := []byte("server:\n  port: 9090\n")
			require.NoError(t, os.WriteFile(target, raw, 0o640))
			require.NoError(t, os.Symlink("managed/runtime.yaml", file))
			env := upgradeEnvironment{}
			if fallback {
				resolved, err := filepath.EvalSymlinks(target)
				require.NoError(t, err)
				env.Rename = func(from, to string) error {
					require.Equal(t, resolved, to)
					require.Equal(t, filepath.Dir(resolved), filepath.Dir(from))
					return syscall.EBUSY
				}
			}
			_, err := upgradeConfigFile(file, true, env)
			require.NoError(t, err)
			link, err := os.Readlink(file)
			require.NoError(t, err)
			require.Equal(t, "managed/runtime.yaml", link)
			info, err := os.Stat(target)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
			backup, err := os.ReadFile(file + ".v1.bak")
			require.NoError(t, err)
			require.Equal(t, raw, backup)
			upgraded, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Contains(t, string(upgraded), "config_version: 2")
			require.NoError(t, os.WriteFile(target, []byte("config_version: 2\nserver:\n  port: 9191\n"), 0o640))
			loaded, err := loadFrom(file, true, upgradeEnvironment{}, true)
			require.NoError(t, err)
			require.Equal(t, 9191, loaded.Server.Port)
		})
	}
}

// TestWriteConfigFileRejectsDanglingSymlink protects missing managed targets from silent detachment.
// Installation output must report the unresolved target and leave the configured
// link intact, allowing its owner to restore the intended file location.
func TestWriteConfigFileRejectsDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.Symlink("missing.yaml", file))
	require.ErrorIs(t, WriteConfigFile(file, []byte("config_version: 2\n")), os.ErrNotExist)
	link, err := os.Readlink(file)
	require.NoError(t, err)
	require.Equal(t, "missing.yaml", link)
	require.NoFileExists(t, filepath.Join(dir, "missing.yaml"))
}

// TestUpgradeIgnoresStaleTemporaryFiles models files surviving an interrupted container process.
// A retry with the same PID must succeed without reusing, following, or deleting
// either the stale bytes or a preexisting temporary symlink.
func TestUpgradeIgnoresStaleTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(file, []byte("server: {port: 9090}\n"), 0o600))
	stale := filepath.Join(dir, fmt.Sprintf(".config.yaml.tmp-%d", os.Getpid()))
	require.NoError(t, os.WriteFile(stale, []byte("interrupted write"), 0o600))
	link := filepath.Join(dir, ".config.yaml.tmp-preexisting")
	require.NoError(t, os.Symlink(filepath.Base(stale), link))
	_, err := upgradeConfigFile(file, true, upgradeEnvironment{})
	require.NoError(t, err)
	raw, err := os.ReadFile(stale)
	require.NoError(t, err)
	require.Equal(t, "interrupted write", string(raw))
	_, err = os.Readlink(link)
	require.NoError(t, err)
	temps, err := filepath.Glob(filepath.Join(dir, ".config.yaml.tmp-*"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{stale, link}, temps)
	loaded, err := loadFrom(file, true, upgradeEnvironment{}, true)
	require.NoError(t, err)
	require.Equal(t, 9090, loaded.Server.Port)
}
