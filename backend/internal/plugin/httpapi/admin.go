package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// admin.go maps plugin administration to bounded panel HTTP requests and explicit
// response DTOs. Installation records contain encrypted secrets and internal
// paths, so responses select public fields instead of serializing domain records.
// Configuration responses come through the service's secret-presence projection.
//
// Authentication and step-up guards are installed by the route registrar before
// handlers run. Mutations take actor identity from that authenticated context,
// and uploaded archives have both a request bound and a package-format bound.
// Multipart temporary files are removed after every upload attempt. Errors use
// the shared panel mapper, preserving stable reasons without returning internal
// database or filesystem causes as response text.

// Handler serves plugin management with shared lifecycle and diagnostic readers.
type Handler struct {
	service *plugin.Service        // serialized plugin administration operations
	manager *plugin.Manager        // local runtime state and restart owner
	logs    plugin.LogReader       // bounded retained process diagnostics
	catalog *plugin.CatalogService // source ownership and release recommendations
}

// InstallationDTO exposes accepted metadata without ciphertext or local storage paths.
type InstallationDTO struct {
	IgnoreHostVersion    bool                `json:"ignore_host_version"`           // gateway version bounds were explicitly waived
	IndexName            string              `json:"index_name,omitempty"`          // registered source display name
	IndexBuiltin         bool                `json:"index_builtin"`                 // original source is builtin
	SignatureStatus      string              `json:"signature_status,omitempty"`    // latest source verification outcome
	UpdateVersion        string              `json:"update_version,omitempty"`      // compatible offered newer release
	CurrentVersionYanked bool                `json:"current_version_yanked"`        // installed release was withdrawn
	CurrentYankReason    string              `json:"current_yank_reason,omitempty"` // published withdrawal explanation
	Delisted             bool                `json:"delisted"`                      // original source withdrew listing
	Tasks                []plugin.TaskStatus `json:"tasks,omitempty"`               // in-memory declared task execution observations
	PluginID             string              `json:"plugin_id"`                     // publisher-qualified installation identity
	Version              string              `json:"version"`                       // installed release SemVer
	Source               plugin.Source       `json:"source"`                        // accepted package distribution source
	IndexID              *int64              `json:"index_id"`                      // optional distribution index identifier
	Manifest             *manifest.Manifest  `json:"manifest"`                      // installed release declaration
	ManifestSHA256       string              `json:"manifest_sha256"`               // original declaration asset digest
	Enabled              bool                `json:"enabled"`                       // desired runtime availability flag
	GrantedPermissions   []string            `json:"granted_permissions"`           // administrator-confirmed capability names
	Limits               json.RawMessage     `json:"limits"`                        // administrator capability quota configuration
	PreviousVersion      string              `json:"previous_version,omitempty"`    // retained rollback release version
	Status               plugin.Status       `json:"status"`                        // local process lifecycle observation
	InstalledAt          time.Time           `json:"installed_at"`                  // first installation creation time
	UpdatedAt            time.Time           `json:"updated_at"`                    // latest persisted modification time
}

// ChangeDTO reports a saved installation and any remaining configuration requirement.
type ChangeDTO struct {
	Installation       InstallationDTO `json:"installation"`        // accepted installation and runtime state
	NeedsConfiguration bool            `json:"needs_configuration"` // required settings remain incomplete
	Repaired           bool            `json:"repaired"`            // missing uploaded package was restored
}

// UploadDTO contains the reviewable archive identity and its expiring installation token.
type UploadDTO struct {
	UploadToken    string             `json:"upload_token"`    // opaque staged archive handle
	ExpiresAt      time.Time          `json:"expires_at"`      // staged archive expiration timestamp
	Manifest       *manifest.Manifest `json:"manifest"`        // validated uploaded release declaration
	ManifestSHA256 string             `json:"manifest_sha256"` // uploaded declaration asset digest
	Platform       string             `json:"platform"`        // validated archive target platform
}

// ConfigInput wraps dynamic schema values so audit and confirmation can redact them together.
type ConfigInput struct {
	Values map[string]any `json:"values" binding:"required"` // administrator-submitted configuration field values
}

