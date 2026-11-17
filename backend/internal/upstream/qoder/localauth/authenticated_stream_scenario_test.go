package localauth

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

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// The real-API scenario imports a local global-site identity and sends one
// generated streaming payload. It runs only with the opt-in flag, then observes
// nonempty visible text while a separate collection context bounds response-body
// reading after dispatch.

// TestRealAPI uses local global credentials to request one real Qoder response
// when explicitly enabled. It skips absent local data and checks nonempty
// collected text, with a separate context bounding collection after request
// dispatch.
func TestRealAPI(t *testing.T) {
	if os.Getenv("QODER_RUN_REAL_API_TESTS") != "1" {
		t.Skip("set QODER_RUN_REAL_API_TESTS=1 to run real Qoder API integration test")
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
	t.Log("Loaded local Qoder auth metadata")

	identity := info.ToAuthIdentity()
	machine := &qoder.MachineIdentity{
		MachineID:    info.MachineID,
		MachineToken: qoder.RandomToken(50),
		MachineType:  qoder.RandomHex(18),
	}

	session, err := qoder.NewSessionForSite(identity, machine, qoder.SiteGlobal)
	if err != nil {
		t.Fatalf("qoder.NewSession: %v", err)
	}
	t.Logf("Session cosy_key length: %d", len(session.CosyKey))

	payload := map[string]any{
		"stream":           true,
		"session_id":       qoder.GenerateRequestID(),
		"request_id":       qoder.GenerateRequestID(),
		"chat_record_id":   qoder.GenerateRequestID(),
		"request_set_id":   qoder.GenerateRequestID(),
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

	// The fixture uses JSON-supported strings, numbers, and container values.
	// Marshaling therefore supplies its request or response representation before
	// the client behavior is exercised.
	bodyJSON, _ := json.Marshal(payload)
	client := qoder.NewClient(qoder.APIBaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	resp, err := client.StreamRequestContext(context.Background(), session, "", bodyJSON, map[string]string{
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

// collectStream collects visible events until completion, channel closure, or
// context cancellation. The caller supplies a usable response and collection
// budget. A goroutine closes the body after cancellation so blocked reads can
// end.
//
// StreamEvents owns its scanner channel and body cleanup, while this helper
// stops collecting on DONE or returns false when cancellation is observed.
// Stream error events are logged and scanning continues; channel closure reports
// true, so that flag represents either a completion marker or exhausted event
// delivery.
func collectStream(ctx context.Context, resp *http.Response, t *testing.T) ([]string, bool) {
	t.Helper()
	var textParts []string

	// StreamEvents can be blocked reading upstream while this collection budget
	// expires. Closing the response body releases that read even before another
	// event arrives.
	go func() {
		<-ctx.Done()
		_ = resp.Body.Close()
	}()

	for event := range qoder.StreamEvents(resp) {
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
