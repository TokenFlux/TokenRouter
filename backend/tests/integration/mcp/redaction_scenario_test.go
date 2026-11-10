package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	audithttp "github.com/TokenFlux/TokenRouter/internal/audit/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	mcphttp "github.com/TokenFlux/TokenRouter/internal/mcp/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerhttp "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	providerdto "github.com/TokenFlux/TokenRouter/internal/provider/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// redaction_scenario_test.go joins MCP transport, routing and audit handlers,
// provider DTOs, and payment credential declarations. Immutable in-memory repositories
// replace persistence; all calls use MCP HTTP, token admission, management
// catalog, business DTO conversion, and final MCP result serialization.

var (
	// Test-only routes expose the same protocol catalog and domain credential
	// declarations that production assembly supplies, without starting application resources.
	protocolOperation = adminapi.Define(adminapi.Operation{
		ID: "protocol_capabilities.list", Method: http.MethodGet, Path: "/api/v1/admin/protocol-capabilities",
		ReadOnly: true, Response: adminapi.ResponseJSON,
		Input: adminapi.Arguments("/api/v1/admin/protocol-capabilities", nil, nil),
	})
	credentialOperation = adminapi.Define(adminapi.Operation{
		ID: "fixture.credentials.list", Method: http.MethodGet, Path: "/api/v1/admin/fixture-credentials",
		ReadOnly: true, Response: adminapi.ResponseJSON,
		Input: adminapi.Arguments("/api/v1/admin/fixture-credentials", nil, nil),
	})
)

// pricingRepository supplies one configuration to the real pricing service.
type pricingRepository struct {
	routing.PricingConfigRepository                        // unused pricing persistence methods
	config                          *routing.PricingConfig // immutable fixture configuration
}

// auditRepository supplies one stored audit entry to the real query service.
type auditRepository struct {
	audit.AuditLogRepository                 // unused audit persistence methods
	entry                    *audit.AuditLog // immutable fixture audit entry
}

// tokenRepository supplies an immutable administrator grant for MCP authentication.
type tokenRepository struct {
	identity.MCPTokenRepository                   // unused credential persistence methods
	token                       identity.MCPToken // fixed management permission grant
	digest                      string            // accepted credential SHA-256 digest
}

// userReader supplies the active administrator owning the fixture token.
type userReader struct{}

// GetByID returns the fixture configuration without changing its stored values.
func (r pricingRepository) GetByID(context.Context, int64) (*routing.PricingConfig, error) {
	return r.config, nil
}

// GetByID returns the fixture audit entry without changing its stored metadata.
func (r auditRepository) GetByID(context.Context, int64) (*audit.AuditLog, error) {
	return r.entry, nil
}

// Get returns a grant copy for the fixture's exact token identity.
// Permissions remain shared and immutable throughout this read-only scenario;
// callers receive a separate token value for request-local authorization metadata.
func (r tokenRepository) Get(_ context.Context, id int64) (*identity.MCPToken, error) {
	if id != r.token.ID {
		return nil, identity.ErrMCPTokenNotFound
	}
	value := r.token
	return &value, nil
}

// FindHash validates the fixture digest before returning its management grant.
func (r tokenRepository) FindHash(ctx context.Context, digest string) (*identity.MCPToken, error) {
	if digest != r.digest {
		return nil, identity.ErrMCPTokenInvalid
	}
	return r.Get(ctx, r.token.ID)
}

// Touch accepts admission observations without modifying the immutable test grant.
func (tokenRepository) Touch(context.Context, int64) error { return nil }

// GetByID returns an active administrator for the supplied fixture identity.
func (userReader) GetByID(_ context.Context, id int64) (*identity.User, error) {
	return &identity.User{ID: id, Role: identity.RoleAdmin, Status: identity.StatusActive}, nil
}

