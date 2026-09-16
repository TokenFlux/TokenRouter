package googleapi

import (
	"testing"
)

func TestParseError(t *testing.T) {
	errorBody := `{
		"error": {
			"code": 403,
			"message": "API not enabled",
			"status": "PERMISSION_DENIED"
		}
	}`

	errResp, err := ParseError(errorBody)
	if err != nil {
		t.Fatalf("Failed to parse error: %v", err)
	}

	if errResp.Error.Code != 403 {
		t.Errorf("Expected code 403, got %d", errResp.Error.Code)
	}

	if errResp.Error.Status != "PERMISSION_DENIED" {
		t.Errorf("Expected status PERMISSION_DENIED, got %s", errResp.Error.Status)
	}

	if errResp.Error.Message != "API not enabled" {
		t.Errorf("Expected message 'API not enabled', got %s", errResp.Error.Message)
	}
}
