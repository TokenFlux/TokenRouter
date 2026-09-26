package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newCyberBlockTestCtx(headers map[string]string, body string) (*gin.Context, []byte) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/openai/v1/responses", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	return c, []byte(body)
}

func TestCyberSessionExplicitBlockKey(t *testing.T) {
	c1, b1 := newCyberBlockTestCtx(map[string]string{"session_id": "sess-abc"}, `{}`)
	k1 := gatewayhttp.CyberSessionExplicitBlockKey(101, c1, b1)
	require.NotEmpty(t, k1)

	// Same session, different apiKey → different key (isolation).
	c2, b2 := newCyberBlockTestCtx(map[string]string{"session_id": "sess-abc"}, `{}`)
	require.NotEqual(t, k1, gatewayhttp.CyberSessionExplicitBlockKey(202, c2, b2))

	// Same session + same apiKey → stable key.
	c3, b3 := newCyberBlockTestCtx(map[string]string{"session_id": "sess-abc"}, `{}`)
	require.Equal(t, k1, gatewayhttp.CyberSessionExplicitBlockKey(101, c3, b3))

	// prompt_cache_key in body counts as explicit.
	c4, b4 := newCyberBlockTestCtx(nil, `{"prompt_cache_key":"pck-1"}`)
	require.NotEmpty(t, gatewayhttp.CyberSessionExplicitBlockKey(101, c4, b4))

	// No explicit signal → empty key → caller must skip blocking entirely.
	c5, b5 := newCyberBlockTestCtx(nil, `{"input":"hello world"}`)
	require.Empty(t, gatewayhttp.CyberSessionExplicitBlockKey(101, c5, b5))

	// conversation_id header counts as explicit; key is stable and non-empty.
	c6, b6 := newCyberBlockTestCtx(map[string]string{"conversation_id": "conv-xyz"}, `{}`)
	k6 := gatewayhttp.CyberSessionExplicitBlockKey(101, c6, b6)
	require.NotEmpty(t, k6)
	c6b, b6b := newCyberBlockTestCtx(map[string]string{"conversation_id": "conv-xyz"}, `{}`)
	require.Equal(t, k6, gatewayhttp.CyberSessionExplicitBlockKey(101, c6b, b6b), "conversation_id key must be stable")
}

func TestCyberTranscriptBlockKeysRequireModelGeneratedHistory(t *testing.T) {
	first := []byte(`{"instructions":"shared","input":[{"role":"user","content":"fixed environment"},{"role":"user","content":"question one"}]}`)
	second := []byte(`{"instructions":"shared","input":[{"role":"user","content":"fixed environment"},{"role":"user","content":"question two"}]}`)
	firstKeys := session.CyberSessionTranscriptBlockKeys(77, first)
	secondKeys := session.CyberSessionTranscriptBlockKeys(77, second)
	require.Len(t, firstKeys, 1)
	require.Len(t, secondKeys, 1)
	require.NotEqual(t, firstKeys[0], secondKeys[0])

	hit := []byte(`{"messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"trigger"}]}`)
	continuation := []byte(`{"messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"different trigger"},{"role":"assistant","content":"blocked"},{"role":"user","content":"continue"}]}`)
	hitKeys := session.CyberSessionTranscriptBlockKeys(77, hit)
	require.Len(t, hitKeys, 2)
	require.Contains(t, session.CyberSessionTranscriptLookupKeys(77, continuation), hitKeys[1])
}

func TestCyberTranscriptBlockKeysWebSocketResponseCreate(t *testing.T) {
	body := []byte(`{"type":"response.create","response":{"prompt_cache_key":"ws-session","input":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"trigger"}]}}`)
	c, _ := newCyberBlockTestCtx(nil, string(body))
	require.NotEmpty(t, gatewayhttp.CyberSessionExplicitBlockKey(88, c, body))
	require.Len(t, session.CyberSessionTranscriptBlockKeys(88, body), 2)
}

