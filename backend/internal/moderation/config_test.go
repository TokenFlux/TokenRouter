package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestContentModerationUpdateConfigPersistsKeyMetadataAndAuditScope updates two
// saved metadata entries and audit source controls through a settings fixture.
// It checks returned limits/toggles and exact stored key metadata after JSON
// decoding.
func TestContentModerationUpdateConfigPersistsKeyMetadataAndAuditScope(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.APIKeys = []string{"sk-high", "sk-low"}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationTestSettingRepo{values: map[string]string{SettingKeyContentModerationConfig: string(rawCfg)}}
	svc := newTestModeration(repo, nil, nil, nil, nil, nil, nil)
	svc.Start()
	updates := []ContentModerationAPIKeyMetadata{
		{KeyHash: moderationAPIKeyHash("sk-high"), Priority: 100, Note: "Tier 5"},
		{KeyHash: moderationAPIKeyHash("sk-low"), Priority: 20, Note: "Tier 1"},
	}
	userLimit := 8000
	toolLimit := 2000
	auditImages := false
	auditTools := false

	view, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{
		APIKeyUpdates:           &updates,
		AuditUserTextMaxChars:   &userLimit,
		AuditImages:             &auditImages,
		AuditToolOutputs:        &auditTools,
		AuditToolOutputMaxChars: &toolLimit,
	})

	require.NoError(t, err)
	require.Equal(t, 8000, view.AuditUserTextMaxChars)
	require.False(t, view.AuditImages)
	require.False(t, view.AuditToolOutputs)
	require.Equal(t, 2000, view.AuditToolOutputMaxChars)
	require.Equal(t, 100, view.APIKeyStatuses[0].Priority)
	require.Equal(t, "Tier 5", view.APIKeyStatuses[0].Note)
	require.Equal(t, 20, view.APIKeyStatuses[1].Priority)

	var saved ContentModerationConfig
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingKeyContentModerationConfig]), &saved))
	require.Equal(t, updates, saved.APIKeyMetadata)
}

// TestContentModerationUpdateConfigAddsKeyWithPriorityAndNote adds a credential
// entry with administrator scheduling attributes to an empty saved pool. It
// checks returned count, priority/note, and its literal key with hash-addressed
// stored metadata.
func TestContentModerationUpdateConfigAddsKeyWithPriorityAndNote(t *testing.T) {
	cfg := defaultContentModerationConfig()
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationTestSettingRepo{values: map[string]string{SettingKeyContentModerationConfig: string(rawCfg)}}
	svc := newTestModeration(repo, nil, nil, nil, nil, nil, nil)
	svc.Start()
	entries := []ContentModerationAPIKeyEntryInput{{APIKey: "sk-tier-five", Priority: 250, Note: "Tier 5 primary"}}

	view, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{APIKeyEntries: &entries})

	require.NoError(t, err)
	require.Equal(t, 1, view.APIKeyCount)
	require.Equal(t, 250, view.APIKeyStatuses[0].Priority)
	require.Equal(t, "Tier 5 primary", view.APIKeyStatuses[0].Note)
	var saved ContentModerationConfig
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingKeyContentModerationConfig]), &saved))
	require.Equal(t, []string{"sk-tier-five"}, saved.APIKeys)
	require.Equal(t, []ContentModerationAPIKeyMetadata{{
		KeyHash: moderationAPIKeyHash("sk-tier-five"), Priority: 250, Note: "Tier 5 primary",
	}}, saved.APIKeyMetadata)
}

