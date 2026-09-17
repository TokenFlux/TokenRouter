package provider

import (
	"os"
	"path/filepath"
	"strings"
)

func DefaultBackupLocalPath() string {
	return filepath.Join(resolveBackupDataDir(), "backups")
}
func resolveBackupDataDir() string {
	if dir := os.Getenv("DATA_DIR"); strings.TrimSpace(dir) != "" {
		return dir
	}

	dockerDataDir := "/app/data"
	if info, err := os.Stat(dockerDataDir); err == nil && info.IsDir() {
		testFile := filepath.Join(dockerDataDir, ".write_test")
		if f, err := os.Create(testFile); err == nil {
			_ = f.Close()
			_ = os.Remove(testFile)
			return dockerDataDir
		}
	}
	return "."
}
