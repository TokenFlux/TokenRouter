package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// mcp_tokens.go issues manual MCP credentials and resolves current administrator
// permissions for manual tokens and OAuth application grants. Manual credentials
// use random bytes, stored verification digests, and encrypted values for browser
// copy actions. OAuth credential verification is delegated to the configured
// resolver, which returns the same permission representation with its source.
//
// Every authorization reads the current owner and grant, so revocation, expiry,
// disabling, and administrator demotion affect subsequent calls. Confirmation
// preferences and configuration timestamps accompany both sources; usage writes
// leave that configuration version unchanged. Browser and internal dispatch
// callers pass source-qualified identifiers so equal numeric IDs cannot select
// a grant from a different credential store.

var (
	// MCP token errors distinguish invalid input from credentials that cannot
	// authorize a request without exposing token existence or digest contents.
	ErrMCPTokenInvalid  = apperror.Unauthorized("MCP_TOKEN_INVALID", "MCP access token is invalid or expired")
	ErrMCPTokenNotFound = apperror.NotFound("MCP_TOKEN_NOT_FOUND", "MCP access token not found")
)

// MCPToken contains the permissions and metadata of a manual token or OAuth grant.
type MCPToken struct {
	// Source distinguishes OAuth grants from manual credentials; an empty value
	// retains the identity of manual tokens and existing serialized temporary records.
	Source string `json:"source,omitempty"` // OAuth or empty manual source
	// Credential accompanies the current internal OAuth dispatch and is excluded
	// from metadata, stored approval records, and every browser response.
	Credential string `json:"-"` // request-local opaque OAuth credential

	ID          int64    `json:"id"`          // persisted token identifier
	UserID      int64    `json:"user_id"`     // owning administrator identifier
	Name        string   `json:"name"`        // administrator chosen label
	Prefix      string   `json:"prefix"`      // non-secret display prefix
	Permissions []string `json:"permissions"` // exact authorized operation identifiers
	Enabled     bool     `json:"enabled"`     // current admission switch
	// SkipBrowserConfirmation permits sensitive MCP calls to run directly when
	// their existing HTTP route does not require recent browser TOTP verification.
	SkipBrowserConfirmation bool       `json:"skip_browser_confirmation"`
	ExpiresAt               *time.Time `json:"expires_at"`   // optional absolute expiry
	LastUsedAt              *time.Time `json:"last_used_at"` // most recent admitted request
	CreatedAt               time.Time  `json:"created_at"`   // initial creation instant
	UpdatedAt               time.Time  `json:"updated_at"`   // latest settings change
	RevokedAt               *time.Time `json:"revoked_at"`   // permanent revocation instant
}

// MCPTokenInput selects a complete token label, grant set, and expiry policy.
type MCPTokenInput struct {
	Name        string   `json:"name" binding:"required,max=120"` // administrator chosen label
	Permissions []string `json:"permissions"`                     // exact selected operation identifiers
	Enabled     bool     `json:"enabled"`                         // requested admission switch
	// SkipBrowserConfirmation opts into direct sensitive calls; omitted values
	// remain false and existing recent-TOTP requirements still govern those routes.
	SkipBrowserConfirmation bool       `json:"skip_browser_confirmation"`
	ExpiresAt               *time.Time `json:"expires_at"` // nil permits no expiry
	// ExpectedUpdatedAt makes updates conditional on the last observed configuration.
	// Nil keeps ordinary replacement semantics; creation does not consult this update precondition.
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}

// MCPTokenRepository persists token metadata and reads uncached authorization grants.
type MCPTokenRepository interface {
	// Create stores the digest and authenticated ciphertext with generated metadata.
	// Both secret representations are persisted with the row before returning its
	// generated identity and timestamps through the supplied token.
	Create(context.Context, *MCPToken, string, string) error
	// Credential reads ciphertext and its digest for one owned, unrevoked token.
	// Disabled and expired tokens remain copyable for their owner; callers separately
	// check the administrator's current account status before decrypting the result.
	Credential(context.Context, int64, int64) (ciphertext, digest string, err error)
	// List returns this administrator's tokens including previously revoked credentials.
	List(context.Context, int64) ([]MCPToken, error)
	// Get reads one token using its immutable persisted identifier.
	Get(context.Context, int64) (*MCPToken, error)
	// FindHash reads one token using its complete credential digest.
	FindHash(context.Context, string) (*MCPToken, error)
	// Update replaces metadata for a live token belonging to the supplied owner.
	Update(context.Context, int64, int64, MCPTokenInput) (*MCPToken, error)
	// Revoke permanently disables a token owned by the supplied administrator.
	Revoke(context.Context, int64, int64) error
	// Touch records successful admission without changing the token's configuration version.
	Touch(context.Context, int64) error
}

// MCPUserReader loads current account status before a credential grants administrator access.
type MCPUserReader interface {
	// GetByID returns the live account or an error for unavailable users.
	GetByID(context.Context, int64) (*User, error)
}

