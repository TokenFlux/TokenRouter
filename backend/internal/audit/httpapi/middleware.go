package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
)

// middleware.go captures management request outcomes and restores inspected
// request bodies for downstream handlers.
// Audit capture surrounds management handlers so the stored event can include
// the final status and handler-supplied actor overrides. Mutating methods are
// captured by default; selected reads receive fixed actions, and credential-bearing
// routes omit bodies that key-based JSON redaction cannot safely inspect. Successful
// token refreshes are excluded after the handler runs to limit routine event volume.
//
// Reading a bounded prefix advances the original body before JSON binding occurs.
// The middleware therefore restores that prefix ahead of the remaining stream,
// including when the prefix read reports an error. Close still reaches the
// original body. Authentication metadata is read after handler execution, with
// positive actor IDs and nonempty emails supplied by handlers taking precedence.

// Handlers and authentication middleware use these Gin context keys to supply audit metadata.
const (
	auditCtxKeyAction     = "audit_action"      // handler-selected operation name
	auditCtxKeyActorID    = "audit_actor_id"    // handler-identified actor user
	auditCtxKeyActorEmail = "audit_actor_email" // handler-identified actor email
	auditCtxKeySkip       = "audit_skip"        // suppress middleware recording
	// ContextKeyAuthEmail holds the email supplied by authentication middleware.
	ContextKeyAuthEmail = "auth_email"
)

var (
	// auditSensitiveReads selects GET method/route pairs that expose sensitive data
	// and assigns their stable actions before the handler executes the read.
	auditSensitiveReads = map[string]string{
		"GET /api/v1/admin/mcp/tokens/:id/credential": "admin.mcp.tokens.copy",
		"GET /api/v1/admin/mcp/files/:id":             "admin.mcp.files.download",
		"GET /api/v1/admin/mcp/confirmations/:id":     "admin.mcp.confirmations.read",
		"GET /api/v1/admin/providers/data":            "admin.providers.export",
		"GET /api/v1/admin/proxies/data":              "admin.proxies.export",
		"GET /api/v1/admin/redeem-codes/export":       "admin.redeem_codes.export",
		"GET /api/v1/admin/backups/:id/download-url":  "admin.backups.download",
		"GET /api/v1/admin/backups/:id/download":      "admin.backups.download",
		"GET /api/v1/admin/settings/admin-api-key":    "admin.admin_api_key.read",
		"GET /api/v1/admin/users/:id/api-keys":        "admin.users.api_keys.read",
		"GET /api/v1/admin/groups/:id/api-keys":       "admin.groups.api_keys.read",
		"GET /api/v1/admin/backups/s3-config":         "admin.backups.s3_config.read",
		"GET /api/v1/admin/backups/storage-config":    "admin.backups.storage_config.read",
		"GET /api/v1/admin/data-management/s3/config": "admin.data_management.s3_config.read",
	}

	// auditActionOverrides supplies stable mutation actions when generic method and
	// route derivation would obscure the authentication or management operation being recorded.
	auditActionOverrides = map[string]string{
		"POST /api/v1/auth/login":                                 audit.AuditActionLogin,
		"POST /api/v1/auth/login/2fa":                             audit.AuditActionLogin2FA,
		"POST /api/v1/auth/register":                              audit.AuditActionRegister,
		"POST /api/v1/auth/refresh":                               audit.AuditActionTokenRefresh,
		"POST /api/v1/user/totp/step-up":                          audit.AuditActionStepUpVerify,
		"POST /api/v1/admin/audit-logs/clear":                     audit.AuditActionAuditLogClear,
		"POST /api/v1/admin/providers/data":                       "admin.providers.import",
		"POST /api/v1/admin/providers/:id/upstream-usage/query":   "admin.providers.upstream_usage.query",
		"POST /api/v1/admin/providers/upstream-usage/query/batch": "admin.providers.upstream_usage.query_batch",
		"POST /api/v1/admin/backups":                              "admin.backups.create",
		"POST /api/v1/admin/backups/:id/restore":                  "admin.backups.restore",
		"DELETE /api/v1/admin/backups/:id":                        "admin.backups.delete",
		"PUT /api/v1/admin/backups/s3-config":                     "admin.backups.s3_config.update",
		"POST /api/v1/admin/settings/admin-api-key/regenerate":    "admin.admin_api_key.regenerate",
		"DELETE /api/v1/admin/settings/admin-api-key":             "admin.admin_api_key.delete",
		"POST /api/v1/subscriptions/:id/revoke":                   audit.AuditActionUserSubscriptionRevoke,
	}

	// auditBodyOmittedRoutes lists routes with opaque uploads or credentials whose field names are chosen dynamically.
	// Key-based redaction cannot safely inspect these values, so the entire body is omitted.
	auditBodyOmittedRoutes = map[string]struct{}{
		"PUT /api/v1/admin/plugins/:id/config":                       {},
		"POST /api/v1/admin/plugins/uploads":                         {},
		"POST /api/v1/admin/mcp/oauth/requests/:id/decision":         {},
		"POST /api/v1/admin/mcp/files":                               {},
		"POST /api/v1/auth/passkey/login/finish":                     {},
		"POST /api/v1/user/passkeys/register/finish":                 {},
		"POST /api/v1/admin/providers/import/codex-session":          {},
		"PUT /api/v1/admin/providers/:id/ollama-cloud-usage/session": {},
	}
)

