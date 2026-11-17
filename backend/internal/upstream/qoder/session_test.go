package qoder

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

// Session fixtures inspect encrypted header presence and default temporary-key
// representation using known identities. Empty-ciphertext rejection and
// version-bearing base64 headers have separate assertions, so cryptographic
// setup and client-version selection can fail independently.

// TestNewSession constructs an encrypted global session with a supplied
// sixteen-byte key. It checks nonempty encrypted key and identity strings plus
// the retained identity name.
func TestNewSession(t *testing.T) {
	identity := &AuthIdentity{
		Name: "test",
		UID:  "test123",
	}
	machine := &MachineIdentity{
		MachineID:    "test-id",
		MachineToken: "test-token",
		MachineType:  "test-type",
	}

	session, err := NewSessionForProfileWithKey(identity, machine, MustProfileForSite(SiteGlobal), []byte("abcdefghijklmnop"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if session.CosyKey == "" {
		t.Error("expected non-empty cosy_key")
	}
	if session.Info == "" {
		t.Error("expected non-empty info")
	}
	if session.Identity.Name != "test" {
		t.Errorf("identity name = %q, want %q", session.Identity.Name, "test")
	}
}

// TestNewSessionDefaultTempKeyIsASCIIHex constructs a session using the
// generated temporary key. It requires sixteen bytes and checks every byte
// belongs to the lowercase ASCII hexadecimal alphabet.
func TestNewSessionDefaultTempKeyIsASCIIHex(t *testing.T) {
	identity := &AuthIdentity{
		Name: "test",
		UID:  "test123",
	}
	machine := &MachineIdentity{
		MachineID:    "test-id",
		MachineToken: "test-token",
		MachineType:  "test-type",
	}

	session, err := NewSessionForSite(identity, machine, SiteGlobal)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if len(session.TempKey) != 16 {
		t.Fatalf("temp key length = %d, want 16", len(session.TempKey))
	}
	for i, b := range session.TempKey {
		if (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			t.Fatalf("temp key byte %d = %q, want ASCII hex", i, b)
		}
	}
}

// TestAESDecryptRejectsEmptyCiphertext passes no encrypted bytes with a valid
// sixteen-byte key. It checks the exact empty-ciphertext error before any block
// decryption.
func TestAESDecryptRejectsEmptyCiphertext(t *testing.T) {
	_, err := AESDecrypt(nil, []byte("abcdefghijklmnop"))
	if err == nil {
		t.Fatal("expected empty ciphertext error")
	}
	if err.Error() != "qoder: ciphertext is empty" {
		t.Fatalf("error = %q, want empty ciphertext", err.Error())
	}
}

// TestBuildPayloadB64 builds global and CN version-bearing authentication
// headers and decodes them from base64. Each case checks its expected
// cosyVersion value in the JSON payload.
func TestBuildPayloadB64(t *testing.T) {
	tests := []struct {
		name  string                 // fixture case label
		build func() (string, error) // header construction callback
		want  string                 // expected client version
	}{
		{name: "国际站默认版本", build: func() (string, error) {
			return BuildPayloadB64WithVersion("test_info", "request123", GlobalClientVersion)
		}, want: "1.1.86-hotfix.5.3"},
		{name: "国内站显式版本", build: func() (string, error) {
			return BuildPayloadB64WithVersion("test_info", "request123", CNClientVersion)
		}, want: "1.1.86-hotfix.5.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := tt.build()
			if err != nil {
				t.Fatalf("BuildPayloadB64: %v", err)
			}
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			var payload map[string]string
			if err := json.Unmarshal(decoded, &payload); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			if payload["cosyVersion"] != tt.want {
				t.Fatalf("cosyVersion = %q, want %q", payload["cosyVersion"], tt.want)
			}
		})
	}
}
