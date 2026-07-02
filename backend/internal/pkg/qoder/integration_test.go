package qoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealAPI performs an end-to-end test against the real Qoder API.
func TestRealAPI(t *testing.T) {
	if os.Getenv("QODER_RUN_REAL_API_TESTS") != "1" {
		t.Skip("set QODER_RUN_REAL_API_TESTS=1 to run real Qoder API integration test")
	}
	authDir := DefaultAuthDir()
	if authDir == "" {
		t.Skip("no home directory")
	}
	if _, err := os.Stat(filepath.Join(authDir, "machine_id")); os.IsNotExist(err) {
		t.Skip("local Qoder auth not found")
	}

	info, err := ReadLocalAuth(authDir)
	if err != nil {
		t.Fatalf("ReadLocalAuth: %v", err)
	}
	t.Log("Loaded local Qoder auth metadata")

	identity := info.ToAuthIdentity()
	machine := &MachineIdentity{
		MachineID:    info.MachineID,
		MachineToken: RandomToken(50),
		MachineType:  RandomHex(18),
	}

	session, err := NewSession(identity, machine)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Logf("Session cosy_key length: %d", len(session.CosyKey))

	payload := map[string]any{
		"stream":           true,
		"session_id":       GenerateRequestID(),
		"request_id":       GenerateRequestID(),
		"chat_record_id":   GenerateRequestID(),
		"request_set_id":   GenerateRequestID(),
		"agent_id":         "agent_common",
		"task_id":          "common",
		"session_type":     "qodercli",
		"aliyun_user_type": identity.UserType,
		"model_config": map[string]any{
			"key":    "auto",
			"source": "system",
			"format": "openai",
		},
		"messages": []map[string]any{
			{
				"role":     "user",
				"content":  "Say hi.",
				"contents": []map[string]any{{"type": "text", "text": "Say hi."}},
			},
		},
		"parameters": map[string]any{"max_tokens": 10},
		"chat_context": map[string]any{
			"text": map[string]any{"type": "text", "text": "Say hi."},
			"extra": map[string]any{
				"modelConfig":     map[string]any{"key": "auto"},
				"originalContent": map[string]any{"type": "text", "text": "Say hi."},
			},
		},
	}

	bodyJSON, _ := json.Marshal(payload)
	client := NewClient(APIBaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	resp, err := client.StreamRequest(session, "", bodyJSON, map[string]string{
		"x-model-key":    "auto",
		"x-model-source": "system",
	})
	if err != nil {
		t.Fatalf("StreamRequest: %v", err)
	}
	t.Logf("Response status: %d", resp.StatusCode)

	textParts, done := collectStream(ctx, resp, t)
	result := strings.TrimSpace(strings.Join(textParts, ""))
	t.Logf("Result: %q (done=%v)", result, done)

	if result == "" {
		t.Error("Expected non-empty response")
	} else {
		fmt.Printf("✓ SUCCESS: %q\n", result)
	}
}

func collectStream(ctx context.Context, resp *http.Response, t *testing.T) ([]string, bool) {
	t.Helper()
	var textParts []string

	go func() {
		<-ctx.Done()
		_ = resp.Body.Close()
	}()

	for event := range StreamEvents(resp) {
		select {
		case <-ctx.Done():
			return textParts, false
		default:
		}
		if event.Type == "text_delta" && event.Text != "" {
			textParts = append(textParts, event.Text)
		} else if event.Type == "error" {
			t.Logf("SSE error: %s", event.Text)
		} else if event.IsDone {
			return textParts, true
		}
	}
	return textParts, true
}

// TestReadLocalAuthFromDisk tests reading and decrypting local Qoder auth.
func TestReadLocalAuthFromDisk(t *testing.T) {
	if os.Getenv("QODER_RUN_LOCAL_AUTH_TESTS") != "1" && os.Getenv("QODER_RUN_REAL_API_TESTS") != "1" {
		t.Skip("set QODER_RUN_LOCAL_AUTH_TESTS=1 to run local Qoder auth import test")
	}
	authDir := DefaultAuthDir()
	if authDir == "" {
		t.Skip("no home directory")
	}
	if _, err := os.Stat(filepath.Join(authDir, "machine_id")); os.IsNotExist(err) {
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
