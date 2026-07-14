package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitContentModerationTextKeepsOverlapAndTail(t *testing.T) {
	chunks := splitContentModerationText("abcdefghij", 6, 2)

	require.Equal(t, []string{"abcdef", "efghij"}, chunks)
}

func TestContentModerationTextBatchingDoesNotCreateOverlapOnlyTail(t *testing.T) {
	var batches [][]string
	forEachContentModerationTextBatch("abcdef", 6, 2, 2, func(_ int, chunks []string) {
		batches = append(batches, append([]string(nil), chunks...))
	})

	require.Equal(t, [][]string{{"abcdef"}}, batches)
	require.Equal(t, 1, countContentModerationTextChunks("abcdef", 6, 2))
	require.Equal(t, 3, countContentModerationTextChunks("abcdefghijk", 6, 2))
}

func TestContentModerationNormalizePreservesFullOriginalText(t *testing.T) {
	input := ContentModerationInput{Text: "  first line\nsecond line  "}

	input.Normalize()

	require.Equal(t, "  first line\nsecond line  ", input.Text)
	require.Equal(t, input.Text, input.Items[0].Text)
}

func TestContentModerationAsyncQueueDropsDuplicateBodyAndTracksBytes(t *testing.T) {
	svc := &ContentModerationService{asyncQueue: make(chan contentModerationTask, 1)}
	content := ContentModerationInput{
		Text:  "完整文本",
		Items: []ContentModerationInputItem{{Index: 0, Source: ContentModerationSourceUser, Type: ContentModerationItemTypeText, Text: "完整文本"}},
	}
	svc.enqueueAsync(ContentModerationCheckInput{Endpoint: "/v1/chat/completions", Body: []byte("raw body")}, &ContentModerationConfig{QueueSize: 1}, content, "hash")

	task := <-svc.asyncQueue
	require.Nil(t, task.input.Body)
	require.Empty(t, task.content.Text)
	require.Equal(t, "完整文本", contentModerationTextFromItems(task.content.Items))
	require.Positive(t, task.bufferedBytes)
	require.Equal(t, task.bufferedBytes, svc.asyncBufferedBytes.Load())
	svc.releaseContentModerationBufferedBytes(task.bufferedBytes)
	require.Zero(t, svc.asyncBufferedBytes.Load())
}

func TestContentModerationAsyncQueueRejectsTaskBeyondByteBudget(t *testing.T) {
	svc := &ContentModerationService{asyncQueue: make(chan contentModerationTask, 1)}
	svc.asyncBufferedBytes.Store(maxContentModerationBufferedBytes - 1)
	content := ContentModerationInput{Text: "text", Items: []ContentModerationInputItem{{Type: ContentModerationItemTypeText, Text: "text"}}}

	svc.enqueueAsync(ContentModerationCheckInput{}, &ContentModerationConfig{QueueSize: 1}, content, "hash")

	require.Empty(t, svc.asyncQueue)
	require.Equal(t, maxContentModerationBufferedBytes-1, svc.asyncBufferedBytes.Load())
	require.Equal(t, int64(1), svc.asyncDropped.Load())
}

func TestContentModerationObserveQueueOverflowRecordsIncompleteAudit(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModeObserve
	cfg.QueueSize = 1
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationTestRepo{}
	svc := &ContentModerationService{
		settingRepo: &contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		repo:       repo,
		asyncQueue: make(chan contentModerationTask, 1),
	}
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

func TestContentModerationCheckAuditsTextBeyondFirstChunkAndStoresFullInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inputs := decodeModerationTextInputs(t, r)
		results := make([]moderationAPIResult, 0, len(inputs))
		for _, input := range inputs {
			score := 0.01
			if strings.Contains(input, "tail-risk") {
				score = 0.9
			}
			results = append(results, moderationAPIResult{CategoryScores: map[string]float64{"sexual": score}})
		}
		require.NoError(t, json.NewEncoder(w).Encode(moderationAPIResponse{Results: results}))
	}))
	defer server.Close()

	text := strings.Repeat("a", maxModerationInputRunes+100) + "tail-risk"
	svc, repo := newExpandedModerationTestService(t, server.URL, false)
	body, err := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": text}}})
	require.NoError(t, err)

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{Protocol: ContentModerationProtocolOpenAIChat, Body: body})

	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Equal(t, 0.9, decision.HighestScore)
	logs := requireContentModerationLogCount(t, repo, 1)
	require.True(t, logs[0].ContentComplete)
	require.True(t, logs[0].AuditComplete)
	require.Equal(t, 2, logs[0].TextUnitCount)
	require.Equal(t, 0.9, logs[0].CategoryScores["sexual"])
	require.Equal(t, text, logs[0].InputItems[0].Text)
}