// NewHandler constructs administration using the application's shared plugin owners.
func NewHandler(service *plugin.Service, manager *plugin.Manager, logs plugin.LogReader, catalog *plugin.CatalogService) *Handler {
	return &Handler{service: service, manager: manager, logs: logs, catalog: catalog}
}

// List returns installed metadata and status without decrypting stored configuration.
func (h *Handler) List(c *gin.Context) {
	items, err := h.service.List(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	var entries map[string]plugin.CatalogEntry
	if h.catalog != nil {
		entries, err = h.catalog.InstalledEntries(c.Request.Context(), items)
		if err != nil {
			httpx.ErrorFrom(c, err)
			return
		}
	}
	result := make([]InstallationDTO, 0, len(items))
	for i := range items {
		item := installationDTO(&items[i], h.manager.Status(items[i].PluginID))
		item.Tasks = h.manager.TaskStatuses(items[i].PluginID)
		if entry, exists := entries[item.PluginID]; exists {
			item.IndexName, item.IndexBuiltin = entry.IndexName, entry.Builtin
			item.SignatureStatus, item.UpdateVersion = entry.SignatureStatus, entry.UpdateVersion
			item.CurrentVersionYanked, item.CurrentYankReason, item.Delisted = entry.CurrentVersionYanked, entry.CurrentYankReason, entry.Delisted
		}
		result = append(result, item)
	}
	httpx.Success(c, result)
}

// Get returns one installation's declaration, grants, limits, and current runtime status.
func (h *Handler) Get(c *gin.Context) {
	item, err := h.service.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	result := installationDTO(item, h.manager.Status(item.PluginID))
	result.Tasks = h.manager.TaskStatuses(item.PluginID)
	httpx.Success(c, result)
}

// Upload validates a bounded multipart archive and returns its review metadata.
// Only the file field is consumed. Multipart disk spill is removed after success
// or failure, and no uploaded plugin code executes during this preview operation.
func (h *Handler) Upload(c *gin.Context) {
	// Multipart boundaries need a small allowance beyond the compressed package cap; the package parser independently enforces the exact archive limit.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65<<20)
	defer func() {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
	}()
	if err := c.Request.ParseMultipartForm(8 << 20); err != nil {
		httpx.ErrorFrom(c, plugin.ErrPackageInvalid)
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		httpx.ErrorFrom(c, plugin.ErrPackageInvalid)
		return
	}
	defer func() { _ = file.Close() }()
	if header.Size > 64<<20 {
		httpx.ErrorFrom(c, plugin.ErrPackageInvalid)
		return
	}
	upload, err := h.service.Preview(c.Request.Context(), file)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, UploadDTO{
		UploadToken:    upload.Token,
		ExpiresAt:      upload.ExpiresAt,
		Manifest:       upload.Snapshot.Manifest,
		ManifestSHA256: upload.Snapshot.ManifestSHA256,
		Platform:       upload.Platform,
	})
}

// Install accepts a reviewed release and returns the persisted installation outcome.
func (h *Handler) Install(c *gin.Context) {
	actor, ok := actorID(c)
	if !ok {
		return
	}
	var input plugin.InstallInput
	if !decodeBody(c, &input) {
		return
	}
	result, err := h.service.Install(c.Request.Context(), input, actor)
	writeChange(c, result, err)
}

// Enable validates required settings before requesting a running plugin generation.
func (h *Handler) Enable(c *gin.Context) {
	actor, ok := actorID(c)
	if !ok {
		return
	}
	result, err := h.service.Enable(c.Request.Context(), c.Param("id"), actor)
	writeChange(c, result, err)
}

// Disable persists disabled intent and waits for admitted work to drain.
func (h *Handler) Disable(c *gin.Context) {
	actor, ok := actorID(c)
	if !ok {
		return
	}
	result, err := h.service.Disable(c.Request.Context(), c.Param("id"), actor)
	writeChange(c, result, err)
}

// Restart reloads one installation after draining its previous process generation.
func (h *Handler) Restart(c *gin.Context) {
	if err := h.manager.Restart(c.Request.Context(), c.Param("id")); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, h.manager.Status(c.Param("id")))
}

