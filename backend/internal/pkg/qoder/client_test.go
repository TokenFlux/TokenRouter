package qoder

import (
	"net/http"
	"testing"
)

func testSession() *SessionContext {
	return &SessionContext{
		CosyKey: "test-cosy-key",
		Info:    "test-info",
		Identity: &AuthIdentity{
			Name:           "test",
			UID:            "u-123",
			AID:            "a-456",
			OrganizationID: "org-789",
		},
		Machine: &MachineIdentity{
			MachineID:    "mid-abc",
			MachineToken: "mytoken",
			MachineType:  "5",
		},
	}
}

func getHeaders(t *testing.T) http.Header {
	t.Helper()
	c := NewClient("https://test.qoder.sh")
	req, _ := http.NewRequest("POST", "https://test.qoder.sh/test", nil)
	c.setHeaders(req, testSession(), "/test", "encoded-body")
	return req.Header
}

func TestHeadersClientIPIsMachineID(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-clientip") != "mid-abc" {
		t.Errorf("cosy-clientip = %q, want mid-abc", h.Get("cosy-clientip"))
	}
}

func TestHeadersMachineTypeIs5(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-machinetype") != "5" {
		t.Errorf("cosy-machinetype = %q, want 5", h.Get("cosy-machinetype"))
	}
}

func TestHeadersMachineTokenIsMachineID(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-machinetoken") != "mid-abc" {
		t.Errorf("cosy-machinetoken = %q, want mid-abc", h.Get("cosy-machinetoken"))
	}
}

func TestHeadersDataPolicyIsDisagree(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-data-policy") != "disagree" {
		t.Errorf("cosy-data-policy = %q, want disagree", h.Get("cosy-data-policy"))
	}
}

func TestHeadersVersionIs120(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-version") != "1.0.20" {
		t.Errorf("cosy-version = %q, want 1.0.20", h.Get("cosy-version"))
	}
}

func TestHeadersOrganizationID(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-organization-id") != "org-789" {
		t.Errorf("cosy-organization-id = %q, want org-789", h.Get("cosy-organization-id"))
	}
}

func TestHeadersOrganizationTags(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-organization-tags") != "Normal" {
		t.Errorf("cosy-organization-tags = %q, want Normal", h.Get("cosy-organization-tags"))
	}
}

func TestHeadersScene(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-scene") != "assistant" {
		t.Errorf("cosy-scene = %q, want assistant", h.Get("cosy-scene"))
	}
}

func TestHeadersBusinessProduct(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-business-product") != "cli" {
		t.Errorf("cosy-business-product = %q, want cli", h.Get("cosy-business-product"))
	}
}

func TestHeadersBusinessType(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-business-type") != "agent" {
		t.Errorf("cosy-business-type = %q, want agent", h.Get("cosy-business-type"))
	}
}

func TestHeadersNoHardcodedIP(t *testing.T) {
	h := getHeaders(t)
	if h.Get("cosy-clientip") == "169.254.198.161" {
		t.Error("cosy-clientip should not be hardcoded 169.254.198.161")
	}
}
