package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// publicRuntimeFixture exposes sequential request snapshots and lease ownership observations.
type publicRuntimeFixture struct {
	enabled   bool                     // host-wide contribution availability switch
	loaded    map[string]plugin.Loaded // immutable fixture installation views
	transport *publicTransportFixture  // captured contribution transport behavior
	version   string                   // optional acquired version override
	onAcquire func()                   // optional version publication interleaving
	acquired  int                      // successful fixture lease acquisitions
	released  int                      // completed fixture lease releases
}

// publicTransportFixture captures the filtered request and supplies a chosen plugin reply.
type publicTransportFixture struct {
	last     *plugin.HTTPRequest                                                      // most recent contribution request
	calls    int                                                                      // observed contribution invocation count
	exchange func(context.Context, *plugin.HTTPRequest) (*plugin.HTTPResponse, error) // optional response behavior
}

// publicProfilesFixture supplies only the account projection consumed by HTTP identity mapping.
type publicProfilesFixture struct {
	plugin.UserReader // unused profile query capabilities
}

// publicFilesFixture retains immutable versioned file content and counts descriptor cleanup.
type publicFilesFixture struct {
	packages map[string]plugin.Package // version-indexed local package metadata
	content  map[string][]byte         // directory and path indexed bytes
	opened   []string                  // requested declared asset paths
	closed   int                       // completed fixture descriptor closes
}

// publicFileReader wraps immutable fixture bytes with observable close ownership.
type publicFileReader struct {
	*bytes.Reader                     // bounded in-memory file content
	owner         *publicFilesFixture // descriptor cleanup observation owner
}

// publicSecurityFixture records dynamic authentication and rate-limiter selection.
type publicSecurityFixture struct {
	jwt         int  // user authentication invocation count
	admin       int  // administrator authentication invocation count
	public      int  // public-address limiter invocation count
	panel       int  // user-scoped limiter invocation count
	backendMode bool // restrict anonymous and user requests
}

// Enabled reports the fixture emergency availability switch without changing state.
func (f *publicRuntimeFixture) Enabled() bool { return f.enabled }

// Snapshot returns the retained immutable release view for this fixture identity.
func (f *publicRuntimeFixture) Snapshot(id string) (plugin.Loaded, bool) {
	loaded, found := f.loaded[id]
	return loaded, found
}

// Snapshots returns fixture views in the same deterministic order as the manager.
func (f *publicRuntimeFixture) Snapshots() []plugin.Loaded {
	result := make([]plugin.Loaded, 0, len(f.loaded))
	for _, item := range f.loaded {
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b plugin.Loaded) int { return strings.Compare(a.Installation.PluginID, b.Installation.PluginID) })
	return result
}

// AssetReference selects fixture archive metadata from the accepted current or previous manifest.
func (f *publicRuntimeFixture) AssetReference(_ context.Context, id, version string) (plugin.PackageRef, error) {
	loaded, found := f.loaded[id]
	if !found {
		return plugin.PackageRef{}, plugin.ErrPluginNotFound
	}
	if loaded.Installation.Version == version {
		return plugin.PackageRef{PluginID: id, Version: version, Icon: loaded.Installation.Manifest.Icon}, nil
	}
	previous := loaded.Installation.Previous
	if previous != nil && previous.Version == version {
		return plugin.PackageRef{PluginID: id, Version: version, Icon: previous.Manifest.Icon}, nil
	}
	return plugin.PackageRef{}, plugin.ErrPluginNotFound
}

// Acquire returns one fixture lease and optionally publishes a concurrent declaration change.
func (f *publicRuntimeFixture) Acquire(_ context.Context, id string) (*plugin.Lease, error) {
	version := f.version
	if version == "" {
		version = f.loaded[id].Installation.Version
	}
	f.acquired++
	if f.onAcquire != nil {
		f.onAcquire()
	}
	return plugin.NewLease(&plugin.Conn{HTTP: f.transport}, version, func() { f.released++ }), nil
}

// Exchange records the full filtered request before returning fixture output.
func (f *publicTransportFixture) Exchange(ctx context.Context, request *plugin.HTTPRequest) (*plugin.HTTPResponse, error) {
	f.calls++
	f.last = request
	if f.exchange != nil {
		return f.exchange(ctx, request)
	}
	return &plugin.HTTPResponse{Status: 201, Headers: map[string]string{"Content-Type": "application/json; charset=utf-8", "Set-Cookie": "private=leak", "X-Plugin-Result": "accepted"}, Body: []byte(`{"ok":true}`)}, nil
}

// Account returns public caller naming while retaining deliberately private unused profile fields.
func (publicProfilesFixture) Account(_ context.Context, id int64) (plugin.AccountView, error) {
	return plugin.AccountView{User: plugin.UserView{ID: id, Username: "Current username", Email: "private@example.invalid"}}, nil
}

// Local returns fixture metadata for an already retained version or missing-package error.
// The in-memory fixture ignores context cancellation. It never creates records,
// performs network recovery, or exposes versions outside its configured package map.
func (f *publicFilesFixture) Local(_ context.Context, ref plugin.PackageRef) (plugin.Package, error) {
	if ref.PluginID != "test.http" {
		return plugin.Package{}, plugin.ErrPackageMissing
	}
	item, ok := f.packages[ref.Version]
	if !ok {
		return plugin.Package{}, plugin.ErrPackageMissing
	}
	return item, nil
}