// Delete removes installation intent after draining, optionally purging retained KV.
// Missing purge_data retains business state. Invalid boolean text fails before
// draining or deletion, preventing malformed query values from selecting a purge.
func (h *Handler) Delete(c *gin.Context) {
	var purge bool
	if value, ok := c.GetQuery("purge_data"); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			httpx.BadRequest(c, "purge_data must be a boolean")
			return
		}
		purge = parsed
	}
	if err := h.service.Uninstall(c.Request.Context(), c.Param("id"), purge); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, gin.H{"deleted": true})
}

// Config returns ordinary configuration fields and presence markers for secret fields.
func (h *Handler) Config(c *gin.Context) {
	values, err := h.service.Config(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, gin.H{"values": values})
}

// SetConfig validates bounded configuration without returning submitted secret values.
// Omitted secrets are preserved by the service. The response rereads only the
// public presence projection after the persisted change and runtime notification.
func (h *Handler) SetConfig(c *gin.Context) {
	actor, ok := actorID(c)
	if !ok {
		return
	}
	var input ConfigInput
	if !decodeBody(c, &input) {
		return
	}
	if input.Values == nil {
		httpx.ErrorFrom(c, plugin.ErrConfigInvalid)
		return
	}
	if err := h.service.SetConfig(c.Request.Context(), c.Param("id"), input.Values, actor); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	h.Config(c)
}

// Limits returns the resolved administrator policy including conservative omitted defaults.
func (h *Handler) Limits(c *gin.Context) {
	limits, err := h.service.Limits(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, limits)
}

// SetLimits validates and persists a complete or partially defaulted capability policy.
func (h *Handler) SetLimits(c *gin.Context) {
	actor, ok := actorID(c)
	if !ok {
		return
	}
	var input json.RawMessage
	if !decodeBody(c, &input) {
		return
	}
	if err := h.service.SetLimits(c.Request.Context(), c.Param("id"), input, actor); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	h.Limits(c)
}

// Logs returns bounded diagnostics only for an existing administrative installation record.
func (h *Handler) Logs(c *gin.Context) {
	if _, err := h.service.Get(c.Request.Context(), c.Param("id")); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	lines := h.logs.Logs(c.Param("id"))
	if lines == nil {
		lines = []plugin.LogLine{}
	}
	httpx.Success(c, lines)
}

// installationDTO selects public installation fields and excludes all stored secret representations.
func installationDTO(item *plugin.Installation, status plugin.Status) InstallationDTO {
	limits := item.Limits
	if len(limits) == 0 {
		limits = json.RawMessage(`{}`)
	}
	result := InstallationDTO{
		PluginID:           item.PluginID,
		Version:            item.Version,
		Source:             item.Source,
		IndexID:            item.IndexID,
		Manifest:           item.Manifest,
		ManifestSHA256:     item.ManifestSHA256,
		IgnoreHostVersion:  item.IgnoreHostVersion,
		Enabled:            item.Enabled,
		GrantedPermissions: item.GrantedPermissions,
		Limits:             limits,
		Status:             status,
		InstalledAt:        item.InstalledAt,
		UpdatedAt:          item.UpdatedAt,
	}
	if item.Previous != nil {
		result.PreviousVersion = item.Previous.Version
	}
	return result
}

// writeChange serializes one accepted mutation through the safe installation response projection.
func writeChange(c *gin.Context, result *plugin.ChangeResult, err error) {
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, ChangeDTO{
		Installation:       installationDTO(result.Installation, result.Status),
		NeedsConfiguration: result.NeedsConfiguration,
		Repaired:           result.Repaired,
	})
}

// actorID reads authenticated identity instead of accepting administrator attribution in JSON.
func actorID(c *gin.Context) (int64, bool) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		httpx.Unauthorized(c, "Authenticated administrator is required")
		return 0, false
	}
	return subject.UserID, true
}

// decodeBody accepts one bounded JSON value while preserving exact numeric input.
// Unknown object fields and trailing values fail. Errors omit parser text because
// it can include administrator-supplied secret bytes from malformed JSON documents.
func decodeBody(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.BadRequest(c, "Invalid plugin request body")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		httpx.BadRequest(c, "Plugin request body must contain one JSON value")
		return false
	}
	return true
}