// TestMCPMetadataAcrossHandlers executes real pricing, protocol, and audit handlers
// and serializes provider DTOs. Public metadata keeps its original JSON types,
// while domain-owned credentials and secrets adjacent to metadata stay masked.
func TestMCPMetadataAcrossHandlers(t *testing.T) {
	limit, zero := 256000, 0.0
	row := &routing.PricingConfig{
		ID: 1, Name: "fixture", Status: "active",
		ModelPricing: []routing.ModelPricingEntry{{Models: []string{"fixture-model"}, Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: &limit}, {MinTokens: limit, MaxTokens: nil},
		}}},
	}
	catalog := &routing.PricingCatalog{Snapshot: func() routing.DefaultPricingSnapshot {
		return routing.DefaultPricingSnapshot{Prices: []pricing.DefaultModelPrice{{
			Model: "fixture-model", Platform: "openai", BillingMode: "token",
			Prices: []pricing.DefaultPriceValue{{Key: "input", Value: &zero, Unit: "USD/MTok"}},
			ContextIntervals: []pricing.DefaultPriceInterval{{
				MinTokens: limit, MaxTokens: nil,
				Prices: []pricing.DefaultPriceValue{{Key: "output", Value: &zero, Unit: "USD/MTok"}},
			}},
		}}}
	}}
	pricingHandler := routinghttp.NewPricingHandler(routing.NewPricingConfigService(pricingRepository{config: row}, nil), catalog)
	entry := &audit.AuditLog{ID: 1, Extra: map[string]any{"mcp_token_id": int64(9007199254740993), "refresh_token": "private"}}
	auditService := audit.NewAuditLogService(auditRepository{entry: entry}, nil)
	t.Cleanup(auditService.Stop)
	auditHandler := audithttp.NewAuditLogHandler(auditService, nil)
	providerRecord := &provider.Record{
		Now: time.Now, LoadLocation: time.LoadLocation,
		ID: 1, Platform: provider.PlatformAntigravity, Type: provider.ProviderTypeOAuth,
		Credentials: map[string]any{"refresh_token": "private"},
		Extra: map[string]any{
			"session_token_present":                       true,
			provider.AntigravityForceTokenRefreshExtraKey: false,
			"refresh_token":                               "private",
		},
	}
	credential := "tr_mcp_" + strings.Repeat("a", 43)
	repository := tokenRepository{
		token: identity.MCPToken{ID: 1, UserID: 1, Enabled: true, RedactToolResults: true, Permissions: []string{
			"pricing.configs.get", "pricing.defaults.list", protocolOperation.ID, credentialOperation.ID,
			"audit_logs.get", "providers.get",
		}},
		digest: identity.MCPTokenDigest(credential),
	}
	tokens := identity.NewMCPTokenService(repository, userReader{}, nil)
	handler := mcphttp.NewHandler(tokens, nil, nil, userReader{}, nil, mcphttp.Options{})
	router := gin.New()
	admin := identityhttp.MCPAdminAuth(tokens, func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
	group := router.Group("/api/v1/admin", admin)
	adminapi.Register(group, routinghttp.ManagementOperations["pricing.configs.get"], pricingHandler.GetByID)
	adminapi.Register(group, routinghttp.ManagementOperations["pricing.defaults.list"], pricingHandler.ListDefaultPricing)
	adminapi.Register(group, protocolOperation, routinghttp.NewProtocolCatalogHandler(nil))
	adminapi.Register(group, audithttp.ManagementOperations["audit_logs.get"], auditHandler.Get)
	adminapi.Register(group, providerhttp.ManagementOperations["providers.get"], func(c *gin.Context) {
		httpx.Success(c, providerdto.ProviderFromRecordShallow(providerRecord))
	})
	keys := append([]string(nil), provider.SensitiveCredentialKeys...)
	for _, fields := range payment.ConfigProviderSensitiveConfigFields {
		for key := range fields {
			keys = append(keys, key)
		}
	}
	secretRows := make([]map[string]any, 0)
	for _, value := range []any{"private", json.Number("123456"), true, []any{"private"}, map[string]any{"value": "private"}} {
		secrets := make(map[string]any)
		for _, key := range keys {
			secrets[key] = value
		}
		secretRows = append(secretRows, secrets)
	}
	_, presence := providerdto.RedactCredentials(secretRows[0])
	adminapi.Register(group, credentialOperation, func(c *gin.Context) {
		httpx.Success(c, gin.H{"rows": secretRows, "credentials_status": presence})
	})
	require.NoError(t, handler.Mount(router, admin, func(c *gin.Context) { c.Next() }))
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	for _, args := range []map[string]any{
		{"operation_id": "pricing.configs.get", "path": map[string]string{"id": "1"}},
		{"operation_id": "pricing.defaults.list"},
		{"operation_id": protocolOperation.ID},
		{"operation_id": credentialOperation.ID},
		{"operation_id": "audit_logs.get", "path": map[string]string{"id": "1"}},
		{"operation_id": "providers.get", "path": map[string]string{"id": "1"}},
	} {
		payload, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "read_operation", "arguments": args},
		})
		require.NoError(t, err)
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/mcp", bytes.NewReader(payload))
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer "+credential)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		decoder := json.NewDecoder(response.Body)
		decoder.UseNumber()
		var wire map[string]any
		decodeErr := decoder.Decode(&wire)
		require.NoError(t, response.Body.Close())
		require.NoError(t, decodeErr)
		require.Equal(t, http.StatusOK, response.StatusCode)
		require.NotContains(t, wire, "error")
		result, ok := wire["result"].(map[string]any)
		require.True(t, ok)
		require.NotEqual(t, true, result["isError"])
		data := result["structuredContent"]
		encoded, err := json.Marshal(data)
		require.NoError(t, err)
		text, ok := valueAt(t, result, "content", 0, "text").(string)
		require.True(t, ok)
		var textValue any
		textDecoder := json.NewDecoder(strings.NewReader(text))
		textDecoder.UseNumber()
		require.NoError(t, textDecoder.Decode(&textValue))
		textJSON, err := json.Marshal(textValue)
		require.NoError(t, err)
		require.Equal(t, string(encoded), string(textJSON))
		switch args["operation_id"] {
		case "pricing.configs.get":
			require.Equal(t, json.Number(strconv.Itoa(limit)), valueAt(t, data, "data", "data", "model_pricing", 0, "intervals", 0, "max_tokens"))
			require.Nil(t, valueAt(t, data, "data", "data", "model_pricing", 0, "intervals", 1, "max_tokens"))
		case "pricing.defaults.list":
			require.Equal(t, "input", valueAt(t, data, "data", "data", "items", 0, "prices", 0, "key"))
			require.Equal(t, "output", valueAt(t, data, "data", "data", "items", 0, "context_intervals", 0, "prices", 0, "key"))
			require.Nil(t, valueAt(t, data, "data", "data", "items", 0, "context_intervals", 0, "max_tokens"))
		case protocolOperation.ID:
			entries, ok := valueAt(t, data, "data", "data", "auxiliary_operations").([]any)
			require.True(t, ok)
			want := routinghttp.AdminProtocolCatalog(nil).AuxiliaryOperations
			require.Len(t, entries, len(want))
			for i, entry := range entries {
				require.Equal(t, want[i].Authorization, valueAt(t, entry, "authorization"))
			}
		case credentialOperation.ID:
			rows, ok := valueAt(t, data, "data", "data", "rows").([]any)
			require.True(t, ok)
			require.Len(t, rows, len(secretRows))
			for _, entry := range rows {
				for _, key := range keys {
					require.Equal(t, "***", valueAt(t, entry, key), key)
				}
			}
			for key := range presence {
				require.Equal(t, true, valueAt(t, data, "data", "data", "credentials_status", key), key)
			}
		case "audit_logs.get":
			require.Equal(t, json.Number("9007199254740993"), valueAt(t, data, "data", "data", "extra", "mcp_token_id"))
			require.Equal(t, "***", valueAt(t, data, "data", "data", "extra", "refresh_token"))
		case "providers.get":
			require.Equal(t, true, valueAt(t, data, "data", "data", "extra", "session_token_present"))
			require.Equal(t, false, valueAt(t, data, "data", "data", "extra", provider.AntigravityForceTokenRefreshExtraKey))
			require.Equal(t, "***", valueAt(t, data, "data", "data", "extra", "refresh_token"))
			require.Equal(t, true, valueAt(t, data, "data", "data", "credentials_status", "has_refresh_token"))
		}
	}
}

// valueAt follows exact object keys and array positions in decoded MCP JSON.
// Missing members fail the test instead of looking like explicit null values;
// scalar values, including json.Number, are returned without conversion.
func valueAt(t *testing.T, value any, path ...any) any {
	t.Helper()
	for _, segment := range path {
		switch key := segment.(type) {
		case string:
			object, ok := value.(map[string]any)
			require.True(t, ok)
			require.Contains(t, object, key)
			value = object[key]
		case int:
			array, ok := value.([]any)
			require.True(t, ok)
			require.Greater(t, len(array), key)
			value = array[key]
		default:
			t.Fatalf("unsupported path segment %T", segment)
		}
	}
	return value
}
