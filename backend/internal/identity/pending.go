// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	context "context"
	rand "crypto/rand"
	sha256 "crypto/sha256"
	hex "encoding/hex"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	strings "strings"
	time "time"
)

var (
	ErrPendingAuthSessionNotFound = infraerrors.NotFound("PENDING_AUTH_SESSION_NOT_FOUND", "pending auth session not found")
	ErrPendingAuthSessionExpired  = infraerrors.Unauthorized("PENDING_AUTH_SESSION_EXPIRED", "pending auth session has expired")
	ErrPendingAuthSessionConsumed = infraerrors.Unauthorized("PENDING_AUTH_SESSION_CONSUMED", "pending auth session has already been used")
	ErrPendingAuthCodeInvalid     = infraerrors.Unauthorized("PENDING_AUTH_CODE_INVALID", "pending auth completion code is invalid")
	ErrPendingAuthCodeExpired     = infraerrors.Unauthorized("PENDING_AUTH_CODE_EXPIRED", "pending auth completion code has expired")
	ErrPendingAuthCodeConsumed    = infraerrors.Unauthorized("PENDING_AUTH_CODE_CONSUMED", "pending auth completion code has already been used")
	ErrPendingAuthBrowserMismatch = infraerrors.Unauthorized("PENDING_AUTH_BROWSER_MISMATCH", "pending auth completion code does not match this browser session")
)

const (
	DefaultPendingAuthTTL           = 15 * time.Minute
	DefaultPendingAuthCompletionTTL = 5 * time.Minute
)

type PendingAuthIdentityKey struct {
	ProviderType    string
	ProviderKey     string
	ProviderSubject string
}

type CreatePendingAuthSessionInput struct {
	SessionToken             string
	Intent                   string
	Identity                 PendingAuthIdentityKey
	TargetUserID             *int64
	RedirectTo               string
	ResolvedEmail            string
	RegistrationPasswordHash string
	BrowserSessionKey        string
	UpstreamIdentityClaims   map[string]any
	LocalFlowState           map[string]any
	ExpiresAt                time.Time
}

type IssuePendingAuthCompletionCodeInput struct {
	PendingAuthSessionID int64
	BrowserSessionKey    string
	TTL                  time.Duration
}

type IssuePendingAuthCompletionCodeResult struct {
	Code      string
	ExpiresAt time.Time
}

type PendingIdentityAdoptionDecisionInput struct {
	PendingAuthSessionID int64
	IdentityID           *int64
	AdoptDisplayName     bool
	AdoptAvatar          bool
}

func SanitizePendingAuthLocalFlowState(localFlowState map[string]any) map[string]any {
	sanitized := CopyPendingMap(localFlowState)
	if len(sanitized) == 0 {
		return sanitized
	}

	rawCompletion, ok := sanitized["completion_response"]
	if !ok {
		return sanitized
	}
	completion, ok := rawCompletion.(map[string]any)
	if !ok {
		return sanitized
	}

	cleanedCompletion := CopyPendingMap(completion)
	for _, key := range []string{"access_token", "refresh_token", "expires_in", "token_type"} {
		delete(cleanedCompletion, key)
	}
	sanitized["completion_response"] = cleanedCompletion
	return sanitized
}

func ValidatePendingSessionState(session *PendingAuthSession, browserSessionKey string, expiredErr error, consumedErr error) error {
	return ValidatePendingSessionStateWithClock(session, browserSessionKey, expiredErr, consumedErr, time.Now)
}
func ValidatePendingSessionStateWithClock(session *PendingAuthSession, browserSessionKey string, expiredErr error, consumedErr error, readTime func() time.Time) error {
	if session == nil {
		return ErrPendingAuthSessionNotFound
	}

	now := readTime().UTC()
	if session.ConsumedAt != nil {
		return consumedErr
	}
	if !session.ExpiresAt.IsZero() && now.After(session.ExpiresAt) {
		return expiredErr
	}
	if session.CompletionCodeExpiresAt != nil && now.After(*session.CompletionCodeExpiresAt) {
		return expiredErr
	}
	if strings.TrimSpace(session.BrowserSessionKey) != "" && strings.TrimSpace(browserSessionKey) != strings.TrimSpace(session.BrowserSessionKey) {
		return ErrPendingAuthBrowserMismatch
	}
	return nil
}

func CopyPendingMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func RandomOpaqueToken(byteLen int) (string, error) {
	if byteLen <= 0 {
		byteLen = 16
	}
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func HashPendingAuthCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// PendingAuthSession 是不携带 Ent 行为的持久状态投影。
type PendingAuthSession struct {
	ID                       int64          `json:"id,omitempty"`
	CreatedAt                time.Time      `json:"created_at,omitempty"`
	UpdatedAt                time.Time      `json:"updated_at,omitempty"`
	SessionToken             string         `json:"session_token,omitempty"`
	Intent                   string         `json:"intent,omitempty"`
	ProviderType             string         `json:"provider_type,omitempty"`
	ProviderKey              string         `json:"provider_key,omitempty"`
	ProviderSubject          string         `json:"provider_subject,omitempty"`
	TargetUserID             *int64         `json:"target_user_id,omitempty"`
	RedirectTo               string         `json:"redirect_to,omitempty"`
	ResolvedEmail            string         `json:"resolved_email,omitempty"`
	RegistrationPasswordHash string         `json:"registration_password_hash,omitempty"`
	UpstreamIdentityClaims   map[string]any `json:"upstream_identity_claims,omitempty"`
	LocalFlowState           map[string]any `json:"local_flow_state,omitempty"`
	BrowserSessionKey        string         `json:"browser_session_key,omitempty"`
	CompletionCodeHash       string         `json:"completion_code_hash,omitempty"`
	CompletionCodeExpiresAt  *time.Time     `json:"completion_code_expires_at,omitempty"`
	EmailVerifiedAt          *time.Time     `json:"email_verified_at,omitempty"`
	PasswordVerifiedAt       *time.Time     `json:"password_verified_at,omitempty"`
	TotpVerifiedAt           *time.Time     `json:"totp_verified_at,omitempty"`
	ExpiresAt                time.Time      `json:"expires_at,omitempty"`
	ConsumedAt               *time.Time     `json:"consumed_at,omitempty"`
}

// IdentityAdoptionDecision 是不携带 Ent 行为的持久状态投影。
type IdentityAdoptionDecision struct {
	ID                   int64     `json:"id,omitempty"`
	CreatedAt            time.Time `json:"created_at,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
	PendingAuthSessionID int64     `json:"pending_auth_session_id,omitempty"`
	IdentityID           *int64    `json:"identity_id,omitempty"`
	AdoptDisplayName     bool      `json:"adopt_display_name,omitempty"`
	AdoptAvatar          bool      `json:"adopt_avatar,omitempty"`
	DecidedAt            time.Time `json:"decided_at,omitempty"`
}

// PendingStore 以闭合存储操作保护完成码与浏览器会话的一次性消费。
type PendingStore interface {
	CreatePendingSession(ctx context.Context, input CreatePendingAuthSessionInput) (*PendingAuthSession, error)
	IssueCompletionCode(ctx context.Context, input IssuePendingAuthCompletionCodeInput) (*IssuePendingAuthCompletionCodeResult, error)
	ConsumeCompletionCode(ctx context.Context, rawCode, browserSessionKey string) (*PendingAuthSession, error)
	ConsumeBrowserSession(ctx context.Context, sessionToken, browserSessionKey string) (*PendingAuthSession, error)
	GetBrowserSession(ctx context.Context, sessionToken, browserSessionKey string) (*PendingAuthSession, error)
	UpsertAdoptionDecision(ctx context.Context, input PendingIdentityAdoptionDecisionInput) (*IdentityAdoptionDecision, error)
}
