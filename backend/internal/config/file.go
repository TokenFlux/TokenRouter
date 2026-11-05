package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// file.go locates the process configuration and installer data directory.
// Startup and setup share file discovery so an existing configuration suppresses
// installation regardless of whether it lives in the data directory, an explicit
// path, or a supported system directory.

// ResolveConfigFile selects an explicit path or the first existing conventional
// configuration file. Missing files return the installer destination with exists
// false; inaccessible paths and the retired system location return an error.
func ResolveConfigFile() (path string, exists bool, err error) {
	if explicit := strings.TrimSpace(os.Getenv("CONFIG_FILE")); explicit != "" {
		return inspectConfigFile(explicit)
	}
	dirs := []string{}
	if dir := strings.TrimSpace(os.Getenv("DATA_DIR")); dir != "" {
		dirs = append(dirs, dir)
	}
	dirs = append(dirs, "/app/data", ".", "./config", "/etc/tokenrouter")
	for _, dir := range dirs {
		path, exists, err = inspectConfigFile(filepath.Join(dir, "config.yaml"))
		if err != nil || exists {
			return path, exists, err
		}
	}
	_, legacy, err := inspectConfigFile("/etc/sub2api/config.yaml")
	if err != nil {
		return "", false, err
	}
	if legacy {
		return "", false, fmt.Errorf("found legacy configuration /etc/sub2api/config.yaml; move it to /etc/tokenrouter/config.yaml (or set CONFIG_FILE) and restart")
	}
	return DefaultConfigFilePath(), false, nil
}

// inspectConfigFile reports file presence while preserving non-missing filesystem
// errors. Directories cannot supply configuration and are rejected before parsing.
// A missing explicit path remains available to environment-only migration.
func inspectConfigFile(path string) (string, bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return path, false, nil
	}
	if err != nil {
		return path, false, err
	}
	if info.IsDir() {
		return path, false, fmt.Errorf("configuration %s is a directory", path)
	}
	return path, true, nil
}

// DataDir returns the explicit data directory, writable container directory, or
// current directory. The container probe is removed before returning; an unusable
// container directory leaves local installations rooted in their working directory.
func DataDir() string {
	if dir := strings.TrimSpace(os.Getenv("DATA_DIR")); dir != "" {
		return dir
	}
	if info, err := os.Stat("/app/data"); err == nil && info.IsDir() {
		probe, err := os.CreateTemp("/app/data", ".write-test-*")
		if err == nil {
			// Remove only this probe after closing its descriptor so concurrent starts
			// cannot delete one another's data-directory writability probes.
			name := probe.Name()
			_ = probe.Close()
			_ = os.Remove(name)
			return "/app/data"
		}
	}
	// An unavailable or unwritable container mount leaves local installation files
	// in the current directory, whose owner controls its persistence and permissions.
	return "."
}

// DefaultConfigFilePath returns the destination for newly installed configuration.
// CONFIG_FILE takes precedence over DataDir, including a path that does not yet
// exist; callers are responsible for preparing its parent directory.
func DefaultConfigFilePath() string {
	if path := strings.TrimSpace(os.Getenv("CONFIG_FILE")); path != "" {
		return path
	}
	return filepath.Join(DataDir(), "config.yaml")
}