// MCPTokenService owns credential generation and current-owner authorization checks.
type MCPTokenService struct {
	repo  MCPTokenRepository // uncached credential persistence
	users MCPUserReader      // current account reader
	// cipher is absent when deployment encryption is ephemeral; issuance and
	// plaintext recovery then fail before producing a credential that cannot survive restart.
	cipher SecretEncryptor
	oauth  MCPOAuthResolver // optional OAuth credential resolver
}

// NewMCPTokenService binds grants, current accounts, and durable secret encryption.
// Passing nil for cipher disables issuance and plaintext recovery while retaining
// digest-based authorization and metadata management for already stored tokens.
func NewMCPTokenService(repo MCPTokenRepository, users MCPUserReader, cipher SecretEncryptor) *MCPTokenService {
	return &MCPTokenService{repo: repo, users: users, cipher: cipher}
}

// Create validates token metadata and issues a random administrator credential.
// Callers validate operation identifiers against the mounted catalogue first,
// with owner authorization established by the management route. The repository
// receives the digest and encrypted credential before the complete value is
// returned for immediate setup. Encryption failures prevent persistence, and a
// storage failure returns no credential to display. Subsequent copy actions use
// a separate owner-authorized read; lists and authentication retain metadata-only
// access so ordinary requests do not decrypt reusable credentials.
func (s *MCPTokenService) Create(ctx context.Context, userID int64, input MCPTokenInput) (*MCPToken, string, error) {
	if err := validateMCPTokenInput(input); err != nil {
		return nil, "", err
	}
	if s.cipher == nil {
		return nil, "", apperror.ServiceUnavailable("MCP_ENCRYPTION_NOT_CONFIGURED", "Configure a fixed security.secret_encryption_key before creating or copying MCP tokens")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, "", err
	}
	credential := "tr_mcp_" + base64.RawURLEncoding.EncodeToString(random)
	sealed, err := s.cipher.Encrypt(credential)
	if err != nil {
		return nil, "", apperror.InternalServer("MCP_CREDENTIAL_UNAVAILABLE", "MCP token credential is unavailable")
	}
	// Display prefixes retain eight random characters after the credential scheme marker.
	token := &MCPToken{
		UserID:                  userID,
		Name:                    strings.TrimSpace(input.Name),
		Prefix:                  credential[:15],
		Permissions:             slices.Clone(input.Permissions),
		Enabled:                 input.Enabled,
		SkipBrowserConfirmation: input.SkipBrowserConfirmation,
		ExpiresAt:               input.ExpiresAt,
	}
	if token.Permissions == nil {
		token.Permissions = []string{}
	}
	if err := s.repo.Create(ctx, token, MCPTokenDigest(credential), sealed); err != nil {
		return nil, "", err
	}
	return token, credential, nil
}

// Credential returns a complete token to its current administrator owner.
// Disabled or expired tokens can be copied for later renewal; revoked tokens
// and another owner's identifiers return not-found. The browser HTTP endpoint
// supplies the owner identity after authenticating an actual administrator session.
//
// Ciphertext is fetched separately from public metadata and verified against the
// stored authentication digest after decryption. A corrupt or mismatched value
// yields a fixed error without returning any partial credential or cipher details.
func (s *MCPTokenService) Credential(ctx context.Context, userID, id int64) (*MCPToken, string, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil || user == nil || !user.IsActive() || !user.IsAdmin() {
		return nil, "", ErrMCPTokenInvalid
	}
	token, err := s.repo.Get(ctx, id)
	if err != nil || token.UserID != userID || token.RevokedAt != nil {
		return nil, "", ErrMCPTokenNotFound
	}
	if s.cipher == nil {
		return nil, "", apperror.ServiceUnavailable("MCP_ENCRYPTION_NOT_CONFIGURED", "Configure a fixed security.secret_encryption_key before creating or copying MCP tokens")
	}
	sealed, digest, err := s.repo.Credential(ctx, userID, id)
	if err != nil {
		return nil, "", err
	}
	credential, err := s.cipher.Decrypt(sealed)
	if err != nil || MCPTokenDigest(credential) != digest {
		return nil, "", apperror.InternalServer("MCP_CREDENTIAL_UNAVAILABLE", "MCP token credential is unavailable")
	}
	return token, credential, nil
}

// Authenticate validates a presented credential and reloads its owning account.
// Disabled, expired, revoked, missing, and demoted accounts share one rejection;
// unavailable storage also rejects admission without treating missing state as authorized.
//
// @project-doc docs/interfaces/mcp_management.md#access_tokens
func (s *MCPTokenService) Authenticate(ctx context.Context, credential string) (*MCPToken, *User, error) {
	if !strings.HasPrefix(credential, "tr_mcp_") && s.oauth != nil {
		token, err := s.oauth.Authenticate(ctx, credential)
		if err != nil {
			return nil, nil, ErrMCPTokenInvalid
		}
		token.Credential = credential
		return s.authorize(ctx, token)
	}
	if !strings.HasPrefix(credential, "tr_mcp_") || len(credential) != 50 {
		return nil, nil, ErrMCPTokenInvalid
	}
	token, err := s.repo.FindHash(ctx, MCPTokenDigest(credential))
	if err != nil {
		return nil, nil, ErrMCPTokenInvalid
	}
	return s.authorize(ctx, token)
}