// TestContentModerationUpdateConfigProxyIDSemantics updates a selected proxy,
// omits its field, clears it with zero, and supplies an absent ID. It checks
// retained/cleared returned selection and failed validation for the nonexistent
// proxy.
func TestContentModerationUpdateConfigProxyIDSemantics(t *testing.T) {
	settingRepo := &contentModerationTestSettingRepo{values: map[string]string{}}
	proxyRepo := &contentModerationTestProxyRepo{proxies: map[int64]*Proxy{
		5: {ID: 5, Name: "p", Protocol: "http", Host: "127.0.0.1", Port: 8080, Status: StatusActive},
	}}
	svc := newTestModeration(settingRepo, nil, nil, nil, nil, nil, nil)
	svc.Start()
	svc.SetProxyRepository(proxyRepo)
	ctx := context.Background()

	view, err := svc.UpdateConfig(ctx, UpdateContentModerationConfigInput{ProxyID: moderationProxyIDPtr(5)})
	if err != nil {
		t.Fatalf("set proxy_id=5: %v", err)
	}
	if view.ProxyID == nil || *view.ProxyID != 5 {
		t.Fatalf("expected proxy_id=5 in view, got %v", view.ProxyID)
	}

	// Omitting ProxyID leaves the selected proxy unchanged.
	enabled := true
	view, err = svc.UpdateConfig(ctx, UpdateContentModerationConfigInput{Enabled: &enabled})
	if err != nil {
		t.Fatalf("update unrelated field: %v", err)
	}
	if view.ProxyID == nil || *view.ProxyID != 5 {
		t.Fatalf("expected proxy_id to stay 5 when omitted, got %v", view.ProxyID)
	}

	// Zero clears the proxy and restores direct connections.
	view, err = svc.UpdateConfig(ctx, UpdateContentModerationConfigInput{ProxyID: moderationProxyIDPtr(0)})
	if err != nil {
		t.Fatalf("clear proxy_id: %v", err)
	}
	if view.ProxyID != nil {
		t.Fatalf("expected proxy_id cleared, got %v", *view.ProxyID)
	}

	// A nonexistent proxy ID fails validation.
	if _, err := svc.UpdateConfig(ctx, UpdateContentModerationConfigInput{ProxyID: moderationProxyIDPtr(404)}); err == nil {
		t.Fatal("expected validation error for nonexistent proxy")
	}
}

// TestContentModerationRuntimeSnapshotCachesSettings checks twenty clean
// requests through a keyword-only runtime snapshot with a long TTL. It checks
// one batch settings read and no dedicated value reads from those hot-path
// checks.
func TestContentModerationRuntimeSnapshotCachesSettings(t *testing.T) {
	repo := &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: runtimeCacheTestConfig(t, "blocked"),
	}}
	svc := runtimeCacheTestService(repo, time.Hour)

	for range 20 {
		decision, err := svc.Check(context.Background(), runtimeCacheTestInput("clean prompt"))
		require.NoError(t, err)
		require.True(t, decision.Allowed)
	}

	getValue, getMultiple := repo.calls()
	require.Zero(t, getValue)
	require.Equal(t, 1, getMultiple)
}

// TestContentModerationRuntimeSnapshotUpdateConfigIsImmediate loads old keyword
// policy, persists a new keyword, and checks it before TTL expiry. It checks
// the new block and reuse of the initial batch settings read count.
func TestContentModerationRuntimeSnapshotUpdateConfigIsImmediate(t *testing.T) {
	repo := &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: runtimeCacheTestConfig(t, "old-keyword"),
	}}
	svc := runtimeCacheTestService(repo, time.Hour)

	decision, err := svc.Check(context.Background(), runtimeCacheTestInput("new-keyword"))
	require.NoError(t, err)
	require.True(t, decision.Allowed)

	keywords := []string{"new-keyword"}
	_, err = svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{
		BlockedKeywords: &keywords,
	})
	require.NoError(t, err)

	decision, err = svc.Check(context.Background(), runtimeCacheTestInput("new-keyword"))
	require.NoError(t, err)
	require.True(t, decision.Blocked)

	_, getMultiple := repo.calls()
	require.Equal(t, 1, getMultiple)
}

