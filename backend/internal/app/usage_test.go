package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// usage_test.go verifies usage export provider assembly, directory resolution,
// and confined artifact-store lifecycle hooks. Disabled export configurations
// must leave disk untouched, while enabled stores must initialize confined scope
// directories and close their root descriptor during manager shutdown.

// TestProvideUsageOptionsCopiesExportTaskSettings populates UsageExportConfig on
// config.Config and passes it to provideUsageOptions. Every background export
// field must map onto the resulting usage.Options struct without alteration.
func TestProvideUsageOptionsCopiesExportTaskSettings(t *testing.T) {
	cfg := &config.Config{
		Usage: config.UsageConfig{
			ExportTasks: config.UsageExportConfig{
				Enabled:               true,
				RetentionHours:        48,
				MaxActivePerUser:      3,
				MaxActivePerAdmin:     7,
				BatchSize:             1500,
				WorkerConcurrency:     4,
				WorkerIntervalSeconds: 10,
				TaskTimeoutSeconds:    900,
			},
		},
	}

	opts := provideUsageOptions(cfg, timezone.Calendar{})
	require.NotNil(t, opts)
	require.True(t, opts.UsageExport.Enabled)
	require.Equal(t, 48, opts.UsageExport.RetentionHours)
	require.Equal(t, 3, opts.UsageExport.MaxActivePerUser)
	require.Equal(t, 7, opts.UsageExport.MaxActivePerAdmin)
	require.Equal(t, 1500, opts.UsageExport.BatchSize)
	require.Equal(t, 4, opts.UsageExport.WorkerConcurrency)
	require.Equal(t, 10, opts.UsageExport.WorkerIntervalSeconds)
	require.Equal(t, 900, opts.UsageExport.TaskTimeoutSeconds)
}

// TestProvideUsageExportArtifactStoreSkipsDisabledAndClosesOnShutdown exercises
// disabled and enabled export artifact store assembly. Disabled settings must
// avoid directory creation, whereas enabled settings must create user/admin
// scope directories and close cleanly through the lifecycle manager.
func TestProvideUsageExportArtifactStoreSkipsDisabledAndClosesOnShutdown(t *testing.T) {
	tempDir := t.TempDir()
	disabledPath := filepath.Join(tempDir, "disabled-exports")
	disabledStore, err := provideUsageExportArtifactStore(&config.Config{
		Usage: config.UsageConfig{
			ExportTasks: config.UsageExportConfig{
				Enabled: false,
				Dir:     disabledPath,
			},
		},
	}, lifecycle.New())
	require.NoError(t, err)
	require.Nil(t, disabledStore)
	_, statErr := os.Stat(disabledPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)

	enabledPath := filepath.Join(tempDir, "enabled-exports")
	manager := lifecycle.New()
	enabledStore, err := provideUsageExportArtifactStore(&config.Config{
		Usage: config.UsageConfig{
			ExportTasks: config.UsageExportConfig{
				Enabled: true,
				Dir:     enabledPath,
			},
		},
	}, manager)
	require.NoError(t, err)
	require.NotNil(t, enabledStore)
	require.DirExists(t, filepath.Join(enabledPath, "user"))
	require.DirExists(t, filepath.Join(enabledPath, "admin"))
	require.NoError(t, manager.Stop(context.Background()))
}

// TestProvideUsageExportHandlesNilStorageDependencies passes nil database and
// store pointers into provideUsageExportRepository and provideUsageExport. The
// repository interface must remain nil and Starting the assembled export service
// must return cleanly instead of panicking on a typed-nil row reader.
func TestProvideUsageExportHandlesNilStorageDependencies(t *testing.T) {
	repo := provideUsageExportRepository(nil, nil)
	require.Nil(t, repo)

	svc := provideUsageExport(repo, nil, nil, nil, nil, provideUsageOptions(&config.Config{
		Usage: config.UsageConfig{
			ExportTasks: config.UsageExportConfig{Enabled: true},
		},
	}, timezone.Calendar{}))
	require.NotNil(t, svc)
	svc.Start()
	svc.Stop()
}
