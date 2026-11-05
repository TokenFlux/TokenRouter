package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// upgrade.go owns configuration version transitions and durable file replacement.
// Migration operates on YAML nodes so comments and key order remain available
// when settings move. A legacy file is backed up as its original bytes before
// replacement, and current-version files are read without any write. Unsupported
// future versions fail before configuration reaches application construction.
//
// Ordinary files are replaced through a uniquely named, synced temporary file
// beside their resolved target, preserving configuration symlinks. Single-file
// mounts can reject rename, so selected filesystem errors trigger a synced
// in-place write after the backup exists. That fallback cannot
// promise crash atomicity; the original backup remains the recovery source. The
// explicit migration command uses the same transformations without modifying or
// backing up its source and keeps environment secrets as references on disk.

const (
	// CurrentConfigVersion identifies the YAML structure supported by this binary,
	// independently of application releases and backward-compatible additions of defaults.
	CurrentConfigVersion = 2
)

var (
	// ErrConfigNotWritable identifies migration failures requiring an operator to
	// replace a mounted configuration after running the offline migration command.
	ErrConfigNotWritable = errors.New("configuration is not writable")

	// upgradeSteps applies each adjacent version transition in ascending order,
	// keeping historical transformations independent of the current application's schema.
	upgradeSteps = []upgradeStep{{From: 1, Apply: upgradeV1ToV2}}
)

// upgradeEnvironment supplies external observations used by migration and loading.
type upgradeEnvironment struct {
	// LookupEnv, Stat, Now, and Rename are completed at the loading boundary.
	// Omitted filesystem and clock callbacks select their operating-system implementations,
	// while an omitted lookup represents an intentionally empty migration environment.
	LookupEnv func(string) (string, bool)       // explicit environment name lookup
	Stat      func(string) (os.FileInfo, error) // legacy resource existence lookup
	Now       func() time.Time                  // backup collision timestamp clock
	Rename    func(string, string) error        // file replacement syscall injection
}

// upgradeStep converts one version to its immediate successor. Only the version
// 1 transition may import implicit environment settings; later transitions must
// preserve explicit references without consulting deployment environment values.
type upgradeStep struct {
	From  int                                                         // source configuration version
	Apply func(*yaml.Node, upgradeEnvironment) (upgradeReport, error) // adjacent version transformation
}

// upgradeReport records names and reasons without retaining configuration values.
type upgradeReport struct {
	Renamed     []string // old and new paths
	Dropped     []string // removed paths and reasons
	ImportedEnv []string // imported environment variable names
	Warnings    []string // value-free migration warning descriptions
}

// processUpgradeEnvironment binds migration observations to the running process.
func processUpgradeEnvironment() upgradeEnvironment {
	return upgradeEnvironment{LookupEnv: os.LookupEnv, Stat: os.Stat, Now: time.Now, Rename: os.Rename}
}