// AuditLogMiddleware identifies the management audit wrapper for application dependency injection.
type AuditLogMiddleware gin.HandlerFunc

// restoredBody replays captured bytes before continuing reads from the original body.
// Close reaches the original body even when a handler leaves the restored stream unread.
type restoredBody struct {
	// Reader replays the consumed prefix before the unread original stream; closer
	// retains the original body so net/http cleanup still releases that body's resources.
	io.Reader           // complete reconstructed request stream
	closer    io.Closer // original request body closer
}

// SetAuditAction supplies a handler-selected action for the current request's
// audit record. Middleware reads it after c.Next returns; an empty action leaves
// the fixed or route-derived operation name in effect.
func SetAuditAction(c *gin.Context, action string) {
	c.Set(auditCtxKeyAction, action)
}

// SetAuditActor supplies identified actor metadata during a handler, including
// login before authentication. Positive user IDs and nonempty emails override
// authentication metadata when middleware builds the completed request's audit record.
func SetAuditActor(c *gin.Context, userID int64, email string) {
	if userID > 0 {
		c.Set(auditCtxKeyActorID, userID)
	}
	if email != "" {
		c.Set(auditCtxKeyActorEmail, email)
	}
}

// SkipAudit suppresses middleware recording for the current handled management request.
func SkipAudit(c *gin.Context) {
	c.Set(auditCtxKeySkip, true)
}