func TestContentModerationBatchFailureFallsBackToSingleTextUnits(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload struct {
			Input json.RawMessage `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		var batch []string
		if json.Unmarshal(payload.Input, &batch) == nil && len(batch) > 1 {
			http.Error(w, "batch unsupported", http.StatusBadRequest)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{CategoryScores: map[string]float64{"sexual": 0.01}}}}))
	}))
	defer server.Close()

	text := strings.Repeat("a", maxModerationInputRunes+100)
	svc, repo := newExpandedModerationTestService(t, server.URL, true)
	body, err := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": text}}})
	require.NoError(t, err)

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{Protocol: ContentModerationProtocolOpenAIChat, Body: body})

	require.NoError(t, err)
	require.False(t, decision.Blocked)
	require.Equal(t, int64(3), calls.Load())
	logs := requireContentModerationLogCount(t, repo, 1)
	require.True(t, logs[0].AuditComplete)
	require.Equal(t, 2, logs[0].TextUnitCount)
}

func TestContentModerationTransientBatchFailureDoesNotFanOut(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	text := strings.Repeat("a", maxModerationInputRunes+100)
	svc, repo := newExpandedModerationTestService(t, server.URL, false)
	body, err := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": text}}})
	require.NoError(t, err)

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{Protocol: ContentModerationProtocolOpenAIChat, Body: body})

	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Equal(t, int64(1), calls.Load())
	logs := requireContentModerationLogCount(t, repo, 1)
	require.False(t, logs[0].AuditComplete)
	require.Equal(t, 2, logs[0].FailedUnitCount)
}

func TestContentModerationCheckAuditsAllImagesAndSnapshotsOnlyHit(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw := decodeModerationRawInput(t, r)
		score := 0.01
		if strings.Contains(string(raw), "marker=flag") {
			score = 0.9
		}
		require.NoError(t, json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{CategoryScores: map[string]float64{"sexual": score}}}}))
	}))
	defer server.Close()

	imageData := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M/wHwAF/gL+Z8dZAAAAAElFTkSuQmCC"
	first := "data:image/png;marker=pass;base64," + imageData
	second := "data:image/png;marker=flag;base64," + imageData
	body, err := json.Marshal(map[string]any{"messages": []any{map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": first}},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": second}},
		},
	}}})
	require.NoError(t, err)
	svc, repo := newExpandedModerationTestService(t, server.URL, false)

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{Protocol: ContentModerationProtocolOpenAIChat, Body: body})

	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Equal(t, int64(2), calls.Load())
	logs := requireContentModerationLogCount(t, repo, 1)
	require.Equal(t, 2, logs[0].ImageUnitCount)
	require.Len(t, logs[0].Media, 1)
	require.Equal(t, second, logs[0].Media[0].OriginalRef)
	require.Equal(t, "ready", logs[0].Media[0].SnapshotStatus)
	require.NotEmpty(t, logs[0].Media[0].Content)
}

func TestContentModerationPartialFailureStillBlocksAndRecordsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := decodeModerationRawInput(t, r)
		if strings.Contains(string(raw), "marker=fail") {
			http.Error(w, "image rejected", http.StatusBadRequest)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{CategoryScores: map[string]float64{"sexual": 0.9}}}}))
	}))
	defer server.Close()

	imageData := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M/wHwAF/gL+Z8dZAAAAAElFTkSuQmCC"
	body, err := json.Marshal(map[string]any{"messages": []any{map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;marker=fail;base64," + imageData}},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;marker=flag;base64," + imageData}},
		},
	}}})
	require.NoError(t, err)
	svc, repo := newExpandedModerationTestService(t, server.URL, false)

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{Protocol: ContentModerationProtocolOpenAIChat, Body: body})

	require.NoError(t, err)
	require.True(t, decision.Blocked)
	logs := requireContentModerationLogCount(t, repo, 1)
	require.True(t, logs[0].Flagged)
	require.False(t, logs[0].AuditComplete)
	require.Equal(t, 1, logs[0].FailedUnitCount)
	require.Equal(t, ContentModerationItemTypeImage, logs[0].FailedUnits[0].Type)
}

func TestContentModerationAllFailuresFailOpenAndAlwaysRecord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	svc, repo := newExpandedModerationTestService(t, server.URL, false)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     []byte(`{"messages":[{"role":"user","content":"audit me"}]}`),
	})

	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.False(t, decision.Blocked)
	logs := requireContentModerationLogCount(t, repo, 1)
	require.Equal(t, ContentModerationActionError, logs[0].Action)
	require.False(t, logs[0].AuditComplete)
	require.Equal(t, 1, logs[0].FailedUnitCount)
}

func TestContentModerationRemoteSnapshotRejectsPrivateAddress(t *testing.T) {
	_, _, err := fetchContentModerationImage(context.Background(), "http://127.0.0.1/private.png")

	require.Error(t, err)
	require.Contains(t, err.Error(), "私网")
}

func TestContentModerationSnapshotRejectsWrongMIMEAndPrivateRedirect(t *testing.T) {
	_, err := normalizeContentModerationImageMIME("text/plain")
	require.Error(t, err)

	client := newContentModerationSnapshotClient()
	redirect := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/redirected.png", nil)
	require.Error(t, client.CheckRedirect(redirect, nil))
}

func TestContentModerationDataImageRejectsDecodedPayloadOverLimit(t *testing.T) {
	payload := strings.Repeat("A", base64.StdEncoding.EncodedLen(maxContentModerationSnapshotBytes+1))

	_, _, err := decodeContentModerationDataImage("data:image/png;base64," + payload)

	require.Error(t, err)
	require.Contains(t, err.Error(), "20 MB")
}

func TestContentModerationSnapshotTotalByteBudgetIsAtomic(t *testing.T) {
	var retained atomic.Int64

	require.True(t, reserveContentModerationSnapshotBytes(&retained, maxContentModerationSnapshotTotalBytes-1))
	require.False(t, reserveContentModerationSnapshotBytes(&retained, 2))
	require.Equal(t, int64(maxContentModerationSnapshotTotalBytes-1), retained.Load())
}

func TestContentModerationSnapshotStopsImmediatelyWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	media := make([]ContentModerationMedia, 100)
	for index := range media {
		media[index] = ContentModerationMedia{OriginalRef: "https://example.com/image.png", SnapshotStatus: "pending"}
	}

	result := (&ContentModerationService{}).snapshotContentModerationMedia(ctx, media)

	require.Len(t, result, len(media))
	for _, item := range result {
		require.Equal(t, "error", item.SnapshotStatus)
		require.Contains(t, item.SnapshotError, context.Canceled.Error())
	}
}

func newExpandedModerationTestService(t *testing.T, baseURL string, recordNonHits bool) (*ContentModerationService, *contentModerationTestRepo) {
	t.Helper()
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.BaseURL = baseURL
	cfg.APIKeys = []string{"sk-test"}
	cfg.RetryCount = 0
	cfg.RecordNonHits = recordNonHits
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationTestRepo{}
	service := NewContentModerationService(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		repo,
		&contentModerationTestHashCache{},
		nil,
		nil,
		nil,
		nil,
	)
	return service, repo
}

func decodeModerationRawInput(t *testing.T, r *http.Request) json.RawMessage {
	t.Helper()
	var payload struct {
		Input json.RawMessage `json:"input"`
	}
	require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
	return payload.Input
}

func decodeModerationTextInputs(t *testing.T, r *http.Request) []string {
	t.Helper()
	raw := decodeModerationRawInput(t, r)
	var batch []string
	if err := json.Unmarshal(raw, &batch); err == nil {
		return batch
	}
	var single string
	require.NoError(t, json.Unmarshal(raw, &single))
	return []string{single}
}