// completeUpgradeEnvironment fills optional filesystem and clock dependencies.
// A missing LookupEnv intentionally means an empty environment, allowing isolated
// callers to migrate files without importing the test runner's process settings.
func completeUpgradeEnvironment(env upgradeEnvironment) upgradeEnvironment {
	if env.LookupEnv == nil {
		env.LookupEnv = func(string) (string, bool) { return "", false }
	}
	if env.Stat == nil {
		env.Stat = os.Stat
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	if env.Rename == nil {
		env.Rename = os.Rename
	}
	return env
}

// upgradeConfigFile reads a configuration and persists required adjacent upgrades.
// A missing file is created only when legacy environment settings supply input;
// current-version files are returned without writes. Existing files receive an
// exclusive original-byte backup before the upgraded document replaces them.
//
// Permission failures identify the offline migration command. Rename failures on
// single-file mounts can use an in-place write, whose recovery source is the
// completed backup rather than the temporary file. Transformation errors leave
// the source untouched and return before either persistence operation begins.
// @project-doc docs/interfaces/configuration.md#config_versioning
func upgradeConfigFile(path string, exists bool, env upgradeEnvironment) (*yaml.Node, error) {
	env = completeUpgradeEnvironment(env)
	raw, doc, version, report, err := prepareConfigUpgrade(path, exists, env)
	if err != nil || doc == nil || version == CurrentConfigVersion {
		return doc, err
	}
	encoded, err := encodeConfigDocument(doc)
	if err != nil {
		return nil, err
	}
	mode := os.FileMode(0o600)
	if exists {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		mode = info.Mode().Perm()
		// Directory write permission can allow rename over a read-only file.
		// Check write access without truncating so migration respects the file's
		// own permissions before creating a backup or attempting replacement.
		writable, openErr := os.OpenFile(path, os.O_WRONLY, 0)
		if openErr != nil {
			return nil, configWriteError(path, version, openErr)
		}
		if closeErr := writable.Close(); closeErr != nil {
			return nil, closeErr
		}
		// Persist the exact pre-upgrade bytes first so either replacement strategy
		// leaves a usable rollback source when a later write or sync fails.
		if err = writeConfigBackup(path, version, raw, env.Now()); err != nil {
			return nil, configWriteError(path, version, err)
		}
	}
	if err = replaceConfigFile(path, encoded, mode, env.Rename); err != nil {
		return nil, configWriteError(path, version, err)
	}
	report.log(path)
	return doc, nil
}

// prepareConfigUpgrade parses source bytes and applies adjacent migrations in memory.
// The returned version describes the original input for backup naming, even
// after the document reaches the current schema. Missing files without legacy
// environment input return a nil document so loading can use defaults without
// creating installation evidence. Each step contributes value-free report entries;
// a missing step or transformation error stops the sequence before any write.
// Successful upgrades insert the version marker first while leaving persistence
// and original-byte backup to the calling framework.
func prepareConfigUpgrade(path string, exists bool, env upgradeEnvironment) ([]byte, *yaml.Node, int, upgradeReport, error) {
	var raw []byte
	var report upgradeReport
	if exists {
		var err error
		raw, err = os.ReadFile(path)
		if err != nil {
			return nil, nil, 0, report, err
		}
	} else if !hasV1Environment(env.LookupEnv) {
		return nil, nil, CurrentConfigVersion, report, nil
	}
	doc, version, err := parseConfigDocument(raw, path)
	if err != nil {
		return nil, nil, 0, report, err
	}
	for next := version; next < CurrentConfigVersion; next++ {
		found := false
		for _, step := range upgradeSteps {
			if step.From != next {
				continue
			}
			found = true
			part, err := step.Apply(doc, env)
			if err != nil {
				return nil, nil, version, report, err
			}
			report.Renamed = append(report.Renamed, part.Renamed...)
			report.Dropped = append(report.Dropped, part.Dropped...)
			report.ImportedEnv = append(report.ImportedEnv, part.ImportedEnv...)
			report.Warnings = append(report.Warnings, part.Warnings...)
			break
		}
		if !found {
			return nil, nil, version, report, fmt.Errorf("no configuration upgrade from version %d", next)
		}
	}
	if version < CurrentConfigVersion {
		root := doc.Content[0]
		removeConfigPath(root, "config_version")
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "config_version", HeadComment: "Managed by TokenRouter; see docs/interfaces/configuration.md before editing config_version."}
		root.Content = append([]*yaml.Node{key, configValueNode(CurrentConfigVersion)}, root.Content...)
	}
	return raw, doc, version, report, nil
}

