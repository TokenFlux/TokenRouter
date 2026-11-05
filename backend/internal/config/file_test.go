package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolveConfigFileHonorsExplicitPath places two files at competing locations.
// CONFIG_FILE must win over DATA_DIR, and a missing explicit file returns its
// own destination without falling through to an existing conventional file.
func TestResolveConfigFileHonorsExplicitPath(t *testing.T) {
	dir := t.TempDir()
	fallback := filepath.Join(dir, "config.yaml")
	explicit := filepath.Join(t.TempDir(), "selected.yaml")
	require.NoError(t, os.WriteFile(fallback, []byte("{}"), 0o600))
	require.NoError(t, os.WriteFile(explicit, []byte("{}"), 0o600))
	t.Setenv("DATA_DIR", dir)
	t.Setenv("CONFIG_FILE", explicit)
	path, exists, err := ResolveConfigFile()
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, explicit, path)
	require.NoError(t, os.Remove(explicit))
	path, exists, err = ResolveConfigFile()
	require.NoError(t, err)
	require.False(t, exists)
	require.Equal(t, explicit, path)
	require.Equal(t, explicit, DefaultConfigFilePath())
	t.Setenv("CONFIG_FILE", "")
	path, exists, err = ResolveConfigFile()
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, fallback, path)
}
