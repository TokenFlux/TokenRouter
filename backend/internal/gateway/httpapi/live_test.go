package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	moderationadapter "github.com/TokenFlux/TokenRouter/internal/moderation/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// liveModerationSettings supplies fixed configuration values to the
// production moderation service.
type liveModerationSettings struct {
	// Repository leaves unneeded fixture operations unset. The implemented read
	// methods supply every configuration access used by this moderation scenario.
	settings.Repository
	// values is shared with the production service's reads. The test prepares
	// these strings before Start and keeps the map unchanged through cleanup.
	values map[string]string
}

// liveModerationLogs supplies empty violation history and accepts moderation
// audit writes.
type liveModerationLogs struct {
	// ContentModerationRepository leaves unneeded persistence operations unset.
	// This fixture implements log acceptance and empty historical violation counts.
	moderation.ContentModerationRepository
}

// newLiveModerationRuntime constructs production moderation with local-client
// and error-classification bindings. It returns an unstarted service; the
// test starts it after preparing settings and registers cleanup for both the
// service and its background callbacks.
//
// Cleanup callbacks run in reverse registration order, so core.Stop finishes
// before the wait group drains accepted background work. The Background
// function adds each callback through WaitGroup.Go, keeping asynchronous
// audit work tracked until test exit. Stop errors fail the test, and the
// final wait prevents retained callbacks from reading fixture state after its
// owner finishes.
func newLiveModerationRuntime(t *testing.T, settings moderation.SettingRepository, repo moderation.ContentModerationRepository) *moderation.ContentModerationService {
	t.Helper()
	var background sync.WaitGroup
	// Cleanup is registered before core.Stop below so reverse callback ordering
	// stops work producers before waiting for already accepted background callbacks.
	t.Cleanup(background.Wait)
	core := moderation.NewContentModerationService(settings, repo, nil, nil, nil, nil, nil, moderation.Runtime{
		Audit:         moderationadapter.NewAuditClient(),
		SnapshotMedia: moderationadapter.SnapshotMedia,
		Background:    func(_ string, fn func()) bool { background.Go(fn); return true },
		CyberText:     openai.IsOpenAICyberWarningText,
		CyberPolicy:   openai.DetectOpenAICyberPolicy,
		MissingRow:    func(err error) bool { return errors.Is(err, sql.ErrNoRows) },
		MissingUser:   func(err error) bool { return errors.Is(err, identity.ErrUserNotFound) },
	})
	t.Cleanup(func() {
		if err := core.Stop(); err != nil {
			t.Errorf("停止审核运行时: %v", err)
		}
	})
	return core
}

// GetValue returns the fixture value for a present setting, including a
// present empty value. Missing keys return ErrSettingNotFound, so production
// moderation can distinguish absence from configured text during reads.
func (s *liveModerationSettings) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", settings.ErrSettingNotFound
}

// Get constructs a setting value using the fixture's configured key and text.
// Missing keys retain GetValue's error; successful results use a fresh
// Setting while the underlying immutable strings remain values.
func (s *liveModerationSettings) Get(ctx context.Context, key string) (*settings.Setting, error) {
	v, e := s.GetValue(ctx, key)
	if e != nil {
		return nil, e
	}
	return &settings.Setting{Key: key, Value: v}, nil
}

