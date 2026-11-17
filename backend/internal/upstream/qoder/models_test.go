package qoder

import (
	"testing"
)

// Published model IDs are compared as the sorted union expected by the request
// catalogue. Local import conversion is checked separately with supplied
// identity fields, keeping catalogue membership independent of the private token
// representation used for session creation.

// TestLocalAuthInfoToIdentity converts a local fixture with an existing COSY
// security token. It checks the name, UID, and chosen token in the resulting
// session identity.
func TestLocalAuthInfoToIdentity(t *testing.T) {
	info := &AuthInfo{
		Name:               "test user",
		UID:                "uid123",
		SecurityOauthToken: "dt-token",
		RefreshToken:       "drt-refresh",
		UserType:           "personal_standard",
	}
	id := info.ToAuthIdentity()
	if id.Name != "test user" {
		t.Errorf("name = %q", id.Name)
	}
	if id.UID != "uid123" {
		t.Errorf("uid = %q", id.UID)
	}
	if id.SecurityOauthToken != "dt-token" {
		t.Errorf("token = %q", id.SecurityOauthToken)
	}
}
