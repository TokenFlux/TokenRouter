package moderation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestContentModerationEmptyAuditScopeSkipsObserveQueue disables tool and image
// auditing for a tool-only current turn. It checks an allowed decision, an
// empty observe queue, and no stored fixture log.
func TestContentModerationEmptyAuditScopeSkipsObserveQueue(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModeObserve
	cfg.AuditToolOutputs = false
	cfg.AuditImages = false
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationTestRepo{}
	svc := &ContentModerationService{
		runtime: testModerationRuntime(),
		settingRepo: &contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		repo:       repo,
		asyncQueue: make(chan contentModerationTask, 1),
	}
	body := []byte(`{"messages":[{"role":"assistant","content":"answer"},{"role":"tool","content":"tool-output"}]}`)

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{Protocol: ContentModerationProtocolOpenAIChat, Body: body})

	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Empty(t, svc.asyncQueue)
	require.Empty(t, repo.snapshotLogs())
}

// TestContentModerationAsyncQueueDropsDuplicateBodyAndTracksBytes admits one
// observe task without starting a worker and receives it directly. It checks
// dropped raw body and aggregate duplication, reconstructable item text, a
// positive reservation, and counter release.
func TestContentModerationAsyncQueueDropsDuplicateBodyAndTracksBytes(t *testing.T) {
	svc := &ContentModerationService{runtime: testModerationRuntime(), asyncQueue: make(chan contentModerationTask, 1)}
	content := ContentModerationInput{
		Text:  "完整文本",
		Items: []ContentModerationInputItem{{Index: 0, Source: ContentModerationSourceUser, Type: ContentModerationItemTypeText, Text: "完整文本"}},
	}
	svc.enqueueAsync(ContentModerationCheckInput{Endpoint: "/v1/chat/completions", Body: []byte("raw body")}, &ContentModerationConfig{QueueSize: 1}, content, "hash")

	// Receive directly because this fixture starts no worker; the testcase now
	// owns the accepted task's reserved byte cost and releases it after inspecting
	// its queued representation.
	task := <-svc.asyncQueue
	require.Nil(t, task.input.Body)
	require.Empty(t, task.content.Text)
	require.Equal(t, "完整文本", contentModerationTextFromItems(task.content.Items))
	require.Positive(t, task.bufferedBytes)
	require.Equal(t, task.bufferedBytes, svc.asyncBufferedBytes.Load())
	svc.releaseContentModerationBufferedBytes(task.bufferedBytes)
	require.Zero(t, svc.asyncBufferedBytes.Load())
}

// TestContentModerationAsyncQueueRejectsTaskBeyondByteBudget preloads the byte
// counter just below its cap and attempts another task. It checks queue
// rejection, unchanged reserved bytes, and one additional dropped observation.
func TestContentModerationAsyncQueueRejectsTaskBeyondByteBudget(t *testing.T) {
	svc := &ContentModerationService{runtime: testModerationRuntime(), asyncQueue: make(chan contentModerationTask, 1)}
	svc.asyncBufferedBytes.Store(maxContentModerationBufferedBytes - 1)
	content := ContentModerationInput{Text: "text", Items: []ContentModerationInputItem{{Type: ContentModerationItemTypeText, Text: "text"}}}

	svc.enqueueAsync(ContentModerationCheckInput{}, &ContentModerationConfig{QueueSize: 1}, content, "hash")

	require.Empty(t, svc.asyncQueue)
	require.Equal(t, maxContentModerationBufferedBytes-1, svc.asyncBufferedBytes.Load())
	require.Equal(t, int64(1), svc.asyncDropped.Load())
}

// TestContentModerationObserveQueueOverflowRecordsIncompleteAudit fills a
// one-task observe queue before submitting current-turn text. It checks allowed
// admission plus an inline error log with incomplete audit status,
// request-level failure, and retained input text.
func TestContentModerationObserveQueueOverflowRecordsIncompleteAudit(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModeObserve
	cfg.QueueSize = 1
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationTestRepo{}
	svc := &ContentModerationService{
		runtime: testModerationRuntime(),
		settingRepo: &contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		repo:       repo,
		asyncQueue: make(chan contentModerationTask, 1),
	}
	// Preload a placeholder while no worker is running, so a consumer cannot free
	// this slot before Check samples queue length and chooses inline error
	// recording.
	svc.asyncQueue <- contentModerationTask{}

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     []byte(`{"messages":[{"role":"user","content":"audit me"}]}`),
	})

	require.NoError(t, err)
	require.True(t, decision.Allowed)
	logs := requireContentModerationLogCount(t, repo, 1)
	require.Equal(t, ContentModerationActionError, logs[0].Action)
	require.False(t, logs[0].AuditComplete)
	require.Equal(t, ContentModerationItemTypeRequest, logs[0].FailedUnits[0].Type)
	require.Equal(t, "audit me", logs[0].InputItems[0].Text)
}