// TestContentModerationRuntimeSnapshotUpdateWinsOverInitialLoad holds a fixture
// settings read after old values are captured and starts a configuration update
// concurrently. It checks the update waits for initial publication, then that
// new keywords block while old keywords allow.
func TestContentModerationRuntimeSnapshotUpdateWinsOverInitialLoad(t *testing.T) {
	repo := &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: runtimeCacheTestConfig(t, "old-keyword"),
	}}
	svc := runtimeCacheTestService(repo, time.Hour)

	refreshStarted := make(chan struct{}, 1)
	releaseRefresh := make(chan struct{})
	released := false
	defer func() {
		if !released {
			// On an early assertion exit, release the context-ignoring captured read so
			// it and any waiting update can leave the refresh lock.
			close(releaseRefresh)
		}
	}()
	repo.blockNextMultiple(refreshStarted, releaseRefresh)

	initialCheckDone := make(chan error, 1)
	// Start the initial read before submitting the update, and send its eventual
	// result through a buffered channel so releasing the gate can publish both
	// completions.
	go func() {
		decision, err := svc.Check(context.Background(), runtimeCacheTestInput("clean prompt"))
		if err == nil && (decision == nil || !decision.Allowed) {
			err = errors.New("unexpected initial moderation decision")
		}
		initialCheckDone <- err
	}()
	require.Eventually(t, func() bool {
		select {
		case <-refreshStarted:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	updateDone := make(chan error, 1)
	// Submit new keyword policy only after the initial read has captured old
	// values; the buffered result channel reports when the refresh lock permits
	// publication.
	go func() {
		keywords := []string{"new-keyword"}
		_, updateErr := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{
			BlockedKeywords: &keywords,
		})
		updateDone <- updateErr
	}()
	// The short interval checks premature completion while the read gate remains
	// held; later channel joins establish completion before checking final keyword decisions.
	select {
	case updateErr := <-updateDone:
		require.NoError(t, updateErr)
		t.Fatal("configuration update completed before the initial load released its lock")
	case <-time.After(10 * time.Millisecond):
	}

	// Release the old captured read before joining update completion, so its held
	// refresh lock no longer prevents the saved new policy from being published.
	close(releaseRefresh)
	released = true
	require.NoError(t, <-initialCheckDone)
	require.NoError(t, <-updateDone)

	decision, err := svc.Check(context.Background(), runtimeCacheTestInput("new-keyword"))
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	decision, err = svc.Check(context.Background(), runtimeCacheTestInput("old-keyword"))
	require.NoError(t, err)
	require.True(t, decision.Allowed)
}

// TestContentModerationRuntimeSnapshotRefreshFailureKeepsStaleConfig loads
// keyword policy, expires it immediately, and then fails batch settings reads.
// It checks stale blocking continues and an additional refresh read was
// attempted.
func TestContentModerationRuntimeSnapshotRefreshFailureKeepsStaleConfig(t *testing.T) {
	repo := &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: runtimeCacheTestConfig(t, "blocked"),
	}}
	svc := runtimeCacheTestService(repo, time.Nanosecond)
	input := runtimeCacheTestInput("blocked")

	decision, err := svc.Check(context.Background(), input)
	require.NoError(t, err)
	require.True(t, decision.Blocked)

	repo.failMultiple(errors.New("database unavailable"))
	decision, err = svc.Check(context.Background(), input)
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Eventually(t, func() bool {
		_, calls := repo.calls()
		return calls >= 2
	}, time.Second, time.Millisecond)
}

// TestContentModerationRuntimeSnapshotRefreshFailureBacksOff publishes an
// expired copied snapshot and fails its reload. It checks one hundred later
// requests still block on stale policy and the observed batch-read count
// remains two; it does not join the reload callback before that loop.
func TestContentModerationRuntimeSnapshotRefreshFailureBacksOff(t *testing.T) {
	repo := &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: runtimeCacheTestConfig(t, "blocked"),
	}}
	svc := runtimeCacheTestService(repo, time.Minute)
	input := runtimeCacheTestInput("blocked")

	decision, err := svc.Check(context.Background(), input)
	require.NoError(t, err)
	require.True(t, decision.Blocked)

	current := svc.runtimeSnapshot.Load()
	require.NotNil(t, current)
	// Copy the snapshot header before changing its load time; immutable policy
	// and matcher storage stay shared while this test publishes an expired cache entry.
	expired := *current
	expired.loadedAt = time.Now().Add(-2 * time.Minute)
	svc.runtimeSnapshot.Store(&expired)
	repo.failMultiple(errors.New("database unavailable"))

	decision, err = svc.Check(context.Background(), input)
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Eventually(t, func() bool {
		_, calls := repo.calls()
		return calls == 2
	}, time.Second, time.Millisecond)

	for range 100 {
		decision, err = svc.Check(context.Background(), input)
		require.NoError(t, err)
		require.True(t, decision.Blocked)
	}
	_, calls := repo.calls()
	require.Equal(t, 2, calls)
}

