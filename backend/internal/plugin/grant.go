package plugin

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// grant.go binds a short-lived caller authorization to one plugin and user.
// The signing key is derived from the host's persistent JWT secret using a
// plugin-specific purpose label. Reconstructing the signer after a host restart
// therefore preserves still-valid grants without exposing the JWT signing key
// or permitting a grant for one plugin to authorize another plugin's request.
//
// Tokens contain only the plugin identity, user identity, and expiration time.
// Verification authenticates the exact encoded JSON bytes before inspecting the
// claims, then checks both identities and expiry. Financial idempotency records
// exclude this temporary token so retrying with a refreshed grant can recover
// the original result. The same derived key produces deterministic redeem codes
// from the accepted plugin identity and its immutable operation key.

// Grants issues and verifies plugin-scoped authorization using a persistent derived key.
type Grants struct {
	// key is derived once and remains private. now supplies the verification clock;
	// callers coordinate injected clock state when one signer is used across goroutines.
	key []byte           // immutable derived authentication key
	now func() time.Time // host expiration observation clock
}

// callerGrant contains the only claims accepted by the plugin authorization protocol.
type callerGrant struct {
	PluginID string `json:"p"` // authorized plugin identity
	UserID   int64  `json:"u"` // authorized account identifier
	Expiry   int64  `json:"e"` // expiration in Unix seconds
}

// NewGrants derives a dedicated key from an already initialized persistent JWT secret.
// Empty secrets fail construction. A nil clock uses wall time; callers keep the
// resulting signer for both HTTP issuance and host capability authorization checks.
func NewGrants(jwtSecret string, now func() time.Time) (*Grants, error) {
	if jwtSecret == "" {
		return nil, fmt.Errorf("plugin grants require the persistent JWT secret")
	}
	if now == nil {
		now = time.Now
	}
	mac := hmac.New(sha256.New, []byte(jwtSecret))
	_, _ = mac.Write([]byte("tokenrouter-plugin-caller-grant-v1"))
	return &Grants{key: mac.Sum(nil), now: now}, nil
}

// Issue signs one authenticated user's authorization for the selected plugin.
// The caller obtains user identity from existing panel authentication. Grants last
// five minutes and cannot be issued for anonymous users or malformed plugin IDs.
func (g *Grants) Issue(pluginID string, userID int64) (string, error) {
	if !manifest.ValidID(pluginID) || userID <= 0 {
		return "", ErrGrantInvalid
	}
	claims := callerGrant{PluginID: pluginID, UserID: userID, Expiry: g.now().Add(300 * time.Second).Unix()}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, g.key)
	_, _ = mac.Write(payload)
	return "v1." + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify requires a valid plugin-scoped grant belonging to the requested target user.
// Missing and mismatched identities return the same grant-invalid error as expired
// signatures. Capability permission checks remain separate from this caller authorization.
func (g *Grants) Verify(pluginID string, userID int64, value string) error {
	subject, err := g.Subject(pluginID, value)
	if err != nil || userID <= 0 || subject != userID {
		return ErrGrantInvalid
	}
	return nil
}

// Subject authenticates the exact payload and returns its plugin-scoped user identity.
// Invalid signatures, unknown fields, malformed encodings, and expired tickets
// return the same safe error. No token content enters diagnostics or metadata.
//
// Signature comparison happens before claim decoding, and claims must
// identify a positive user within the requested plugin namespace. The payload has
// a small fixed size bound before base64 decoding, preventing oversized caller
// tokens from multiplying allocations.
// An expiration equal to the current Unix second is already invalid. The method
// returns identity only; capability methods still check permission and target-user
// scope before authorizing an effect. Audit can attribute an optional grant even
// when an unrestricted credit targets a different user.
func (g *Grants) Subject(pluginID string, value string) (int64, error) {
	if len(value) > 512 || strings.ContainsAny(value, "\r\n") {
		return 0, ErrGrantInvalid
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return 0, ErrGrantInvalid
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return 0, ErrGrantInvalid
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size {
		return 0, ErrGrantInvalid
	}
	mac := hmac.New(sha256.New, g.key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), signature) {
		return 0, ErrGrantInvalid
	}
	var claims callerGrant
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil {
		return 0, ErrGrantInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return 0, ErrGrantInvalid
	}
	if claims.PluginID != pluginID || claims.UserID <= 0 || claims.Expiry <= g.now().Unix() {
		return 0, ErrGrantInvalid
	}
	return claims.UserID, nil
}

// RedeemCode derives a stable single-use code for a validated financial operation key.
// Callers validate the plugin ID and replay key before using this method. The
// returned code remains identical across retries and restarts with the same host secret.
func (g *Grants) RedeemCode(pluginID, idempotencyKey string) string {
	mac := hmac.New(sha256.New, g.key)
	_, _ = mac.Write([]byte(pluginID + ":" + idempotencyKey))
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(mac.Sum(nil))
	// Twenty-four base32 characters retain 120 bits while the PL prefix keeps the code inside the existing 32-character redemption limit.
	return "PL" + encoded[:24]
}
