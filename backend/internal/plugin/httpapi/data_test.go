package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/plugin/provider"
)

// data_test.go exercises administrative file management through registered HTTP routes.
// Actual directory and archive adapters verify binary transfer, retained-owner
// discovery, empty-destination restore, and conflicts independently of contribution RPC limits.

// dataInstallationStore represents an uninstalled owner for retained-data HTTP scenarios.
type dataInstallationStore struct {
	plugin.InstallationStore // unused persistence operations
}

// Get reports missing installation intent while local business files remain retained.
func (dataInstallationStore) Get(context.Context, string) (*plugin.Installation, error) {
	return nil, plugin.ErrPluginNotFound
}

// List reports an empty installation inventory independently of retained local directories.
func (dataInstallationStore) List(context.Context) ([]plugin.Installation, error) {
	return nil, nil
}

// Delete accepts repeated deletion of an already absent installation and its KV.
func (dataInstallationStore) Delete(context.Context, string, bool) error { return nil }

// TestDataHTTPBackupAndRestoreTransfersLargePrivateFiles downloads a ZIP exceeding ordinary contribution limits.
// The response stays binary and noncacheable, and restoration reproduces its private bytes after an explicit retained-data purge.
func TestDataHTTPBackupAndRestoreTransfersLargePrivateFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := t.TempDir()
	files, err := provider.NewDataFiles(filepath.Join(base, "data"))
	require.NoError(t, err)
	directory, err := files.Directory(t.Context(), "test.plugin")
	require.NoError(t, err)
	content := make([]byte, 5<<20)
	_, err = rand.Read(content)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "private.bin"), content, 0o600))
	packages, err := provider.NewPackageFiles(base, provider.DownloadOptions{})
	require.NoError(t, err)
	processes := provider.NewProcesses(plugin.RuntimeOptions{}, nil, nil)
	manager := plugin.NewManager(plugin.ManagerOptions{}, dataInstallationStore{}, packages, processes, plugin.NewConfiguration(nil, false), files, nil)
	service := plugin.NewService(manager, packages, nil, nil)
	router := gin.New()
	RegisterAdminRoutes(router.Group("/api/v1/admin"), NewHandler(service, manager, processes, nil), func(c *gin.Context) { c.Next() })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins/data", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var inventory struct {
		Data []plugin.DataUsage `json:"data"` // returned retained data inventory
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &inventory))
	require.Len(t, inventory.Data, 1)
	require.False(t, inventory.Data[0].Installed)
	require.NotContains(t, response.Body.String(), base)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins/test.plugin/data/archive", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "application/zip", response.Header().Get("Content-Type"))
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Greater(t, response.Body.Len(), 4<<20)
	archive := bytes.Clone(response.Body.Bytes())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/test.plugin/data/archive", bytes.NewReader(archive))
	request.Header.Set("Content-Type", "application/zip")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusConflict, response.Code)
	require.Contains(t, response.Body.String(), "PLUGIN_DATA_EXISTS")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/plugins/test.plugin/data", nil))
	require.Equal(t, http.StatusOK, response.Code)
	request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/test.plugin/data/archive", bytes.NewReader(archive))
	request.Header.Set("Content-Type", "application/zip")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	restored, err := os.ReadFile(filepath.Join(directory, "private.bin"))
	require.NoError(t, err)
	require.Equal(t, content, restored)
}