// AuthorizeID rechecks stored grants and owner status before an internal dispatch.
// The identifier comes from a previously verified request context, never from
// an external authentication header or model-supplied owner identifier.
func (s *MCPTokenService) AuthorizeID(ctx context.Context, id int64) (*MCPToken, *User, error) {
	token, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, nil, ErrMCPTokenInvalid
	}
	return s.authorize(ctx, token)
}

// authorize rejects stale credentials before resolving their current administrator owner.
// Usage timestamps are operational metadata: a timestamp write failure does not
// replace successful authorization, and no token configuration is changed here.
func (s *MCPTokenService) authorize(ctx context.Context, token *MCPToken) (*MCPToken, *User, error) {
	if !token.Enabled || token.RevokedAt != nil || token.ExpiresAt != nil && !time.Now().Before(*token.ExpiresAt) {
		return nil, nil, ErrMCPTokenInvalid
	}
	user, err := s.users.GetByID(ctx, token.UserID)
	if err != nil || user == nil || !user.IsActive() || !user.IsAdmin() {
		return nil, nil, ErrMCPTokenInvalid
	}
	// Last-use recording does not decide admission; keeping it best-effort avoids
	// rejecting an otherwise valid credential solely because its observation write failed.
	if token.Source == "" {
		_ = s.repo.Touch(ctx, token.ID)
	}
	return token, user, nil
}

// List reads all token metadata belonging to the requested administrator.
func (s *MCPTokenService) List(ctx context.Context, userID int64) ([]MCPToken, error) {
	return s.repo.List(ctx, userID)
}

// Update replaces a live token's metadata after validating its complete input.
// Callers validate permission identifiers first. The repository enforces owner
// matching and refuses updates to permanently revoked credentials.
func (s *MCPTokenService) Update(ctx context.Context, userID, id int64, input MCPTokenInput) (*MCPToken, error) {
	if err := validateMCPTokenInput(input); err != nil {
		return nil, err
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Permissions == nil {
		input.Permissions = []string{}
	}
	return s.repo.Update(ctx, userID, id, input)
}

// Revoke permanently disables one credential belonging to the specified administrator.
func (s *MCPTokenService) Revoke(ctx context.Context, userID, id int64) error {
	return s.repo.Revoke(ctx, userID, id)
}

// MCPTokenDigest converts a high-entropy credential to its stored hexadecimal digest.
func MCPTokenDigest(credential string) string {
	digest := sha256.Sum256([]byte(credential))
	return hex.EncodeToString(digest[:])
}

// Allows reports whether the current grant set includes this exact operation.
func (t *MCPToken) Allows(operation string) bool {
	return slices.Contains(t.Permissions, operation)
}

// validateMCPTokenInput checks label and expiry before persistent token changes.
// Operation identifiers are validated by the HTTP boundary against its mounted
// catalogue, which the identity module does not depend on.
func validateMCPTokenInput(input MCPTokenInput) error {
	if strings.TrimSpace(input.Name) == "" || utf8.RuneCountInString(input.Name) > 120 {
		return apperror.BadRequest("MCP_TOKEN_NAME_INVALID", "Token name must contain 1 to 120 characters")
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now()) {
		return apperror.BadRequest("MCP_TOKEN_EXPIRY_INVALID", "Token expiry must be in the future")
	}
	return nil
}

// SetOAuth attaches the immutable OAuth resolver before HTTP routes begin serving.
// Application assembly calls this once; requests never replace the resolver while
// authentication or internal administrative dispatch is in progress.
func (s *MCPTokenService) SetOAuth(resolver MCPOAuthResolver) { s.oauth = resolver }

// AuthorizeReference rechecks a stable grant and, when present, its access credential.
// Browser confirmations use the stable reference after their owner's JWT check.
// Tool dispatches also provide the current credential to observe family revocation.
func (s *MCPTokenService) AuthorizeReference(ctx context.Context, source string, id int64, credential string) (*MCPToken, *User, error) {
	if source == "" {
		return s.AuthorizeID(ctx, id)
	}
	if source != "oauth" || s.oauth == nil {
		return nil, nil, ErrMCPTokenInvalid
	}
	var token *MCPToken
	var err error
	if credential != "" {
		token, err = s.oauth.Authenticate(ctx, credential)
	} else {
		token, err = s.oauth.AuthorizeGrant(ctx, id)
	}
	if err != nil || token.ID != id {
		return nil, nil, ErrMCPTokenInvalid
	}
	token.Credential = credential
	return s.authorize(ctx, token)
}

// ValidateMCPGrantInput checks browser-managed grant settings before repository persistence.
func ValidateMCPGrantInput(input MCPTokenInput) error { return validateMCPTokenInput(input) }
