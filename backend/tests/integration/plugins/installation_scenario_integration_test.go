//go:build integration

package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/crypto"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/plugin/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/plugin/postgres"
	"github.com/TokenFlux/TokenRouter/internal/plugin/provider"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// installation_scenario_integration_test.go covers service.go, manager.go,
// configuration.go, HTTP administration, PostgreSQL storage, and package/process
// providers, public contribution proxying, and asset serving together. Synthetic archives and a disposable database exercise real
// encryption, startup, restart, repair, and purge without deployment credentials.

// TestInstalledPluginHTTPPersistenceAndRecovery runs the complete uploaded native-plugin administration path against PostgreSQL and a real child.
// Secrets persist encrypted, startup receives plaintext, reconstruction restores enabled intent, and repair retains configuration and KV before purging uninstall.
func TestInstalledPluginHTTPPersistenceAndRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := databaseSuite.New(t)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store := postgres.New(client)
	cipher, err := crypto.NewAESEncryptor(bytes.Repeat([]byte{7}, 32))
	require.NoError(t, err)
	configuration := plugin.NewConfiguration(cipher, true)
	directory := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	packages, err := provider.NewPackageFiles(directory, provider.DownloadOptions{})
	require.NoError(t, err)
	processes := provider.NewProcesses(plugin.RuntimeOptions{HostVersion: "0.5.4", InstanceID: "installation-test", Timezone: "UTC"}, logger, nil)
	manager := plugin.NewManager(plugin.ManagerOptions{Enabled: true, MaxRunning: 20, HostVersion: "0.5.4", StateRoot: filepath.Join(directory, "state")}, store, packages, processes, configuration, logger)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, manager.Stop(ctx))
	})
	service := plugin.NewService(manager, packages, nil, nil)
	router := gin.New()
	admin := router.Group("/api/v1/admin")
	admin.Use(func(c *gin.Context) {
		c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 1})
		c.Next()
	})
	httpapi.RegisterAdminRoutes(admin, httpapi.NewHandler(service, manager, processes, nil), func(c *gin.Context) { c.Next() })
	public := httpapi.NewPublicHandler(httpapi.PublicOptions{
		Runtime:  manager,
		Files:    packages,
		ClientIP: func(*gin.Context) string { return "192.0.2.1" },
		Language: func(*gin.Context) string { return "en" },
		Logger:   logger,
	})
	allow := func(c *gin.Context) { c.Next() }
	deny := func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) }
	httpapi.RegisterPublicRoutes(router.Group("/api/v1"), public, httpapi.PublicMiddleware{JWT: deny, Admin: deny, BackendUser: allow, Panel: allow, PublicIP: allow})
	archive := installationArchive(t)
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	part, err := writer.CreateFormFile("file", "plugin.tar.gz")
	require.NoError(t, err)
	_, err = part.Write(archive)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/uploads", &multipartBody)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var preview struct {
		Data httpapi.UploadDTO `json:"data"` // accepted upload preview envelope
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &preview))
	body, err := json.Marshal(plugin.InstallInput{Source: plugin.SourceUpload, UploadToken: preview.Data.UploadToken, Enable: true, AcceptPermissions: []string{}})
	require.NoError(t, err)
	response := pluginRequest(t, router, http.MethodPost, "/api/v1/admin/plugins", body)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var installed struct {
		Data httpapi.ChangeDTO `json:"data"` // accepted installation mutation envelope
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &installed))
	require.True(t, installed.Data.NeedsConfiguration)
	require.False(t, installed.Data.Installation.Enabled)
	response = pluginRequest(t, router, http.MethodPost, "/api/v1/admin/plugins/test.installation/enable", nil)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "PLUGIN_CONFIG_INVALID")
	response = pluginRequest(t, router, http.MethodPut, "/api/v1/admin/plugins/test.installation/config", []byte(`{"values":{"credential":"fixture-secret","label":"kept"}}`))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"set":true`)
	require.NotContains(t, response.Body.String(), "fixture-secret")
	var encrypted, ordinary string
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT secret_config,config::text FROM plugin_installations WHERE plugin_id='test.installation'").Scan(&encrypted, &ordinary))
	require.NotEmpty(t, encrypted)
	require.NotContains(t, encrypted, "fixture-secret")
	require.NotContains(t, ordinary, "fixture-secret")
	started := time.Now()
	response = pluginRequest(t, router, http.MethodPost, "/api/v1/admin/plugins/test.installation/enable", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, plugin.StateRunning, manager.Status("test.installation").State)
	require.Less(t, time.Since(started), 5*time.Second)
	lease, err := manager.Acquire(t.Context(), "test.installation")
	require.NoError(t, err)
	health, err := lease.Conn.Plugin.Health(t.Context(), &pluginpb.HealthRequest{})
	lease.Release()
	require.NoError(t, err)
	require.True(t, health.Ready)
	response = pluginRequest(t, router, http.MethodGet, "/api/v1/plugins/test.installation/api/ready", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"ready":true}`, response.Body.String())
	response = pluginRequest(t, router, http.MethodGet, "/api/v1/plugins/test.installation/api/undeclared", nil)
	require.Equal(t, http.StatusNotFound, response.Code)
	response = pluginRequest(t, router, http.MethodGet, "/api/v1/plugins/test.installation/assets/1.0.0/frontend/index.js", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "globalThis.installationFixture=true;", response.Body.String())
	require.NotContains(t, response.Body.String(), "fixture-secret")
	response = pluginRequest(t, router, http.MethodGet, "/api/v1/plugins/test.installation/assets/1.0.0/plugin.json", nil)
	require.Equal(t, http.StatusNotFound, response.Code)
	response = pluginRequest(t, router, http.MethodPost, "/api/v1/admin/plugins/test.installation/disable", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Zero(t, manager.Status("test.installation").PID)
	response = pluginRequest(t, router, http.MethodPost, "/api/v1/admin/plugins/test.installation/enable", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	// Stop the first owner before reconstructing the same durable installation; the second manager must restore enabled intent without an HTTP enable request.
	require.NoError(t, manager.Stop(t.Context()))
	restartedProcesses := provider.NewProcesses(plugin.RuntimeOptions{HostVersion: "0.5.4", InstanceID: "restarted-test", Timezone: "UTC"}, logger, nil)
	restarted := plugin.NewManager(plugin.ManagerOptions{Enabled: true, MaxRunning: 20, HostVersion: "0.5.4", StateRoot: filepath.Join(directory, "state")}, store, packages, restartedProcesses, configuration, logger)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, restarted.Stop(ctx))
	})
	restarted.SyncAll(t.Context())
	require.Eventually(t, func() bool { return restarted.Status("test.installation").State == plugin.StateRunning }, 5*time.Second, 10*time.Millisecond)
	_, err = db.ExecContext(t.Context(), `INSERT INTO plugin_kv(plugin_id,collection,key,value) VALUES('test.installation','retained','value','{"score":7}')`)
	require.NoError(t, err)
	require.NoError(t, restartedProcesses.Stop(t.Context(), "test.installation"))
	loaded, ok := restarted.Snapshot("test.installation")
	require.True(t, ok)
	require.NoError(t, os.RemoveAll(loaded.Package.Directory))
	err = restarted.Restart(t.Context(), "test.installation")
	require.ErrorIs(t, err, plugin.ErrPackageMissing)
	upload, err := packages.Preview(t.Context(), bytes.NewReader(archive))
	require.NoError(t, err)
	recovered, err := plugin.NewService(restarted, packages, nil, nil).Install(t.Context(), plugin.InstallInput{Source: plugin.SourceUpload, UploadToken: upload.Token, AcceptPermissions: []string{}}, 1)
	require.NoError(t, err)
	require.True(t, recovered.Repaired)
	require.Equal(t, plugin.StateRunning, recovered.Status.State)
	require.Equal(t, "kept", recovered.Installation.Config["label"])
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM plugin_kv WHERE plugin_id='test.installation'").Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, plugin.NewService(restarted, packages, nil, nil).Uninstall(t.Context(), "test.installation", true))
	_, err = store.Get(t.Context(), "test.installation")
	require.ErrorIs(t, err, plugin.ErrPluginNotFound)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM plugin_kv WHERE plugin_id='test.installation'").Scan(&count))
	require.Zero(t, count)
}

