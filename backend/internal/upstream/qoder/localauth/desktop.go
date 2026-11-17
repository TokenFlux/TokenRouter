package localauth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// desktop.go imports the macOS desktop application's encrypted login for
// explicitly requested diagnostics. Qoder desktop keeps OpenAPI device tokens
// in Electron safeStorage, separately from the older CLI credential directory.
// Site selection determines both the application directory and Keychain item;
// the decoded authentication context must agree with that selection.
//
// Keychain access is read-only and can require a system authorization prompt.
// The password and decrypted record remain in process memory, and command
// failures never include captured output in their errors. Normal gateway
// execution does not call this importer. The caller owns the returned tokens
// and supplies its own bounded context for the potentially interactive read.

// desktopCredential describes the version-one Electron login envelope. Token
// values are OpenAPI credentials, not the older Center refresh-token format.
type desktopCredential struct {
	SchemaVersion         int    `json:"schemaVersion"`         // desktop storage version
	AuthenticationContext string `json:"authenticationContext"` // serialized origin tuple
	Token                 string `json:"token"`                 // OpenAPI access token
	RefreshToken          string `json:"refreshToken"`          // OpenAPI refresh token
	ExpiresAt             string `json:"expiresAt"`             // RFC3339
	RefreshTokenExpiresAt string `json:"refreshTokenExpiresAt"` // RFC3339
	User                  struct {
		ID    string `json:"id"`    // authenticated user identifier
		Name  string `json:"name"`  // authenticated display name
		Email string `json:"email"` // authenticated contact address
	} `json:"user"` // saved desktop identity
}

// ReadDesktopAuth imports one site's macOS desktop login without changing it.
// Callers opt into this operation and bound Keychain interaction with ctx;
// other operating systems return an unsupported-platform error.
func ReadDesktopAuth(ctx context.Context, site qoder.Site) (*qoder.AuthInfo, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("qoder: desktop credential import requires macOS")
	}
	site, err := qoder.ParseSite(string(site))
	if err != nil {
		return nil, err
	}
	app, directory := "Qoder App", "com.qoder.app.stable"
	if site == qoder.SiteCN {
		app, directory = "Qoder CN App", "com.qodercn.app.stable"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("qoder: locate desktop credentials: %w", err)
	}
	root := filepath.Join(home, "Library", "Application Support", directory)
	encrypted, err := os.ReadFile(filepath.Join(root, "auth.v1.dat"))
	if err != nil {
		return nil, fmt.Errorf("qoder: read desktop credential file: %w", err)
	}
	machine, err := os.ReadFile(filepath.Join(root, "auth.machine-id"))
	if err != nil {
		return nil, fmt.Errorf("qoder: read desktop machine identity: %w", err)
	}
	password, err := exec.CommandContext(
		ctx, "security", "find-generic-password",
		"-s", app+" Safe Storage",
		"-a", app+" Key",
		"-w",
	).Output()
	if err != nil {
		// ExitError can retain process output. Return only the operation status so
		// a failed Keychain invocation cannot disclose captured authentication data.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("qoder: desktop Keychain access was not authorized")
	}
	defer clear(password)
	return decodeDesktopCredential(encrypted, bytes.TrimSuffix(password, []byte("\n")), strings.TrimSpace(string(machine)), site)
}

// decodeDesktopCredential validates and decrypts a version-one desktop record.
// The Keychain password supplies Chromium's macOS safeStorage key; malformed
// envelopes, padding, identity, or site context return sanitized errors.
//
// Version and block-length checks precede CBC operations, and every trailing
// padding byte is verified before JSON decoding. Derived-key and plaintext
// buffers are cleared on return. The resulting token strings belong to the
// caller, while errors deliberately omit the decrypted record and captured
// password so diagnostics cannot turn a failed import into credential output.
func decodeDesktopCredential(encrypted, password []byte, machine string, site qoder.Site) (*qoder.AuthInfo, error) {
	if len(encrypted) < 3+aes.BlockSize || string(encrypted[:3]) != "v10" || (len(encrypted)-3)%aes.BlockSize != 0 {
		return nil, errors.New("qoder: unsupported desktop credential encoding")
	}
	// Electron's macOS v10 format derives a 128-bit CBC key with this fixed
	// salt and work factor. Its IV contains sixteen ASCII space bytes.
	key, err := pbkdf2.Key(sha1.New, string(password), []byte("saltysalt"), 1003, 16)
	if err != nil {
		return nil, errors.New("qoder: derive desktop credential key")
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("qoder: initialize desktop credential cipher")
	}
	plaintext := make([]byte, len(encrypted)-3)
	defer clear(plaintext)
	cipher.NewCBCDecrypter(block, bytes.Repeat([]byte{' '}, aes.BlockSize)).CryptBlocks(plaintext, encrypted[3:])
	padding := int(plaintext[len(plaintext)-1])
	if padding < 1 || padding > aes.BlockSize || !bytes.Equal(plaintext[len(plaintext)-padding:], bytes.Repeat([]byte{byte(padding)}, padding)) {
		return nil, errors.New("qoder: desktop credential decryption failed")
	}
	var saved desktopCredential
	if json.Unmarshal(plaintext[:len(plaintext)-padding], &saved) != nil {
		return nil, errors.New("qoder: invalid desktop credential record")
	}
	return saved.authInfo(machine, site)
}

// authInfo validates the stored site and converts desktop expiry timestamps.
// Empty refresh tokens remain valid for current-session diagnostics; an access
// token, user identifier, and machine identifier are required.
func (saved desktopCredential) authInfo(machine string, site qoder.Site) (*qoder.AuthInfo, error) {
	profile, err := qoder.ProfileForSite(site)
	if err != nil {
		return nil, err
	}
	var origins []string
	if saved.SchemaVersion != 1 || json.Unmarshal([]byte(saved.AuthenticationContext), &origins) != nil || len(origins) != 4 || origins[1] != profile.OpenAPIBaseURL || origins[3] != "prod" {
		return nil, errors.New("qoder: desktop authentication context does not match the selected site")
	}
	if saved.Token == "" || saved.User.ID == "" || machine == "" {
		return nil, errors.New("qoder: desktop credentials are incomplete")
	}
	expires, err := desktopExpiry(saved.ExpiresAt)
	if err != nil {
		return nil, err
	}
	refreshExpires, err := desktopExpiry(saved.RefreshTokenExpiresAt)
	if err != nil {
		return nil, err
	}
	return &qoder.AuthInfo{
		UID: saved.User.ID, Name: saved.User.Name, Email: saved.User.Email,
		AccessToken: saved.Token, SecurityOauthToken: saved.Token, RefreshToken: saved.RefreshToken,
		ExpireTime: expires, RefreshTokenExpireTime: refreshExpires, MachineID: machine,
		LoginMethod: "browser",
	}, nil
}

// desktopExpiry converts an optional RFC3339 timestamp into Unix seconds.
// An absent timestamp remains unknown; malformed input never appears in the
// returned error because it belongs to a credential record.
func desktopExpiry(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	timestamp, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0, errors.New("qoder: invalid desktop credential expiry")
	}
	return timestamp.Unix(), nil
}