func TestCyberTranscriptLookupKeysAreBoundedAndKeepNewestOrder(t *testing.T) {
	messages := make([]map[string]string, 256+44)
	for i := range messages {
		messages[i] = map[string]string{"role": "user", "content": "message-" + strconv.Itoa(i)}
	}
	body, err := json.Marshal(map[string]any{"messages": messages})
	require.NoError(t, err)

	keys := session.CyberSessionTranscriptLookupKeys(77, body)
	require.Len(t, keys, 256)

	firstRetainedBody, err := json.Marshal(map[string]any{"messages": messages[:45]})
	require.NoError(t, err)
	firstRetainedPrefix := session.CyberSessionTranscriptLookupKeys(77, firstRetainedBody)
	require.Equal(t, firstRetainedPrefix[len(firstRetainedPrefix)-1], keys[0])

	fullKey := session.CyberSessionTranscriptBlockKeys(77, body)[0]
	require.Equal(t, fullKey, keys[len(keys)-1])
}

// --- fakes ---

type fakeCyberBlockStore struct {
	blocked   map[string]bool
	scopes    map[string]bool
	findCalls int
}

var _ session.CyberSessionBlockStore = (*fakeCyberBlockStore)(nil)

func (f *fakeCyberBlockStore) SetCyberSessionBlocked(_ context.Context, scopeKey string, keys []string, _ time.Duration) error {
	if f.blocked == nil {
		f.blocked = map[string]bool{}
	}
	for _, key := range keys {
		f.blocked[key] = true
	}
	if scopeKey != "" {
		if f.scopes == nil {
			f.scopes = map[string]bool{}
		}
		f.scopes[scopeKey] = true
	}
	return nil
}

func (f *fakeCyberBlockStore) IsCyberSessionScopeActive(_ context.Context, scopeKey string) (bool, error) {
	return f.scopes[scopeKey], nil
}

func (f *fakeCyberBlockStore) FindCyberSessionBlocked(_ context.Context, keys []string) (string, error) {
	f.findCalls++
	for _, key := range keys {
		if f.blocked[key] {
			return key, nil
		}
	}
	return "", nil
}

// fakeSettingRepo is a minimal SettingRepository stub for unit tests.
// Only GetValue is exercised by GetCyberSessionBlockRuntime; all other methods
// panic so accidental calls are caught immediately.
type fakeSettingRepo struct {
	vals map[string]string
}