// Open enforces the fixture byte ceiling and records every acquired descriptor.
// Returned bytes remain immutable and belong to the fixture. Callers close the
// reader to update the cleanup observation after either serving or inspecting it.
func (f *publicFilesFixture) Open(_ context.Context, item plugin.Package, name string, maximum int64) (*plugin.AssetFile, error) {
	f.opened = append(f.opened, name)
	data, found := f.content[item.Directory+":"+name]
	if !found || int64(len(data)) > maximum {
		return nil, plugin.ErrPackageMissing
	}
	return &plugin.AssetFile{Reader: &publicFileReader{Reader: bytes.NewReader(data), owner: f}, Metadata: item.Files[name], ModifiedAt: time.Unix(1700000000, 0)}, nil
}

// Close records release of this fixture's bounded asset descriptor.
func (r *publicFileReader) Close() error {
	r.owner.closed++
	return nil
}

// guards supplies observable panel middleware with ordinary missing-credential rejection.
func (s *publicSecurityFixture) guards() PublicMiddleware {
	return PublicMiddleware{
		JWT: func(c *gin.Context) {
			s.jwt++
			s.authenticate(c, "user")
		},
		Admin: func(c *gin.Context) {
			s.admin++
			s.authenticate(c, "admin")
		},
		BackendUser: func(c *gin.Context) {
			role, _ := authctx.GetUserRoleFromContext(c)
			if s.backendMode && role != "admin" {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
		},
		PublicIP: func(c *gin.Context) {
			s.public++
			c.Next()
		},
		Panel: func(c *gin.Context) {
			s.panel++
			c.Next()
		},
	}
}

// authenticate installs a private verified principal or aborts with the expected panel denial.
func (s *publicSecurityFixture) authenticate(c *gin.Context, requiredRole string) {
	role := "user"
	switch c.GetHeader("Authorization") {
	case "Bearer user":
	case "Bearer admin":
		role = "admin"
	default:
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	if requiredRole == "admin" && role != "admin" {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	authctx.SetPrincipal(c, identity.Principal{UserID: 7, Role: role, CredentialKind: "jwt"}, 1, "private@example.invalid")
	c.Next()
}

// publicFixture assembles real contribution handlers with immutable package and security collaborators.
// Files remain in memory and no native process executes. The tests inspect the
// same registrar, guards, grant generation, and response filtering used by application assembly.
func publicFixture(t *testing.T) (*gin.Engine, *PublicHandler, *publicRuntimeFixture, *publicFilesFixture, *publicSecurityFixture) {
	t.Helper()
	declaration, err := manifest.Parse([]byte(`{"schemaVersion":1,"id":"test.http","version":"1.0.0","apiVersion":1,"minHostVersion":"0.5.0","name":{"en":"Plugin","zh":"插件"},"description":{"en":"Fixture"},"publisher":{"name":"Fixture"},"license":"MIT","backend":{"executable":"bin/plugin","routes":[{"method":"GET","path":"/public","auth":"public"},{"method":"POST","path":"/user","auth":"user","maxBodyBytes":8},{"method":"POST","path":"/admin","auth":"admin"},{"method":"GET","path":"/slow","auth":"user","timeoutSeconds":1},{"method":"POST","path":"/zero","auth":"user","maxBodyBytes":0},{"method":"*","path":"/wild/*","auth":"public"}]},"frontend":{"entry":"frontend/index.js","styles":["frontend/index.css"],"pages":[{"id":"home","path":"","audience":"user","title":{"en":"Home","zh":"首页"}},{"id":"admin","path":"settings","audience":"admin","title":{"en":"Settings","zh":"设置"}}],"menus":[{"page":"home","label":{"en":"Check-in","zh":"签到"},"icon":"frontend/menu.svg"},{"page":"admin","label":{"en":"Admin","zh":"管理"},"icon":"frontend/menu.svg"}]}}`))
	require.NoError(t, err)
	files := &publicFilesFixture{packages: make(map[string]plugin.Package), content: make(map[string][]byte)}
	for _, version := range []string{"1.0.0", "0.9.0"} {
		item := plugin.Package{Directory: "/fixture/" + version, Files: make(map[string]plugin.Asset)}
		for name, data := range map[string]string{"frontend/index.js": "window.fixture=true", "frontend/index.css": "body{}", "frontend/menu.svg": "<svg/>", "icon.svg": "<svg/>", "plugin.json": "private configuration", "bin/plugin": "private executable"} {
			digest := sha256.Sum256([]byte(data))
			integrity := sha512.Sum384([]byte(data))
			item.Files[name] = plugin.Asset{Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), SRI: "sha384-" + base64.StdEncoding.EncodeToString(integrity[:])}
			files.content[item.Directory+":"+name] = []byte(data)
		}
		files.packages[version] = item
	}
	installation := &plugin.Installation{PluginID: "test.http", Enabled: true, VersionSnapshot: plugin.VersionSnapshot{Version: "1.0.0", Manifest: declaration, ManifestSHA256: strings.Repeat("a", 64), Config: map[string]any{"private": "configuration-secret"}, SecretConfig: "ciphertext-secret"}}
	runtime := &publicRuntimeFixture{enabled: true, loaded: map[string]plugin.Loaded{"test.http": {Installation: installation, Package: files.packages["1.0.0"]}}, transport: &publicTransportFixture{}}
	grants, err := plugin.NewGrants("fixture-http-key", nil)
	require.NoError(t, err)
	handler := NewPublicHandler(PublicOptions{Runtime: runtime, Files: files, Profiles: publicProfilesFixture{}, Grants: grants, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), ClientIP: func(*gin.Context) string { return "203.0.113.7" }, Language: func(c *gin.Context) string { return c.GetHeader("Accept-Language") }})
	security := &publicSecurityFixture{}
	router := gin.New()
	RegisterPublicRoutes(router.Group("/api/v1"), handler, security.guards())
	return router, handler, runtime, files, security
}
