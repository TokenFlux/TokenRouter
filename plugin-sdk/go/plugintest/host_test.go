package plugintest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestNewLocalDirectories exercises plugin-owned files across independent harnesses
// and cache removal. Data remains isolated and writable until testing cleanup
// removes each harness's directories after the owning subtest finishes.
func TestNewLocalDirectories(t *testing.T) {
	var directories []string
	t.Run("local storage", func(t *testing.T) {
		first := New(t)
		second := New(t)
		directories = []string{
			first.Env.StateDir, first.Env.DataDir,
			second.Env.StateDir, second.Env.DataDir,
		}
		seen := make(map[string]bool)
		for _, directory := range directories {
			if !filepath.IsAbs(directory) || seen[directory] {
				t.Fatalf("directory must be absolute and distinct: %q", directory)
			}
			seen[directory] = true
			if err := os.WriteFile(filepath.Join(directory, "probe"), []byte(directory), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		marker := filepath.Join(first.Env.DataDir, "records.json")
		if err := os.WriteFile(marker, []byte(`{"saved":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, directory := range []string{first.Env.StateDir, second.Env.DataDir} {
			if _, err := os.Stat(filepath.Join(directory, "records.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("plugin data leaked to %q: %v", directory, err)
			}
		}
		// Remove disposable cache files independently of business records, as an
		// author may do during initialization before reopening local storage.
		if err := os.RemoveAll(first.Env.StateDir); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(marker); err != nil || string(data) != `{"saved":true}` {
			t.Fatalf("persistent record after cache removal = %q, %v", data, err)
		}
	})
	for _, directory := range directories {
		if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("directory remains after test cleanup: %q, %v", directory, err)
		}
	}
}