// TestContentModerationRuntimeSnapshotRefreshReusesUnchangedMatcher expires a
// snapshot without changing stored JSON and waits for renewed load time. It
// checks shared config/matcher pointers and exactly one additional settings
// read.
func TestContentModerationRuntimeSnapshotRefreshReusesUnchangedMatcher(t *testing.T) {
	repo := &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: runtimeCacheTestConfig(t, "blocked"),
	}}
	svc := runtimeCacheTestService(repo, time.Minute)
	input := runtimeCacheTestInput("blocked")

	decision, err := svc.Check(context.Background(), input)
	require.NoError(t, err)
	require.True(t, decision.Blocked)

	current := svc.runtimeSnapshot.Load()
	require.NotNil(t, current)
	// Copy the snapshot header before changing its load time; immutable policy
	// and matcher storage stay shared while this test publishes an expired cache entry.
	expired := *current
	expired.loadedAt = time.Now().Add(-2 * time.Minute)
	svc.runtimeSnapshot.Store(&expired)

	decision, err = svc.Check(context.Background(), input)
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Eventually(t, func() bool {
		refreshed := svc.runtimeSnapshot.Load()
		return refreshed != nil && refreshed.loadedAt.After(expired.loadedAt)
	}, time.Second, time.Millisecond)

	refreshed := svc.runtimeSnapshot.Load()
	require.Same(t, current.config, refreshed.config)
	require.Same(t, current.keywordMatcher, refreshed.keywordMatcher)
	_, calls := repo.calls()
	require.Equal(t, 2, calls)
}

// TestContentModerationRuntimeSnapshotUpdateWinsOverInFlightRefresh holds a
// reload after capturing old values while a dedicated update waits for the
// refresh lock. It checks the update cannot finish early and that new keyword
// policy wins after the gate is released.
func TestContentModerationRuntimeSnapshotUpdateWinsOverInFlightRefresh(t *testing.T) {
	repo := &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: runtimeCacheTestConfig(t, "old-keyword"),
	}}
	svc := runtimeCacheTestService(repo, time.Minute)

	decision, err := svc.Check(context.Background(), runtimeCacheTestInput("old-keyword"))
	require.NoError(t, err)
	require.True(t, decision.Blocked)

	current := svc.runtimeSnapshot.Load()
	require.NotNil(t, current)
	// Copy the snapshot header before changing its load time; immutable policy
	// and matcher storage stay shared while this test publishes an expired cache entry.
	expired := *current
	expired.loadedAt = time.Now().Add(-2 * time.Minute)
	svc.runtimeSnapshot.Store(&expired)

	refreshStarted := make(chan struct{}, 1)
	releaseRefresh := make(chan struct{})
	repo.blockNextMultiple(refreshStarted, releaseRefresh)
	decision, err = svc.Check(context.Background(), runtimeCacheTestInput("clean prompt"))
	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Eventually(t, func() bool {
		select {
		case <-refreshStarted:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	updateDone := make(chan error, 1)
	// Request the saved replacement after the stale-snapshot check launches and
	// gates a reload; the completion channel observes whether that update waits
	// for the reload lock.
	go func() {
		keywords := []string{"new-keyword"}
		_, updateErr := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{
			BlockedKeywords: &keywords,
		})
		updateDone <- updateErr
	}()
	// The short interval checks premature completion while the read gate remains
	// held; later channel joins establish completion before checking final keyword decisions.
	select {
	case updateErr := <-updateDone:
		require.NoError(t, updateErr)
		t.Fatal("configuration update completed before the in-flight refresh released its lock")
	case <-time.After(10 * time.Millisecond):
	}

	// Release the old captured read before joining update completion, so its held
	// refresh lock no longer prevents the saved new policy from being published.
	close(releaseRefresh)
	require.NoError(t, <-updateDone)
	decision, err = svc.Check(context.Background(), runtimeCacheTestInput("new-keyword"))
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	decision, err = svc.Check(context.Background(), runtimeCacheTestInput("old-keyword"))
	require.NoError(t, err)
	require.True(t, decision.Allowed)
}

// TestContentModerationRuntimeSnapshotConcurrentReadAndReplace runs eight
// clean-request readers while twenty keyword replacements are persisted
// serially. It joins every reader and checks no request error or unexpected
// blocking decision was returned.
func TestContentModerationRuntimeSnapshotConcurrentReadAndReplace(t *testing.T) {
	repo := &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: runtimeCacheTestConfig(t, "blocked-0"),
	}}
	svc := runtimeCacheTestService(repo, time.Hour)
	_, err := svc.Check(context.Background(), runtimeCacheTestInput("clean prompt"))
	require.NoError(t, err)

	// Each of eight readers reports at most one error into an eight-slot channel;
	// this capacity permits joining readers before the caller drains their observations.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			for range 100 {
				decision, checkErr := svc.Check(context.Background(), runtimeCacheTestInput("clean prompt"))
				if checkErr != nil {
					errs <- checkErr
					return
				}
				if decision == nil || !decision.Allowed {
					errs <- errors.New("unexpected moderation decision")
					return
				}
			}
		})
	}
	for i := 1; i <= 20; i++ {
		// Duration formatting supplies distinct fixture keyword suffixes for each
		// replacement.
		keywords := []string{"blocked-" + time.Duration(i).String()}
		_, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{
			BlockedKeywords: &keywords,
		})
		require.NoError(t, err)
	}
	// Join every possible sender before closing error collection; the final loop
	// can then drain observations until channel closure without racing another send.
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