// GetMultiple builds a fresh map of configured values for the supplied keys.
// Missing settings are omitted and existing values, including empty strings,
// are retained for production configuration reads.
func (s *liveModerationSettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

// CreateLog accepts production moderation audit records without storing
// fixture history.
func (*liveModerationLogs) CreateLog(context.Context, *moderation.ContentModerationLog) error {
	return nil
}

// CountFlaggedByUserSince supplies zero historical violations for every
// tested user and time window. This fixture keeps the content-blocking test
// focused on the current configured moderation decision, with no prior-strike
// contribution.
func (*liveModerationLogs) CountFlaggedByUserSince(context.Context, int64, time.Time) (int, error) {
	return 0, nil
}

// TestParseLiveCallRequestMultipartPreservesSession supplies multipart SDP
// and a session containing delegation and Unicode instructions. Parsing
// retains the SDP text and session fields before any model rewrite occurs.
func TestParseLiveCallRequestMultipartPreservesSession(t *testing.T) {
	session := `{"model":"gpt-live-test","delegation":{"type":"client"},"instructions":"你好"}`
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("sdp", "v=0\r\n"))
	require.NoError(t, writer.WriteField("session", session))
	require.NoError(t, writer.Close())

	request := httptest.NewRequest(http.MethodPost, "/v1/live", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request

	parsed, err := ParseLiveCallRequest(context)
	require.NoError(t, err)
	require.Equal(t, "v=0\r\n", parsed.SDP)
	require.JSONEq(t, session, string(parsed.Session))
	require.Equal(t, "client", jsonPathString(t, parsed.Session, "delegation", "type"))
}

// TestParseLiveCallRequestJSONPreservesSessionWithoutDelegation parses JSON
// session instructions on the Codex creation route. The supplied instructions
// survive, and parsing leaves the absent delegation field absent.
func TestParseLiveCallRequestJSONPreservesSessionWithoutDelegation(t *testing.T) {
	body := `{"sdp":"v=0\\r\\n","session":{"model":"gpt-live-test","instructions":"standalone"}}`
	request := httptest.NewRequest(http.MethodPost, "/backend-api/codex/realtime/calls", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request

	parsed, err := ParseLiveCallRequest(context)
	require.NoError(t, err)
	require.NotContains(t, string(parsed.Session), "delegation")
	require.Equal(t, "standalone", jsonPathString(t, parsed.Session, "instructions"))
}

// TestParseLiveCallRequestRejectsInvalidJSONShape supplies missing SDP, array
// or null session values and a second JSON object. Each invalid creation
// shape returns an error instead of a usable Live request.
func TestParseLiveCallRequestRejectsInvalidJSONShape(t *testing.T) {
	testCases := []string{
		`{"session":{"type":"quicksilver"}}`,
		`{"sdp":"v=0\\r\\n","session":[]}`,
		`{"sdp":"v=0\\r\\n","session":null}`,
		`{"sdp":"v=0\\r\\n","session":{"type":"quicksilver"}} {}`,
	}
	for _, body := range testCases {
		request := httptest.NewRequest(http.MethodPost, "/backend-api/codex/realtime/calls", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = request
		_, err := ParseLiveCallRequest(context)
		require.Error(t, err)
	}
}

// TestLiveSidebandLocationMatchesCreateRoute builds control locations for
// ordinary and Codex creation paths. Each resulting URL uses the matching
// public route family with the same call identifier.
func TestLiveSidebandLocationMatchesCreateRoute(t *testing.T) {
	require.Equal(t, "/v1/live/call_123", LiveSidebandLocation("/v1/live", "call_123"))
	require.Equal(
		t,
		"/backend-api/codex/call_123",
		LiveSidebandLocation("/backend-api/codex/realtime/calls", "call_123"),
	)
}

// TestLiveEnabledForAPIKey checks missing Key, missing group, disabled policy
// and enabled group views. Admission follows the group Live flag and rejects
// the unavailable group cases.
func TestLiveEnabledForAPIKey(t *testing.T) {
	require.False(t, liveEnabledForAPIKey(nil))
	require.False(t, liveEnabledForAPIKey(&LiveAPIKey{}))
	require.False(t, liveEnabledForAPIKey(&LiveAPIKey{
		Group: &LiveGroup{},
	}))
	require.True(t, liveEnabledForAPIKey(&LiveAPIKey{
		Group: &LiveGroup{AllowLive: true},
	}))
	require.True(t, liveEnabledForAPIKey(&LiveAPIKey{
		Group: &LiveGroup{AllowLive: true},
	}))
}

// TestLiveContentModerationBlocksBeforeBilling sends Live content to the
// production moderation service backed by a local response fixture. Its
// blocking decision arrives before unavailable funding and records the Live
// request type.
func TestLiveContentModerationBlocksBeforeBilling(t *testing.T) {
	moderationServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/moderations", r.URL.Path)
		_, _ = w.Write([]byte(`{"results":[{"category_scores":{"sexual":0.9}}]}`))
	}))
	defer moderationServer.Close()

	cfg := &moderation.ContentModerationConfig{
		Enabled:      true,
		Mode:         moderation.ContentModerationModePreBlock,
		BaseURL:      moderationServer.URL,
		Model:        "omni-moderation-latest",
		APIKeys:      []string{"sk-test"},
		SampleRate:   100,
		AllGroups:    true,
		BlockMessage: "Live 内容审核测试阻断",
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	moderationSvc := newLiveModerationRuntime(t, &liveModerationSettings{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}},
		&liveModerationLogs{},
	)
	moderationSvc.Start()

	groupID := int64(2)
	group := &routing.Group{ID: groupID, Name: "live", AllowLive: true}
	apiKey := &apikey.APIKey{
		ID:      101,
		Name:    "live-test-key",
		GroupID: &groupID,
		Group:   group,
		UserID:  1,
		User:    &identity.User{ID: 1},
	}
	body := bytes.NewBufferString(`{"sdp":"v=0\\r\\n","session":{"model":"gpt-live-test","input":"bad prompt"}}`)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/live", body)
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
	context.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1, Concurrency: 1})

	// Omit funding services so the forbidden response proves moderation blocks before billing checks.
	NewLiveHandler(LivePorts{Moderation: moderationSvc}).Live(context)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "content_policy_violation")
	require.Contains(t, recorder.Body.String(), "Live 内容审核测试阻断")
	requestType, exists := context.Get("ops_request_type")
	require.True(t, exists)
	require.Equal(t, int16(usage.RequestTypeLive), requestType)
}

// TestLiveAttestationErrorIsExplicit presents a typed attestation-unavailable
// error through Live creation handling. The response uses temporary-service
// status and retains its supported platform reason for the client.
func TestLiveAttestationErrorIsExplicit(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)

	NewLiveHandler(LivePorts{}).WriteLiveCreateError(context, &gatewaysession.LiveAttestationUnavailableError{
		Reason: "Live attestation is only supported when TokenRouter runs on macOS",
	})

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "TokenRouter runs on macOS")
}

// jsonPathString extracts a string by traversing JSON object keys in fixture
// session bytes. Every intermediate value must be an object and the final
// value a string; failed shape checks stop the test.
func jsonPathString(t *testing.T, raw json.RawMessage, keys ...string) string {
	t.Helper()
	var value any
	require.NoError(t, json.Unmarshal(raw, &value))
	current := value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		require.True(t, ok)
		current = object[key]
	}
	result, ok := current.(string)
	require.True(t, ok)
	return result
}