// NewAuditLogMiddleware captures mutations and selected sensitive reads after
// their handlers complete. Mount it after authentication on administrator, user,
// and administrator-payment groups so rejected authentication is excluded. The
// unauthenticated auth group also records failed login, registration, and refresh attempts.
//
// Bounded body reads are restored before downstream binding, and redaction runs
// only when capture succeeds. Handler action and actor overrides are consumed
// after execution; SkipAudit suppresses recording when a synchronous operation
// already retained its trace. Successful refreshes are skipped, and accepted audit
// records are handed to the service's asynchronous writer.
// Provide a nonnil redactor for JSON body capture; a nil audit service skips
// enqueueing through Record's nil-receiver handling.
func NewAuditLogMiddleware(auditService *audit.AuditLogService, redactor *audit.Redactor) AuditLogMiddleware {
	return AuditLogMiddleware(func(c *gin.Context) {
		routeKey := c.Request.Method + " " + c.FullPath()

		record := false
		action := ""
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			record = true
			if v, ok := auditActionOverrides[routeKey]; ok {
				action = v
			}
		case http.MethodGet:
			if v, ok := auditSensitiveReads[routeKey]; ok {
				record = true
				action = v
			}
		}
		if !record {
			_, record = authctx.GetMCPDispatch(c.Request.Context())
		}
		if !record {
			c.Next()
			return
		}

		// Restore captured bytes so downstream JSON binding can read the complete body.
		// Read only the capture limit plus one byte and retain the unread stream,
		// avoiding a second full in-memory copy of large import bodies.
		var bodyRedacted string
		if _, omit := auditBodyOmittedRoutes[routeKey]; omit {
			// Credentials embedded inside arbitrary strings cannot be identified by JSON
			// key matching, so these routes retain an omission marker for the entire body.
			bodyRedacted = "<credential-bearing body omitted>"
		} else if c.Request.Body != nil && c.Request.Method != http.MethodGet {
			orig := c.Request.Body
			raw, err := io.ReadAll(io.LimitReader(orig, audit.AuditRequestBodyCaptureLimit+1))
			c.Request.Body = &restoredBody{
				Reader: io.MultiReader(bytes.NewReader(raw), orig),
				closer: orig,
			}
			// A partial read leaves audit body text unset, because redacting an incomplete
			// document cannot establish which values are safe. Restored bytes still reach binding.
			if err == nil {
				bodyRedacted = redactor.RedactBody(raw, c.GetHeader("Content-Type"))
			}
		}

		start := time.Now()
		c.Next()

		if c.GetBool(auditCtxKeySkip) {
			return
		}

		status := c.Writer.Status()
		// Successful token refreshes are routine high-volume events. Retain failed
		// refreshes so attempts that return errors remain available for later audit investigation.
		if routeKey == "POST /api/v1/auth/refresh" && status < 400 {
			return
		}

		entry := &audit.AuditLog{
			CreatedAt:   time.Now().UTC(),
			Action:      action,
			Method:      c.Request.Method,
			Path:        c.FullPath(),
			ClientIP:    identityhttp.SecurityClientIP(c),
			UserAgent:   c.Request.UserAgent(),
			RequestBody: bodyRedacted,
			StatusCode:  status,
			LatencyMs:   time.Since(start).Milliseconds(),
		}
		if entry.Path == "" {
			// An unmatched route has no Gin template. Retain the request path so a
			// failed mutation can still be identified in audit searches after routing fails.
			entry.Path = c.Request.URL.Path
		}
		if entry.Action == "" {
			// Routes outside the fixed action maps still need searchable operation names;
			// derive one from the method and route before applying a handler's override.
			entry.Action = deriveAuditAction(c.Request.Method, entry.Path)
		}
		if v, ok := c.Get(auditCtxKeyAction); ok {
			if s, ok := v.(string); ok && s != "" {
				entry.Action = s
			}
		}
		if requestID, ok := c.Request.Context().Value(telemetry.RequestID).(string); ok {
			entry.RequestID = requestID
		}

		// Read the authenticated user, role, email, and method before handler overrides.
		// Login handlers can identify an actor even when the request began unauthenticated.
		if subject, ok := authctx.GetAuthSubjectFromContext(c); ok && subject.UserID > 0 {
			uid := subject.UserID
			entry.ActorUserID = &uid
		}
		if role, ok := authctx.GetUserRoleFromContext(c); ok {
			entry.ActorRole = role
		}
		entry.ActorEmail = c.GetString(ContextKeyAuthEmail)
		entry.AuthMethod = c.GetString("auth_method")
		if entry.AuthMethod == "" && entry.ActorUserID != nil {
			// An authenticated user without an explicit method is recorded as a JWT
			// actor; login-time actor overrides occur later and do not trigger this inference.
			entry.AuthMethod = audit.AuditAuthMethodJWT
		}
		if v, ok := c.Get(auditCtxKeyActorID); ok {
			if id, ok := v.(int64); ok && id > 0 {
				entry.ActorUserID = &id
			}
		}
		if v, ok := c.Get(auditCtxKeyActorEmail); ok {
			if s, ok := v.(string); ok && s != "" {
				entry.ActorEmail = s
			}
		}

		// Mask the header credential before enqueueing so CredentialMasked holds its
		// display form; fields such as route parameters follow their own capture rules below.
		entry.CredentialMasked = MaskedRequestCredential(c)

		extra := map[string]any{}
		if dispatch, ok := authctx.GetMCPDispatch(c.Request.Context()); ok {
			extra["mcp_token_id"] = dispatch.TokenID
			extra["mcp_source"] = dispatch.Source
			extra["mcp_operation_id"] = dispatch.OperationID
			extra["mcp_confirmation_skipped"] = dispatch.ConfirmationSkipped
			entry.CredentialMasked = c.GetString("mcp_token_prefix")
		} else if tokenID := c.GetInt64("mcp_related_token_id"); tokenID > 0 {
			// Browser approvals and downloads retain JWT actor authentication while
			// indexing the originating token, so its history includes human continuation actions.
			extra["mcp_token_id"] = tokenID
			extra["mcp_source"] = c.GetString("mcp_related_source")
			extra["mcp_operation_id"] = c.GetString("mcp_related_operation_id")
			entry.CredentialMasked = c.GetString("mcp_related_token_prefix")
		}
		if len(c.Params) > 0 {
			params := make(map[string]string, len(c.Params))
			for _, p := range c.Params {
				params[p.Key] = p.Value
			}
			extra["params"] = params
		}
		if q := audit.RedactAuditQuery(c.Request.URL.RawQuery); q != "" {
			extra["query"] = q
		}
		if len(extra) > 0 {
			entry.Extra = extra
		}

		auditService.Record(entry)
	})
}