// TestContentModerationConfigNormalize_NonHitRetentionMaxThreeDays supplies a
// non-hit retention period above the supported cap. It checks normalization
// selects three days while the rest of the default policy remains fixture
// input.
func TestContentModerationConfigNormalize_NonHitRetentionMaxThreeDays(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.NonHitRetentionDays = 30

	cfg.normalize()

	require.Equal(t, 3, cfg.NonHitRetentionDays)
}

// TestNormalizeBlockedKeywords_TrimsDedupesAndCaps supplies spaced,
// case-duplicate, blank, and repeated keyword values. It checks retained first
// spelling and order after normalization; this fixture does not reach the
// configured entry or rune caps.
func TestNormalizeBlockedKeywords_TrimsDedupesAndCaps(t *testing.T) {
	out := normalizeBlockedKeywords([]string{"  foo ", "FOO", "", "bar", "baz", "bar"})
	require.Equal(t, []string{"foo", "bar", "baz"}, out)
}

// TestMatchBlockedKeyword_CaseInsensitiveSubstring supplies a mixed-case
// matching substring, clean text, and an absent keyword list. It checks
// returned configured spelling for the hit and false results for both misses.
func TestMatchBlockedKeyword_CaseInsensitiveSubstring(t *testing.T) {
	keyword, hit := matchBlockedKeyword("Please ignore the BadWord here", []string{"badword"})
	require.True(t, hit)
	require.Equal(t, "badword", keyword)

	_, hit = matchBlockedKeyword("clean prompt", []string{"badword"})
	require.False(t, hit)

	_, hit = matchBlockedKeyword("anything", nil)
	require.False(t, hit)
}

// TestNormalizeKeywordBlockingMode_UnknownFallsBackToDefault checks empty,
// unknown, and accepted keyword strategy strings. It protects combined-mode
// defaulting alongside exact accepted keyword-only and API-only values, without
// testing every normalized spelling.
func TestNormalizeKeywordBlockingMode_UnknownFallsBackToDefault(t *testing.T) {
	require.Equal(t, ContentModerationKeywordModeKeywordAndAPI, normalizeKeywordBlockingMode(""))
	require.Equal(t, ContentModerationKeywordModeKeywordAndAPI, normalizeKeywordBlockingMode("bogus"))
	require.Equal(t, ContentModerationKeywordModeKeywordOnly, normalizeKeywordBlockingMode("keyword_only"))
	require.Equal(t, ContentModerationKeywordModeAPIOnly, normalizeKeywordBlockingMode("api_only"))
}

// TestContentModerationLoadConfig_LegacyConfigDefaultsModelFilterToAll loads
// saved JSON with no model-filter field. It checks default all-model scope, an
// empty names list, and admission for the two requested model names.
func TestContentModerationLoadConfig_LegacyConfigDefaultsModelFilterToAll(t *testing.T) {
	raw := `{"enabled":true,"mode":"pre_block","base_url":"https://api.openai.com","model":"omni-moderation-latest","blocked_keywords":["secret-token"]}`
	svc := newTestModeration(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyContentModerationConfig: raw,
		}},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	svc.Start()

	cfg, err := svc.loadConfig(context.Background())

	require.NoError(t, err)
	require.Equal(t, ContentModerationModelFilterAll, cfg.ModelFilter.Type)
	require.Empty(t, cfg.ModelFilter.Models)
	require.True(t, cfg.includesModel("gpt-5.5"))
	require.True(t, cfg.includesModel("gpt-5.4"))
}