// parseConfigDocument accepts one mapping document and a positive integer version.
// Empty input is version 1. Malformed YAML diagnostics deliberately omit parser
// excerpts because those excerpts can contain credentials from the source file.
//
// Version detection uses a materialized copy so YAML merges and scalar aliases
// carry their effective version before any migration can drop unfamiliar keys.
// The original graph is returned unchanged, allowing current-version files to
// preserve their anchors, comments, and exact bytes without an unnecessary write.
func parseConfigDocument(raw []byte, path string) (*yaml.Node, int, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, fmt.Errorf("configuration %s contains invalid YAML", path)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, 0, fmt.Errorf("configuration %s must contain one YAML document", path)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, HeadComment: strings.TrimSpace(string(raw)), Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, 0, fmt.Errorf("configuration %s must be a mapping", path)
	}
	// Decode once to reject duplicate keys and invalid alias graphs before a
	// node move could hide contradictory input or introduce a different precedence.
	var check map[string]any
	if err := doc.Decode(&check); err != nil {
		return nil, 0, fmt.Errorf("configuration %s contains invalid or duplicate YAML keys", path)
	}
	effective, err := expandConfigAliases(&doc, make(map[*yaml.Node]bool))
	if err != nil {
		return nil, 0, fmt.Errorf("configuration %s contains invalid YAML aliases", path)
	}
	for index := 0; index < len(effective.Content[0].Content); index += 2 {
		key := effective.Content[0].Content[index].Value
		if strings.EqualFold(key, "config_version") && key != "config_version" {
			return nil, 0, fmt.Errorf("configuration %s must spell config_version in lowercase", path)
		}
	}
	version := 1
	if node := configPath(effective.Content[0], "config_version"); node != nil {
		var parsed int
		err := node.Decode(&parsed)
		if node.Tag != "!!int" || err != nil || parsed <= 0 {
			return nil, 0, fmt.Errorf("config_version in %s must be a positive integer", path)
		}
		version = parsed
	}
	if version > CurrentConfigVersion {
		return nil, 0, fmt.Errorf("config_version %d in %s is newer than this binary supports (%d); restore %s.v%d.bak or upgrade TokenRouter", version, path, CurrentConfigVersion, path, version-1)
	}
	return &doc, version, nil
}

// encodeConfigDocument serializes nodes with the deployment's two-space indentation.
func encodeConfigDocument(doc *yaml.Node) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// writeConfigBackup creates an exclusive private copy of the original file bytes.
// The ordinary backup name is tried first; an existing file selects a UTC
// timestamped sibling, and another collision returns an error without overwriting
// either backup. Permissions are set explicitly before writing and syncing, so
// a restrictive umask does not alter the promised owner-only access mode.
// Failed writes can leave an incomplete backup, but the caller does not replace
// the source unless this entire operation succeeds.
func writeConfigBackup(path string, version int, raw []byte, now time.Time) error {
	name := fmt.Sprintf("%s.v%d.bak", path, version)
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		// A previous upgrade owns the ordinary backup. A timestamp preserves it
		// while giving this attempt a separate exact copy for rollback.
		name = fmt.Sprintf("%s.v%d.%s.bak", path, version, now.UTC().Format("20060102T150405Z"))
		file, err = os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	return writeAndSyncConfig(file, raw)
}

// writeAndSyncConfig writes, syncs, and closes an owned file descriptor.
// Closing runs even after a write failure, and the earliest failure wins so
// callers retain the reason their persistent configuration may be incomplete.
func writeAndSyncConfig(file *os.File, raw []byte) (err error) {
	defer func() { err = errors.Join(err, file.Close()) }()
	if _, err = file.Write(raw); err != nil {
		return err
	}
	return file.Sync()
}

// replaceConfigFile writes a synced sibling before replacing the destination.
// Caller-supplied permissions are restored explicitly, independently of umask.
// Existing symbolic links are resolved before writing, keeping the link itself
// intact; dangling links fail instead of becoming independent configuration files.
// A failed rename with EBUSY, EXDEV, or EPERM selects an in-place overwrite for
// single-file mounts; all other failures leave the destination unchanged.
//
// The in-place fallback can leave partial bytes if writing or syncing fails.
// Upgrade callers must create their backup before invoking this routine, while
// installation callers own recovery of a newly created configuration. Temporary
// files have exclusive random names, with best-effort cleanup on return. A killed
// process can leave its file behind without blocking another attempt with the
// same PID or causing that attempt to follow a preexisting temporary symlink.
func replaceConfigFile(path string, raw []byte, mode os.FileMode, rename func(string, string) error) error {
	if _, err := os.Lstat(path); err == nil {
		// Resolve the destination before choosing its sibling so rename updates the
		// managed target rather than detaching the operator's configuration symlink.
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer func() { _ = os.Remove(temp) }()
	if err = file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if err = writeAndSyncConfig(file, raw); err != nil {
		return err
	}
	if err = rename(temp, path); err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EBUSY) && !errors.Is(err, syscall.EXDEV) && !errors.Is(err, syscall.EPERM) {
		return err
	}
	// Bind mounts can reject replacement despite permitting writes to their open
	// file. The caller's completed backup remains available if this overwrite fails.
	if err = os.Remove(temp); err != nil {
		return err
	}
	file, err = os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	return writeAndSyncConfig(file, raw)
}