// TestContentModerationStatusTracksPreBlockSyncMetrics sends one flagged and
// one safe text input through a deliberately delayed local endpoint. It checks
// two completed decisions, their blocked/allowed counts, zero active/error
// counts, and a positive sampled average latency.
func TestContentModerationStatusTracksPreBlockSyncMetrics(t *testing.T) {
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		score := 0.01
		if requestCount == 1 {
			score = 0.9
		}
		// Introduce measurable fixture latency for integer-millisecond status averaging.
		time.Sleep(5 * time.Millisecond)
		// Return each selected score after injected latency so completed-decision
		// counters can be inspected. Writer errors are ignored by this fixture, whose
		// service outcomes are asserted below.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{
			Results: []moderationAPIResult{{
				CategoryScores: map[string]float64{"sexual": score},
			}},
		})
	}))
	defer server.Close()

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	svc := newTestModeration(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		&contentModerationTestRepo{},
		&contentModerationTestHashCache{},
		nil,
		nil,
		nil,
		nil,
	)
	svc.Start()

	for _, prompt := range []string{"blocked prompt", "clean prompt"} {
		_, err := svc.Check(context.Background(), ContentModerationCheckInput{
			UserID:   1001,
			Protocol: ContentModerationProtocolOpenAIChat,
			Body:     []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":%q}]}`, prompt)),
		})
		require.NoError(t, err)
	}

	status, err := svc.GetStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), status.PreBlockChecked)
	require.Equal(t, int64(1), status.PreBlockAllowed)
	require.Equal(t, int64(1), status.PreBlockBlocked)
	require.Equal(t, int64(0), status.PreBlockErrors)
	require.Equal(t, 0, status.PreBlockActive)
	require.GreaterOrEqual(t, status.PreBlockAvgLatencyMS, int64(1))
}

// TestContentModerationStatusTracksPreBlockAPIKeyLoad sends four safe
// synchronous audits through two equally weighted credentials. It checks total
// calls, available and active counts, and two successful completed attempts for
// each displayed key.
func TestContentModerationStatusTracksPreBlockAPIKeyLoad(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return successful scores for per-key attempt counting. This fixture ignores
		// response-write errors; the case checks completed success, error, and
		// activity observations after the calls.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{
			Results: []moderationAPIResult{{
				CategoryScores: map[string]float64{"sexual": 0.01},
			}},
		})
	}))
	defer server.Close()

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-one", "sk-two"}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	svc := newTestModeration(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		&contentModerationTestRepo{},
		&contentModerationTestHashCache{},
		nil,
		nil,
		nil,
		nil,
	)
	svc.Start()

	for idx := range 4 {
		_, err := svc.Check(context.Background(), ContentModerationCheckInput{
			UserID:   1001,
			Protocol: ContentModerationProtocolOpenAIChat,
			Body:     []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":"prompt %d"}]}`, idx)),
		})
		require.NoError(t, err)
	}

	status, err := svc.GetStatus(context.Background())
	require.NoError(t, err)
	require.Len(t, status.PreBlockAPIKeyLoads, 2)
	require.Equal(t, int64(4), status.PreBlockAPIKeyTotalCalls)
	require.Equal(t, int64(2), status.PreBlockAPIKeyAvailableCount)
	require.Equal(t, int64(0), status.PreBlockAPIKeyActive)
	require.Equal(t, int64(0), status.PreBlockAPIKeyLoads[0].Active)
	require.Equal(t, int64(2), status.PreBlockAPIKeyLoads[0].Total)
	require.Equal(t, int64(2), status.PreBlockAPIKeyLoads[0].Success)
	require.Equal(t, int64(0), status.PreBlockAPIKeyLoads[0].Errors)
	require.Equal(t, int64(2), status.PreBlockAPIKeyLoads[1].Total)
	require.Equal(t, int64(2), status.PreBlockAPIKeyLoads[1].Success)
}

// TestContentModerationStatusTracksPreBlockLocalBlocks sends one keyword hit
// and one miss under keyword-only pre-block policy. It checks two counted
// decisions split between one blocked and one allowed, with no error outcome.
func TestContentModerationStatusTracksPreBlockLocalBlocks(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.KeywordBlockingMode = ContentModerationKeywordModeKeywordOnly
	cfg.BlockedKeywords = []string{"blocked"}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	svc := newTestModeration(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		&contentModerationTestRepo{},
		&contentModerationTestHashCache{},
		nil,
		nil,
		nil,
		nil,
	)
	svc.Start()

	for _, prompt := range []string{"blocked prompt", "clean prompt"} {
		_, err := svc.Check(context.Background(), ContentModerationCheckInput{
			UserID:   1001,
			Protocol: ContentModerationProtocolOpenAIChat,
			Body:     []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":%q}]}`, prompt)),
		})
		require.NoError(t, err)
	}

	status, err := svc.GetStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), status.PreBlockChecked)
	require.Equal(t, int64(1), status.PreBlockAllowed)
	require.Equal(t, int64(1), status.PreBlockBlocked)
	require.Equal(t, int64(0), status.PreBlockErrors)
}

// TestStopContextSealsAdmissionSynchronouslyOnExpiredContext calls StopContext
// with an already-canceled context while one async audit worker is blocked in
// flight. It checks that StopContext returns context.Canceled immediately after
// sealing admission, rejects later enqueues, and drains cleanly once released.
func TestStopContextSealsAdmissionSynchronouslyOnExpiredContext(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-gate
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"flagged":false,"categories":{},"category_scores":{}}]}`))
	}))
	defer server.Close()

	svc, _ := newExpandedModerationTestService(t, server.URL, true)
	svc.Start()
	cfg, err := svc.loadConfig(context.Background())
	require.NoError(t, err)

	content := ContentModerationInput{Text: "hello"}
	content.Normalize()
	require.True(t, svc.enqueueAsync(ContentModerationCheckInput{UserID: 1, Endpoint: "/v1/responses"}, cfg, content, content.Hash()))
	<-entered

	expiredCtx, cancel := context.WithCancel(context.Background())
	cancel()
	stopErr := svc.StopContext(expiredCtx)
	require.ErrorIs(t, stopErr, context.Canceled)
	require.False(t, svc.enqueueAsync(ContentModerationCheckInput{UserID: 1, Endpoint: "/v1/responses"}, cfg, content, content.Hash()))

	close(gate)
	require.NoError(t, svc.Stop())
}