// Close forwards cleanup to the original request body and returns its close error.
// It can be called before the restored stream is exhausted, so cleanup does not
// wait for downstream body reads.
func (b *restoredBody) Close() error { return b.closer.Close() }

// MaskedRequestCredential returns a masked display credential from request headers.
// A nonempty x-api-key takes precedence over Authorization; authorization schemes
// are retained when separated by a space, and missing headers yield an empty string.
func MaskedRequestCredential(c *gin.Context) string {
	if apiKey := strings.TrimSpace(c.GetHeader("x-api-key")); apiKey != "" {
		return "x-api-key " + audit.MaskAuditCredential(apiKey)
	}
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) == 2 {
		return parts[0] + " " + audit.MaskAuditCredential(strings.TrimSpace(parts[1]))
	}
	return audit.MaskAuditCredential(authHeader)
}

// deriveAuditAction converts an HTTP method and route template into an audit
// operation name, such as admin.providers.update for PUT on the provider detail
// route. Dynamic route segments are omitted, hyphens become underscores, and an
// empty path returns the method's action verb alone.
func deriveAuditAction(method, fullPath string) string {
	path := strings.TrimPrefix(fullPath, "/api/v1/")
	path = strings.Trim(path, "/")
	segs := strings.Split(path, "/")
	parts := make([]string, 0, len(segs))
	for _, seg := range segs {
		// Parameter and catch-all segments describe resource identities rather than
		// operation categories, so removing them keeps action filters stable across individual requests.
		if seg == "" || strings.HasPrefix(seg, ":") || strings.HasPrefix(seg, "*") {
			continue
		}
		parts = append(parts, strings.ReplaceAll(seg, "-", "_"))
	}
	verb := ""
	switch method {
	case "POST":
		verb = "create"
	case "PUT", "PATCH":
		verb = "update"
	case "DELETE":
		verb = "delete"
	case "GET":
		verb = "read"
	default:
		// Methods without a predefined CRUD action retain their lowercase spelling,
		// giving unusual method requests a searchable name instead of an empty action suffix.
		verb = strings.ToLower(method)
	}
	if len(parts) == 0 {
		return verb
	}
	return strings.Join(parts, ".") + "." + verb
}