func (r *fakeSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	v, ok := r.vals[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return v, nil
}

func (r *fakeSettingRepo) Get(_ context.Context, _ string) (*settings.Setting, error) {
	panic("fakeSettingRepo.Get not implemented")
}

func (r *fakeSettingRepo) Set(_ context.Context, _, _ string) error {
	panic("fakeSettingRepo.Set not implemented")
}

func (r *fakeSettingRepo) GetMultiple(_ context.Context, _ []string) (map[string]string, error) {
	panic("fakeSettingRepo.GetMultiple not implemented")
}

func (r *fakeSettingRepo) SetMultiple(_ context.Context, _ map[string]string) error {
	panic("fakeSettingRepo.SetMultiple not implemented")
}

func (r *fakeSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	panic("fakeSettingRepo.GetAll not implemented")
}

func (r *fakeSettingRepo) Delete(_ context.Context, _ string) error {
	panic("fakeSettingRepo.Delete not implemented")
}

var _ settings.Repository = (*fakeSettingRepo)(nil)

// comboCacheAndStore implements both GatewayCache (no-op stubs) and
// CyberSessionBlockStore (delegates to fakeCyberBlockStore) so it can be
// injected as s.cache and successfully type-asserted to CyberSessionBlockStore.
type comboCacheAndStore struct {
	store fakeCyberBlockStore
}

var (
	_ session.GatewayCache           = (*comboCacheAndStore)(nil)
	_ session.CyberSessionBlockStore = (*comboCacheAndStore)(nil)
)

func (c *comboCacheAndStore) GetSessionAccountID(_ context.Context, _ int64, _ string) (int64, error) {
	return 0, errors.New("stub")
}

func (c *comboCacheAndStore) SetSessionAccountID(_ context.Context, _ int64, _ string, _ int64, _ time.Duration) error {
	return nil
}

func (c *comboCacheAndStore) RefreshSessionTTL(_ context.Context, _ int64, _ string, _ time.Duration) error {
	return nil
}

func (c *comboCacheAndStore) DeleteSessionAccountID(_ context.Context, _ int64, _ string) error {
	return nil
}

func (c *comboCacheAndStore) SetSessionOwnerGroupID(_ context.Context, _ int64, _, _ string, _ int64, _ time.Duration) (bool, error) {
	return false, nil
}

func (c *comboCacheAndStore) GetSessionOwnerGroupID(_ context.Context, _ int64, _, _ string) (int64, error) {
	return 0, nil
}

func (c *comboCacheAndStore) RefreshSessionOwnerTTL(_ context.Context, _ int64, _, _ string, _ time.Duration) error {
	return nil
}

func (c *comboCacheAndStore) SetGrokVideoPendingBilling(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}

func (c *comboCacheAndStore) GetGrokVideoPendingBilling(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}

func (c *comboCacheAndStore) ClaimGrokVideoBilled(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

func (c *comboCacheAndStore) ReleaseGrokVideoBilled(_ context.Context, _ string) error {
	return nil
}

func (c *comboCacheAndStore) SetReasoningContent(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}

func (c *comboCacheAndStore) GetReasoningContent(_ context.Context, _ string) (string, error) {
	return "", session.ErrReasoningContentNotFound
}

func (c *comboCacheAndStore) SetCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string, ttl time.Duration) error {
	return c.store.SetCyberSessionBlocked(ctx, scopeKey, keys, ttl)
}

func (c *comboCacheAndStore) IsCyberSessionScopeActive(ctx context.Context, scopeKey string) (bool, error) {
	return c.store.IsCyberSessionScopeActive(ctx, scopeKey)
}

func (c *comboCacheAndStore) FindCyberSessionBlocked(ctx context.Context, keys []string) (string, error) {
	return c.store.FindCyberSessionBlocked(ctx, keys)
}

// --- tests ---

// TestFindCyberSessionBlocked_EmptyAndNilService covers the fail-open paths:
// empty key, nil service, store missing → always false / no panic.
func TestFindCyberSessionBlocked_EmptyAndNilService(t *testing.T) {
	var nilSvc *session.CyberBlocks
	require.Empty(t, gatewayhttp.FindCyberSessionForRequest(context.Background(), nilSvc, 1, nil, nil, "", ""))
	require.NotPanics(t, func() { nilSvc.MarkCyberSessionBlocked(context.Background(), "", []string{"k"}) })

	svc := session.NewCyberBlocks(nil, nil, nil)
	require.Empty(t, gatewayhttp.FindCyberSessionForRequest(context.Background(), svc, 1, nil, nil, "", ""))
}

// TestCyberSessionBlock_RoundTrip exercises the type-assertion success path:
// mark a session blocked via a combo cache+store, then confirm IsCyberSessionBlocked
// returns true, and an unrelated key returns false.
func TestCyberSessionBlock_RoundTrip(t *testing.T) {
	// SettingService with only settingRepo set — GetCyberSessionBlockRuntime needs
	// nothing else (cfg/proxyRepo/etc. are not touched by this code path).
	settingSvc := moderation.NewRuntimeSettings(&fakeSettingRepo{
		vals: map[string]string{
			moderation.SettingKeyCyberSessionBlockEnabled:    "true",
			moderation.SettingKeyCyberSessionBlockTTLSeconds: "60",
		},
	}, settings.ErrSettingNotFound)

	combo := &comboCacheAndStore{}
	svc := session.NewCyberBlocks(session.AdaptCyberSessionBlockStore(combo), settingSvc.GetCyberSessionBlockRuntime, nil)

	ctx := context.Background()
	const testKey = "deadbeef1234"

	c, body := newCyberBlockTestCtx(map[string]string{"session_id": "sess-roundtrip"}, `{}`)
	explicitKey := gatewayhttp.CyberSessionExplicitBlockKey(1, c, body)
	require.Empty(t, gatewayhttp.FindCyberSessionForRequest(ctx, svc, 1, c, body, "203.0.113.1", "client/1.0"))

	svc.MarkCyberSessionBlocked(ctx, "", []string{explicitKey, testKey})

	require.Equal(t, explicitKey, gatewayhttp.FindCyberSessionForRequest(ctx, svc, 1, c, body, "203.0.113.1", "client/1.0"))
}

func TestFindCyberSessionBlockedForRequestUsesScopeForTranscript(t *testing.T) {
	settingSvc := moderation.NewRuntimeSettings(&fakeSettingRepo{vals: map[string]string{
		moderation.SettingKeyCyberSessionBlockEnabled:    "true",
		moderation.SettingKeyCyberSessionBlockTTLSeconds: "60",
	}}, settings.ErrSettingNotFound)
	combo := &comboCacheAndStore{}
	svc := session.NewCyberBlocks(session.AdaptCyberSessionBlockStore(combo), settingSvc.GetCyberSessionBlockRuntime, nil)
	ctx := context.Background()

	hitBody := []byte(`{"messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"trigger"}]}`)
	nextBody := []byte(`{"messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"different trigger"},{"role":"assistant","content":"blocked"},{"role":"user","content":"continue"}]}`)
	nextCtx, _ := newCyberBlockTestCtx(nil, string(nextBody))
	const clientIP = "203.0.113.20"
	const userAgent = "Codex CLI 1.2.3"
	blockKey := session.CyberSessionTranscriptBlockKeys(9, hitBody)[1]

	// Without an active source scope, transcript candidates are never blocks.
	require.Empty(t, gatewayhttp.FindCyberSessionForRequest(ctx, svc, 9, nextCtx, nextBody, clientIP, userAgent))
	scopeKey := session.CyberSessionScopeKey(9, clientIP, userAgent)
	svc.MarkCyberSessionBlocked(ctx, scopeKey, []string{blockKey})
	require.Equal(t, blockKey, gatewayhttp.FindCyberSessionForRequest(ctx, svc, 9, nextCtx, nextBody, clientIP, "Codex CLI 1.2.4"))
}

func TestFindCyberSessionBlockedForRequestFailsClosedOnScopedTranscriptOverflow(t *testing.T) {
	settingSvc := moderation.NewRuntimeSettings(&fakeSettingRepo{vals: map[string]string{
		moderation.SettingKeyCyberSessionBlockEnabled:    "true",
		moderation.SettingKeyCyberSessionBlockTTLSeconds: "60",
	}}, settings.ErrSettingNotFound)
	combo := &comboCacheAndStore{}
	svc := session.NewCyberBlocks(session.AdaptCyberSessionBlockStore(combo), settingSvc.GetCyberSessionBlockRuntime, nil)
	ctx := context.Background()
	const apiKeyID = int64(9)
	const clientIP = "203.0.113.20"
	const userAgent = "Codex CLI 1.2.3"

	messages := make([]map[string]string, 256+1)
	for i := range messages {
		messages[i] = map[string]string{"role": "user", "content": "message-" + strconv.Itoa(i)}
	}
	body, err := json.Marshal(map[string]any{"messages": messages})
	require.NoError(t, err)
	c, _ := newCyberBlockTestCtx(nil, string(body))
	require.Empty(t, gatewayhttp.FindCyberSessionForRequest(ctx, svc, apiKeyID, c, body, clientIP, userAgent), "overflow alone must not bypass the scope gate")
	combo.store.scopes = map[string]bool{session.CyberSessionScopeKey(apiKeyID, clientIP, userAgent): true}

	require.Equal(t, "transcript_lookup_limit_exceeded", gatewayhttp.FindCyberSessionForRequest(ctx, svc, apiKeyID, c, body, clientIP, userAgent))
	require.Zero(t, combo.store.findCalls, "overflow must not issue an unbounded Redis lookup")
}

func TestCyberSessionScopeKeyNormalizesUserAgentVersion(t *testing.T) {
	base := session.CyberSessionScopeKey(7, "203.0.113.10", "Codex CLI 1.2.3")
	require.NotEmpty(t, base)
	require.Equal(t, base, session.CyberSessionScopeKey(7, "203.0.113.10", "Codex CLI 1.2.4"))
	require.NotEqual(t, base, session.CyberSessionScopeKey(8, "203.0.113.10", "Codex CLI 1.2.3"))
	require.NotEqual(t, base, session.CyberSessionScopeKey(7, "203.0.113.11", "Codex CLI 1.2.3"))
}