// TestContentModerationUpdateConfig_AppendsAndDeletesAPIKeys deletes one saved
// credential by hash and appends a new key alongside a duplicate. It checks
// remaining order, returned masks/count, and literal saved credentials after
// decoding.
func TestContentModerationUpdateConfig_AppendsAndDeletesAPIKeys(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.APIKeys = []string{"sk-old-a", "sk-old-b"}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyContentModerationConfig: string(rawCfg),
	}}
	svc := newTestModeration(repo, nil, nil, nil, nil, nil, nil)
	svc.Start()
	deleteHashes := []string{moderationAPIKeyHash("sk-old-a")}
	addKeys := []string{"sk-new-c", "sk-old-b"}

	view, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{
		APIKeys:            &addKeys,
		DeleteAPIKeyHashes: &deleteHashes,
	})

	require.NoError(t, err)
	require.Equal(t, 2, view.APIKeyCount)
	require.Equal(t, []string{maskSecretTail("sk-old-b"), maskSecretTail("sk-new-c")}, view.APIKeyMasks)

	var saved ContentModerationConfig
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingKeyContentModerationConfig]), &saved))
	require.Equal(t, []string{"sk-old-b", "sk-new-c"}, saved.apiKeys())
}

// TestContentModerationUpdateConfig_ReplacesAPIKeysWhenRequested supplies
// replacement credentials and a deletion list together. It checks replacement
// leaves one supplied key in the view and saved policy, independent of prior
// key order.
func TestContentModerationUpdateConfig_ReplacesAPIKeysWhenRequested(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.APIKeys = []string{"sk-old-a", "sk-old-b"}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyContentModerationConfig: string(rawCfg),
	}}
	svc := newTestModeration(repo, nil, nil, nil, nil, nil, nil)
	svc.Start()
	deleteHashes := []string{moderationAPIKeyHash("sk-old-a")}
	replaceKeys := []string{"sk-new-only"}

	view, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{
		APIKeys:            &replaceKeys,
		APIKeysMode:        contentModerationAPIKeysModeReplace,
		DeleteAPIKeyHashes: &deleteHashes,
	})

	require.NoError(t, err)
	require.Equal(t, 1, view.APIKeyCount)
	require.Equal(t, []string{maskSecretTail("sk-new-only")}, view.APIKeyMasks)

	var saved ContentModerationConfig
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingKeyContentModerationConfig]), &saved))
	require.Equal(t, []string{"sk-new-only"}, saved.apiKeys())
}

// TestContentModerationUpdateConfig_SavesCustomThresholds supplies a valid
// category override, excessive category value, and unknown category. It checks
// the accepted score, upper clamp, and omitted unknown key in both returned and
// saved thresholds.
func TestContentModerationUpdateConfig_SavesCustomThresholds(t *testing.T) {
	cfg := defaultContentModerationConfig()
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyContentModerationConfig: string(rawCfg),
	}}
	svc := newTestModeration(repo, nil, nil, nil, nil, nil, nil)
	svc.Start()
	thresholds := map[string]float64{
		"sexual":     0.72,
		"harassment": 1.25,
		"unknown":    0.01,
	}

	view, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{
		Thresholds: &thresholds,
	})

	require.NoError(t, err)
	require.Equal(t, 0.72, view.Thresholds["sexual"])
	require.Equal(t, 1.0, view.Thresholds["harassment"])
	require.NotContains(t, view.Thresholds, "unknown")

	var saved ContentModerationConfig
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingKeyContentModerationConfig]), &saved))
	require.Equal(t, 0.72, saved.Thresholds["sexual"])
	require.Equal(t, 1.0, saved.Thresholds["harassment"])
	require.NotContains(t, saved.Thresholds, "unknown")
}

// TestTriggerRuntimeSnapshotRefreshReleasesMutexWhenBackgroundRejected triggers
// a background runtime snapshot refresh with a Runtime.Background callback that
// rejects task admission. It checks that the callback ran and runtimeRefreshMu
// is immediately unlocked afterward so subsequent updates do not deadlock.
func TestTriggerRuntimeSnapshotRefreshReleasesMutexWhenBackgroundRejected(t *testing.T) {
	settings := &contentModerationRuntimeSettingRepo{
		values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: runtimeCacheTestConfig(t, "blocked"),
		},
	}
	repo := &contentModerationTestRepo{}
	rt := testModerationRuntime()
	rejected := atomic.Bool{}
	rt.Background = func(_ string, _ func()) bool {
		rejected.Store(true)
		return false
	}
	svc := NewContentModerationService(settings, repo, nil, nil, nil, nil, nil, rt)

	svc.triggerRuntimeSnapshotRefresh()
	require.True(t, rejected.Load())
	require.True(t, svc.runtimeRefreshMu.TryLock(), "runtimeRefreshMu must be unlocked when Background returns false")
	svc.runtimeRefreshMu.Unlock()
}