// WriteConfigFile persists installer output with private file permissions.
// The parent directory must already exist. Replacement follows the same mount
// fallback as upgrades, but callers do not receive an automatic historical backup.
func WriteConfigFile(path string, raw []byte) error {
	return replaceConfigFile(path, raw, 0o600, os.Rename)
}

// configWriteError adds actionable offline instructions to filesystem permission
// failures. Other I/O errors retain their original identity and message so
// callers can distinguish an unavailable directory from a read-only deployment.
func configWriteError(path string, version int, err error) error {
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.EPERM) || errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: configuration %s uses config_version %d and must be upgraded to version %d, but the file is not writable (%v).\nRun %q -migrate-config -config %q -out <new-path> with the same environment variables, replace the mounted file with <new-path>, and restart", ErrConfigNotWritable, path, version, CurrentConfigVersion, err, os.Args[0], path)
	}
	return err
}

// log emits migration names and counts without including any configuration values.
func (report upgradeReport) log(path string) {
	slog.Info("configuration upgraded", "path", path, "config_version", CurrentConfigVersion, "renamed", report.Renamed, "imported_env", report.ImportedEnv)
	for _, item := range report.Dropped {
		slog.Warn("configuration key dropped", "key", item)
	}
	for _, item := range report.Warnings {
		slog.Warn("configuration upgrade warning", "detail", item)
	}
}

// MigrateConfig writes an upgraded copy while leaving the source file untouched.
// Output must differ from the source; a dash selects stdout. Current-version
// bytes are preserved exactly, and reports use stderr without resolved secrets.
func MigrateConfig(source, out string, stdout, stderr io.Writer) error {
	if out == "" {
		return errors.New("-migrate-config requires -out")
	}
	var exists bool
	var err error
	if source == "" {
		source, exists, err = ResolveConfigFile()
	} else {
		source, exists, err = inspectConfigFile(source)
	}
	if err != nil {
		return err
	}
	if out != "-" {
		a, _ := filepath.Abs(source)
		b, _ := filepath.Abs(out)
		if a == b {
			return errors.New("migration source and output must differ")
		}
		sourceInfo, sourceErr := os.Stat(source)
		outInfo, outErr := os.Stat(out)
		if sourceErr == nil && outErr == nil && os.SameFile(sourceInfo, outInfo) {
			return errors.New("migration source and output must differ")
		}
	}
	raw, doc, version, report, err := prepareConfigUpgrade(source, exists, processUpgradeEnvironment())
	if err != nil {
		return err
	}
	if doc == nil {
		return errors.New("no configuration file or legacy environment settings to migrate")
	}
	if version == CurrentConfigVersion {
		fmt.Fprintf(stderr, "already at config_version %d\n", CurrentConfigVersion)
	} else {
		raw, err = encodeConfigDocument(doc)
		if err != nil {
			return err
		}
		fmt.Fprintf(stderr, "configuration upgraded to version %d; renamed=%v imported_env=%v dropped=%v warnings=%v\n", CurrentConfigVersion, report.Renamed, report.ImportedEnv, report.Dropped, report.Warnings)
	}
	if out == "-" {
		_, err = stdout.Write(raw)
		return err
	}
	return WriteConfigFile(out, raw)
}
