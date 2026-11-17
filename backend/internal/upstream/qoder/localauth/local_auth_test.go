package localauth

import (
	"os"
	"path/filepath"
	"testing"
)

// Local import is tested only when its dedicated or real-API opt-in flag is
// enabled. The fixture skips absent local machine data and checks identity plus
// token presence after decryption, leaving credential values out of its
// diagnostic log.

// TestReadLocalAuthFromDisk imports saved local credentials when a local-auth or
// real-API opt-in flag is enabled. It skips unavailable local data and requires
// a UID plus an access or security token.
func TestReadLocalAuthFromDisk(t *testing.T) {
	// Local import reads user-owned credential files. Require a dedicated opt-in
	// flag so ordinary unit runs do not depend on the developer's saved Qoder
	// login.
	if os.Getenv("QODER_RUN_LOCAL_AUTH_TESTS") != "1" && os.Getenv("QODER_RUN_REAL_API_TESTS") != "1" {
		t.Skip("set QODER_RUN_LOCAL_AUTH_TESTS=1 to run local Qoder auth import test")
	}
	authDir := DefaultAuthDir()
	if authDir == "" {
		t.Skip("no home directory")
	}
	if _, err := os.Stat(filepath.Join(authDir, "user")); os.IsNotExist(err) {
		t.Skip("local Qoder auth not found")
	}

	info, err := ReadLocalAuth(authDir)
	if err != nil {
		t.Fatalf("ReadLocalAuth: %v", err)
	}

	if info.UID == "" {
		t.Error("expected non-empty UID")
	}
	if info.AccessToken == "" && info.SecurityOauthToken == "" {
		t.Error("expected non-empty token")
	}
	t.Log("Loaded local Qoder auth metadata")
}