// pluginRequest dispatches one JSON administrative request against the in-process route graph.
func pluginRequest(t *testing.T, router *gin.Engine, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// installationArchive packages the freshly built fixture with a required secret schema.
// Only synthetic manifest values and executable bytes are included. The native
// platform's executable suffix follows the same contract as published plugin archives.
func installationArchive(t *testing.T) []byte {
	t.Helper()
	binary, err := os.ReadFile(installationBinary)
	require.NoError(t, err)
	name := "bin/plugin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	declaration := []byte(`{"schemaVersion":1,"id":"test.installation","version":"1.0.0","apiVersion":1,"minHostVersion":"0.5.0","name":{"en":"Installation fixture"},"description":{"en":"Installation fixture"},"publisher":{"name":"Test"},"license":"MIT","backend":{"executable":"bin/plugin","routes":[{"method":"GET","path":"/ready","auth":"public"}]},"frontend":{"entry":"frontend/index.js","pages":[{"id":"home","path":"","audience":"user","title":{"en":"Fixture"}}]},"config":{"fields":[{"key":"credential","type":"secret","label":{"en":"Credential"},"required":true},{"key":"label","type":"string","label":{"en":"Label"}}]}}`)
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, entry := range []struct {
		name string // archive-relative entry path
		data []byte // complete fixture entry bytes
	}{{"plugin.json", declaration}, {name, binary}, {"frontend/index.js", []byte("globalThis.installationFixture=true;")}} {
		require.NoError(t, archive.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o755, Typeflag: tar.TypeReg, Size: int64(len(entry.data))}))
		_, err := archive.Write(entry.data)
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())
	return buffer.Bytes()
}
