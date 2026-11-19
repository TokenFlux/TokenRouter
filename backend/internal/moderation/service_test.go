package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestContentModerationAuditScopeLimitsUpstreamButStoresFullInput sends mixed
// tool/user text through a local audit server with separate source limits. It
// checks the shorter outgoing aggregate and full retained review items after
// record publication.
func TestContentModerationAuditScopeLimitsUpstreamButStoresFullInput(t *testing.T) {
	// Buffer the observation so the HTTP handler can finish its reply before
	// Check returns and the testcase reads the captured audit input.
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inputs := decodeModerationTextInputs(t, r)
		received <- inputs[0]
		require.NoError(t, json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{CategoryScores: map[string]float64{"sexual": 0.01}}}}))
	}))
	defer server.Close()

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	cfg.RetryCount = 0
	cfg.RecordNonHits = true
	cfg.AuditUserTextMaxChars = 5
	cfg.AuditToolOutputMaxChars = 4
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationTestRepo{}
	svc := newTestModeration(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		repo,
		&contentModerationTestHashCache{},
		nil, nil, nil, nil,
	)
	svc.Start()
	body := []byte(`{"messages":[{"role":"user","content":"old"},{"role":"assistant","content":"answer"},{"role":"tool","content":"tool-output"},{"role":"user","content":"user-content"}]}`)

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{Protocol: ContentModerationProtocolOpenAIChat, Body: body})

	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Equal(t, "tool\nuser-", <-received)
	logs := requireContentModerationLogCount(t, repo, 1)
	require.Len(t, logs[0].InputItems, 2)
	require.Equal(t, "tool-output", logs[0].InputItems[0].Text)
	require.Equal(t, "user-content", logs[0].InputItems[1].Text)
}

// TestContentModerationCheckAuditsTextBeyondFirstChunkAndStoresFullInput places
// a risk marker beyond the first text chunk at a local audit endpoint. It
// checks blocking from the later score and retained complete input with two
// successful audit units.
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

// TestContentModerationCheckAuditsAllImagesAndSnapshotsOnlyHit returns
// different scores for two inline image references at a local audit endpoint.
// It checks two audit calls, blocking, and one ready retained snapshot
// corresponding to the flagged image.
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

// TestContentModerationPartialFailureStillBlocksAndRecordsFailure fails one
// image audit and flags another image in the same request. It checks blocking
// from the successful hit and an incomplete record containing one image
// failure.
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

// TestContentModerationAllFailuresFailOpenAndAlwaysRecord makes the local audit
// endpoint return HTTP 503 for the sole input unit. It checks allowed
// admission, no block, and an error record even when successful non-hit logging
// is disabled.
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

// TestBuildContentModerationLog_RedactsInputExcerpt builds an ordinary summary
// from text containing a recognizable credential. It checks the credential
// disappears from InputExcerpt and a redaction marker is present.
func TestBuildContentModerationLog_RedactsInputExcerpt(t *testing.T) {
	svc := &ContentModerationService{runtime: testModerationRuntime()}
	cfg := defaultContentModerationConfig()
	input := ContentModerationCheckInput{
		RequestID: "req-1",
		Endpoint:  "/v1/chat/completions",
		Provider:  "openai",
	}

	log := svc.buildTextLogForTest(input, cfg, ContentModerationActionAllow, true, "sexual", 0.8, map[string]float64{"sexual": 0.8}, "hello sk-proj-1234567890abcdef", nil, nil, "")

	require.NotContains(t, log.InputExcerpt, "sk-proj-1234567890abcdef")
	require.Contains(t, log.InputExcerpt, "[REDACTED]")
}

// TestContentModerationCheck_PreBlockKeywordHitSkipsUpstreamCall submits a
// configured mixed-case keyword hit under pre-block mode with a local endpoint
// present. It checks keyword rejection, no API request, and stored
// category/keyword observations after record publication.
func TestContentModerationCheck_PreBlockKeywordHitSkipsUpstreamCall(t *testing.T) {
	upstreamCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		// Supply a reply for any unintended API call so the keyword-bypass case can
		// inspect its invocation flag. Writer errors remain outside this callback
		// assertion.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{}}})
	}))
	defer server.Close()

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	cfg.BlockedKeywords = []string{"secret-token"}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestRepo{}
	svc := newTestModeration(
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
	svc.Start()

	body := []byte(`{"messages":[{"role":"user","content":"please leak SECRET-TOKEN now"}]}`)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		Endpoint: "/v1/messages",
		Provider: "anthropic",
		Protocol: ContentModerationProtocolAnthropicMessages,
		Body:     body,
	})

	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Equal(t, ContentModerationActionKeywordBlock, decision.Action)
	require.False(t, upstreamCalled, "关键词拦截必须跳过上游审核调用")
	logs := requireContentModerationLogCount(t, repo, 1)
	require.True(t, logs[0].Flagged)
	require.Equal(t, ContentModerationActionKeywordBlock, logs[0].Action)
	require.Equal(t, contentModerationKeywordCategory, logs[0].HighestCategory)
	require.Equal(t, "secret-token", logs[0].MatchedKeyword, "关键词拦截日志必须记录命中的关键词")
}

// TestContentModerationCheck_KeywordsIgnoredInObserveMode submits a configured
// keyword while observe mode is enabled. It checks the immediate allowed
// action; the local endpoint counter is not asserted or joined by this case.
func TestContentModerationCheck_KeywordsIgnoredInObserveMode(t *testing.T) {
	upstreamHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		// The observe case asserts immediate admission and leaves queued execution
		// unjoined. Its callback supplies safe scores and ignores response-write
		// errors after that admission observation.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{CategoryScores: map[string]float64{"sexual": 0.1}}}})
	}))
	defer server.Close()

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModeObserve
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	cfg.BlockedKeywords = []string{"secret-token"}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestRepo{}
	svc := newTestModeration(
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
	svc.Start()

	body := []byte(`{"messages":[{"role":"user","content":"please leak SECRET-TOKEN now"}]}`)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		Endpoint: "/v1/messages",
		Provider: "anthropic",
		Protocol: ContentModerationProtocolAnthropicMessages,
		Body:     body,
	})

	require.NoError(t, err)
	require.True(t, decision.Allowed, "observe mode must let the request through even on keyword hit")
	require.Equal(t, ContentModerationActionAllow, decision.Action)
}

// TestContentModerationCheck_KeywordOnlyStrategySkipsAPIOnMiss submits clean
// text under pre-block keyword-only policy with a potentially flagging
// endpoint. It checks allowed admission, no API request, and no fixture log for
// that keyword miss.
func TestContentModerationCheck_KeywordOnlyStrategySkipsAPIOnMiss(t *testing.T) {
	upstreamCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		// Provide a potentially flagging reply for any unintended audit call. This
		// keyword-only miss case checks the invocation flag and ignores
		// response-write errors from that fixture.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{CategoryScores: map[string]float64{"sexual": 0.99}}}})
	}))
	defer server.Close()

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	cfg.BlockedKeywords = []string{"never-matches"}
	cfg.KeywordBlockingMode = ContentModerationKeywordModeKeywordOnly
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestRepo{}
	svc := newTestModeration(
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
	svc.Start()

	body := []byte(`{"messages":[{"role":"user","content":"absolutely clean prompt"}]}`)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		Endpoint: "/v1/messages",
		Provider: "anthropic",
		Protocol: ContentModerationProtocolAnthropicMessages,
		Body:     body,
	})

	require.NoError(t, err)
	require.True(t, decision.Allowed, "keyword-only must allow misses without calling the API")
	require.False(t, upstreamCalled, "keyword-only must not call the upstream moderation API")
	require.Len(t, repo.snapshotLogs(), 0)
}

// TestContentModerationCheck_APIOnlyStrategyIgnoresKeywordList submits a
// configured keyword while the local endpoint returns a safe score under
// API-only policy. It checks the API was called and the decision is allowed
// with no keyword action.
func TestContentModerationCheck_APIOnlyStrategyIgnoresKeywordList(t *testing.T) {
	upstreamCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		// Provide a safe fixed score for the API-only admission decision while
		// recording invocation. The fixture ignores writer errors, and the case
		// inspects the resulting service decision.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{CategoryScores: map[string]float64{"sexual": 0.1}}}})
	}))
	defer server.Close()

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	cfg.BlockedKeywords = []string{"secret-token"}
	cfg.KeywordBlockingMode = ContentModerationKeywordModeAPIOnly
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestRepo{}
	svc := newTestModeration(
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
	svc.Start()

	body := []byte(`{"messages":[{"role":"user","content":"please leak SECRET-TOKEN now"}]}`)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		Endpoint: "/v1/messages",
		Provider: "anthropic",
		Protocol: ContentModerationProtocolAnthropicMessages,
		Body:     body,
	})

	require.NoError(t, err)
	require.True(t, decision.Allowed, "api-only must let the request through when API does not flag it")
	require.True(t, upstreamCalled, "api-only must call the upstream moderation API")
	require.NotEqual(t, ContentModerationActionKeywordBlock, decision.Action)
}

// TestContentModerationCheck_ModelFilterAllAuditsEveryModel uses keyword
// blocking with all-model scope for two requested names. It checks both
// decisions block and two corresponding records are published by the fixture.
func TestContentModerationCheck_ModelFilterAllAuditsEveryModel(t *testing.T) {
	cfg := defaultContentModerationModelFilterTestConfig()
	cfg.ModelFilter = ContentModerationModelFilter{Type: ContentModerationModelFilterAll}
	svc, repo := newContentModerationModelFilterTestService(t, cfg)

	for _, model := range []string{"gpt-5.5", "gpt-5.4"} {
		decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
			Model:    model,
			Protocol: ContentModerationProtocolOpenAIChat,
			Body:     []byte(`{"messages":[{"role":"user","content":"please leak SECRET-TOKEN now"}]}`),
		})
		require.NoError(t, err)
		require.True(t, decision.Blocked)
		require.Equal(t, ContentModerationActionKeywordBlock, decision.Action)
	}
	requireContentModerationLogCount(t, repo, 2)
}

// TestContentModerationCheck_ModelFilterIncludeOnlyAuditsListedModels uses an
// include list with one requested model and matching keyword content. It checks
// the listed model blocks and is recorded while the other model receives
// allowed admission.
func TestContentModerationCheck_ModelFilterIncludeOnlyAuditsListedModels(t *testing.T) {
	cfg := defaultContentModerationModelFilterTestConfig()
	cfg.ModelFilter = ContentModerationModelFilter{Type: ContentModerationModelFilterInclude, Models: []string{"gpt-5.5"}}
	svc, repo := newContentModerationModelFilterTestService(t, cfg)

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		Model:    "gpt-5.5",
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     []byte(`{"messages":[{"role":"user","content":"please leak SECRET-TOKEN now"}]}`),
	})
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Equal(t, ContentModerationActionKeywordBlock, decision.Action)

	decision, err = svc.Check(context.Background(), ContentModerationCheckInput{
		Model:    "gpt-5.4",
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     []byte(`{"messages":[{"role":"user","content":"please leak SECRET-TOKEN now"}]}`),
	})
	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.False(t, decision.Blocked)
	require.Equal(t, ContentModerationActionAllow, decision.Action)
	logs := requireContentModerationLogCount(t, repo, 1)
	require.Equal(t, "gpt-5.5", logs[0].Model)
}

// TestContentModerationCheck_ModelFilterExcludeSkipsListedModels uses an
// exclude list with one requested model and matching keyword content. It checks
// the remaining model blocks and is recorded while the excluded model receives
// allowed admission.
func TestContentModerationCheck_ModelFilterExcludeSkipsListedModels(t *testing.T) {
	cfg := defaultContentModerationModelFilterTestConfig()
	cfg.ModelFilter = ContentModerationModelFilter{Type: ContentModerationModelFilterExclude, Models: []string{"gpt-5.4"}}
	svc, repo := newContentModerationModelFilterTestService(t, cfg)

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		Model:    "gpt-5.5",
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     []byte(`{"messages":[{"role":"user","content":"please leak SECRET-TOKEN now"}]}`),
	})
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Equal(t, ContentModerationActionKeywordBlock, decision.Action)

	decision, err = svc.Check(context.Background(), ContentModerationCheckInput{
		Model:    "gpt-5.4",
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     []byte(`{"messages":[{"role":"user","content":"please leak SECRET-TOKEN now"}]}`),
	})
	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.False(t, decision.Blocked)
	require.Equal(t, ContentModerationActionAllow, decision.Action)
	logs := requireContentModerationLogCount(t, repo, 1)
	require.Equal(t, "gpt-5.5", logs[0].Model)
}

// TestContentModerationCheck_ModelFilterUsesRequestedModelNotBodyModel supplies
// a selected requested model and a different body model under include scope. It
// checks keyword blocking and the requested-model value retained in the fixture
// log.
func TestContentModerationCheck_ModelFilterUsesRequestedModelNotBodyModel(t *testing.T) {
	cfg := defaultContentModerationModelFilterTestConfig()
	cfg.ModelFilter = ContentModerationModelFilter{Type: ContentModerationModelFilterInclude, Models: []string{"gpt-5.5"}}
	svc, repo := newContentModerationModelFilterTestService(t, cfg)

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		Model:    "gpt-5.5",
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     []byte(`{"model":"mapped-upstream-model","messages":[{"role":"user","content":"please leak SECRET-TOKEN now"}]}`),
	})

	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Equal(t, ContentModerationActionKeywordBlock, decision.Action)
	logs := requireContentModerationLogCount(t, repo, 1)
	require.Equal(t, "gpt-5.5", logs[0].Model)
}

// TestContentModerationCheck_OpenAIResponsesRecordsNonHitForCodexPayload sends
// developer content plus consecutive user messages through a local Responses
// audit fixture. It checks the combined user text in outgoing input and an
// allowed non-hit record.
func TestContentModerationCheck_OpenAIResponsesRecordsNonHitForCodexPayload(t *testing.T) {
	var moderationRequest moderationAPIRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/moderations", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&moderationRequest))
		// Return the safe score after captured request decoding; the case observes
		// outgoing input and the retained allowed log, while this fixture ignores
		// response-write errors.
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
	cfg.APIKeys = []string{"sk-test"}
	cfg.RecordNonHits = true
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestRepo{}
	svc := newTestModeration(
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
	svc.Start()

	body := []byte(`{
		"model":"gpt-5.5",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"developer instructions should not be audited"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"first user prompt"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"last user prompt"}]}
		]
	}`)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		UserID:   1001,
		Endpoint: "/responses",
		Provider: "openai",
		Model:    "gpt-5.5",
		Protocol: ContentModerationProtocolOpenAIResponses,
		Body:     body,
	})

	require.NoError(t, err)
	require.False(t, decision.Blocked)
	logs := requireContentModerationLogCount(t, repo, 1)
	require.False(t, logs[0].Flagged)
	require.Equal(t, ContentModerationActionAllow, logs[0].Action)
	require.Equal(t, "/responses", logs[0].Endpoint)
	require.Equal(t, "first user prompt\nlast user prompt", logs[0].InputExcerpt)
	require.Equal(t, "first user prompt\nlast user prompt", moderationRequest.Input)
}

// TestContentModerationCheck_PreBlockBlocksCodexResponsesLatestUserInput sends
// consecutive user entries and developer text while the local audit returns a
// threshold hit. It checks custom block status/message and joined selected user
// text in both outgoing input and retained summary.
func TestContentModerationCheck_PreBlockBlocksCodexResponsesLatestUserInput(t *testing.T) {
	var moderationRequest moderationAPIRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/moderations", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&moderationRequest))
		// Return the threshold hit for the captured Responses input; decision and
		// retained-text assertions consume that result, while this fixture ignores
		// response-write errors.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{
			Results: []moderationAPIResult{{
				CategoryScores: map[string]float64{"sexual": 0.9},
			}},
		})
	}))
	defer server.Close()

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	cfg.BlockStatus = http.StatusUnavailableForLegalReasons
	cfg.BlockMessage = "内容审计测试阻断"
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestRepo{}
	svc := newTestModeration(
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
	svc.Start()

	body := []byte(`{
		"model":"gpt-5.5",
		"instructions":"instructions.....",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"developer instructions should not be audited"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"environment context"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"latest blocked prompt"}]}
		]
	}`)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		UserID:   1001,
		Endpoint: "/responses",
		Provider: "openai",
		Model:    "gpt-5.5",
		Protocol: ContentModerationProtocolOpenAIResponses,
		Body:     body,
	})

	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Equal(t, ContentModerationActionBlock, decision.Action)
	require.Equal(t, http.StatusUnavailableForLegalReasons, decision.StatusCode)
	require.Equal(t, "内容审计测试阻断", decision.Message)
	logs := requireContentModerationLogCount(t, repo, 1)
	require.True(t, logs[0].Flagged)
	require.Equal(t, ContentModerationActionBlock, logs[0].Action)
	require.Equal(t, ContentModerationModePreBlock, logs[0].Mode)
	require.Equal(t, "environment context\nlatest blocked prompt", logs[0].InputExcerpt)
	require.Equal(t, "environment context\nlatest blocked prompt", moderationRequest.Input)
}

// TestContentModerationCheck_PreHashUsesRedisHashCache prepopulates the
// mutex-backed hash fixture and submits the same text identity. It checks
// cached-hash blocking and message/hash fields, one membership read, and a
// stored hash-block log with no ordinary ban effect.
func TestContentModerationCheck_PreHashUsesRedisHashCache(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.PreHashCheckEnabled = true
	cfg.APIKeys = []string{"sk-test"}
	cfg.BlockStatus = http.StatusConflict
	cfg.BlockMessage = "命中历史风险输入"
	cfg.AutoBanEnabled = true
	cfg.BanThreshold = 1
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	hashCache := &contentModerationTestHashCache{hashes: map[string]struct{}{}}
	content := ContentModerationInput{Text: "blocked prompt"}
	content.Normalize()
	hashCache.hashes[content.Hash()] = struct{}{}

	repo := &contentModerationTestRepo{}
	userRepo := &contentModerationTestUserRepo{user: &User{ID: 1001, Status: StatusActive}}
	svc := newTestModeration(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		repo,
		hashCache,
		nil,
		userRepo,
		nil,
		nil,
	)
	svc.Start()

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		UserID:   1001,
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     []byte(`{"messages":[{"role":"user","content":"blocked prompt"}]}`),
	})
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Equal(t, ContentModerationActionHashBlock, decision.Action)
	require.Equal(t, http.StatusConflict, decision.StatusCode)
	require.Equal(t, content.Hash(), decision.InputHash)
	require.Contains(t, decision.Message, "命中历史风险输入")
	require.Contains(t, decision.Message, content.Hash())
	require.Len(t, hashCache.snapshotChecked(), 1)
	logs := requireContentModerationLogCount(t, repo, 1)
	require.True(t, logs[0].Flagged)
	require.Equal(t, ContentModerationActionHashBlock, logs[0].Action)
	require.Equal(t, 1.0, logs[0].CategoryScores["hash"])
	require.Equal(t, ContentModerationModePreBlock, logs[0].Mode)
	require.Zero(t, logs[0].ViolationCount)
	require.False(t, logs[0].AutoBanned)
	require.Empty(t, userRepo.updated)
}

// TestContentModerationCheck_HashBlockLogsDoNotIncreaseNextViolationCount
// stores a prior hash-block fixture log and then obtains a new external score
// hit. It checks the subsequent ordinary block has violation count one and the
// two action labels remain distinct.
func TestContentModerationCheck_HashBlockLogsDoNotIncreaseNextViolationCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a fresh ordinary score hit after the preloaded hash-block history.
		// This fixture ignores write errors; the case inspects the later counted log
		// and action.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{
			Results: []moderationAPIResult{{
				CategoryScores: map[string]float64{"sexual": 0.9},
			}},
		})
	}))
	defer server.Close()

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	cfg.AutoBanEnabled = false
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	userID := int64(1001)
	repo := &contentModerationTestRepo{}
	hashLog := &ContentModerationLog{
		UserID:          &userID,
		Action:          ContentModerationActionHashBlock,
		Flagged:         true,
		HighestCategory: "hash",
		HighestScore:    1,
		CreatedAt:       time.Now(),
	}
	require.NoError(t, repo.CreateLog(context.Background(), hashLog))

	svc := newTestModeration(
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
	svc.Start()

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		UserID:   userID,
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     []byte(`{"messages":[{"role":"user","content":"new blocked prompt"}]}`),
	})

	require.NoError(t, err)
	require.True(t, decision.Blocked)
	logs := requireContentModerationLogCount(t, repo, 2)
	require.Equal(t, ContentModerationActionHashBlock, logs[0].Action)
	require.Equal(t, ContentModerationActionBlock, logs[1].Action)
	require.Equal(t, 1, logs[1].ViolationCount)
}

// TestContentModerationAutoBanSkipsAdminProvider captures the default logger
// while an admin actor reaches the ordinary hit threshold. It checks retained
// violation count, no disablement/invalidation, and diagnostic
// actor/role/count/threshold attributes before logger restoration.
func TestContentModerationAutoBanSkipsAdminProvider(t *testing.T) {
	var slogOutput bytes.Buffer
	// This serial case replaces the package-wide logger for diagnostic assertions;
	// registered cleanup restores the earlier logger after the captured metadata is inspected.
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&slogOutput, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
	})

	cfg := defaultContentModerationConfig()
	cfg.BanThreshold = 2
	cfg.ViolationWindowHours = 24

	userID := int64(1001)
	repo := &contentModerationTestRepo{}
	require.NoError(t, repo.CreateLog(context.Background(), newContentModerationFlaggedLog(userID)))
	userRepo := &contentModerationTestUserRepo{user: &User{ID: userID, Role: RoleAdmin, Status: StatusActive}}
	invalidator := &contentModerationTestAuthCacheInvalidator{}
	svc := newTestModeration(nil, repo, nil, nil, userRepo, invalidator, nil)
	svc.Start()

	svc.persistContentModerationLog(context.Background(), cfg, newContentModerationFlaggedLog(userID), "", false, true)

	logs := requireContentModerationLogCount(t, repo, 2)
	require.Equal(t, 2, logs[1].ViolationCount)
	require.False(t, logs[1].AutoBanned)
	require.Equal(t, StatusActive, userRepo.user.Status)
	require.Empty(t, userRepo.updated)
	require.Empty(t, invalidator.userIDs)
	require.Contains(t, slogOutput.String(), "content_moderation.autoban_skipped_admin")
	require.Contains(t, slogOutput.String(), "user_id=1001")
	require.Contains(t, slogOutput.String(), "role=admin")
	require.Contains(t, slogOutput.String(), "count=2")
	require.Contains(t, slogOutput.String(), "threshold=2")
}

// TestContentModerationAutoBanDisablesRegularUserAtThreshold adds a prior
// timestamped hit and supplies a regular actor at the next threshold. It checks
// counted violations, a newly disabled fixture user, and authentication
// invalidation for that actor.
func TestContentModerationAutoBanDisablesRegularUserAtThreshold(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.BanThreshold = 2
	cfg.ViolationWindowHours = 24

	userID := int64(1001)
	repo := &contentModerationTestRepo{}
	require.NoError(t, repo.CreateLog(context.Background(), newContentModerationFlaggedLog(userID)))
	userRepo := &contentModerationTestUserRepo{user: &User{ID: userID, Role: RoleUser, Status: StatusActive}}
	invalidator := &contentModerationTestAuthCacheInvalidator{}
	svc := newTestModeration(nil, repo, nil, nil, userRepo, invalidator, nil)
	svc.Start()

	svc.persistContentModerationLog(context.Background(), cfg, newContentModerationFlaggedLog(userID), "", false, true)

	logs := requireContentModerationLogCount(t, repo, 2)
	require.Equal(t, 2, logs[1].ViolationCount)
	require.True(t, logs[1].AutoBanned)
	require.Len(t, userRepo.updated, 1)
	require.Equal(t, StatusDisabled, userRepo.user.Status)
	require.Equal(t, []int64{userID}, invalidator.userIDs)
}

// TestContentModerationTeamAttributionAutoBanDisablesActorOnly supplies
// separate team member, paying owner, and team identifiers for one ordinary
// hit. It checks stored attribution and that lookup, status update, and
// invalidation target the member.
func TestContentModerationTeamAttributionAutoBanDisablesActorOnly(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.BanThreshold = 1
	cfg.ViolationWindowHours = 24

	ownerID := int64(1001)
	memberID := int64(2002)
	teamID := int64(3003)
	repo := &contentModerationTestRepo{}
	userRepo := &contentModerationTestUserRepo{user: &User{ID: memberID, Role: RoleUser, Status: StatusActive}}
	invalidator := &contentModerationTestAuthCacheInvalidator{}
	svc := newTestModeration(nil, repo, nil, nil, userRepo, invalidator, nil)
	svc.Start()
	log := newContentModerationFlaggedLog(memberID)
	log.BillingUserID = &ownerID
	log.TeamID = &teamID

	svc.persistContentModerationLog(context.Background(), cfg, log, "", false, true)

	logs := requireContentModerationLogCount(t, repo, 1)
	require.Equal(t, memberID, *logs[0].UserID)
	require.Equal(t, ownerID, *logs[0].BillingUserID)
	require.Equal(t, teamID, *logs[0].TeamID)
	require.Equal(t, []int64{memberID}, userRepo.requestedIDs)
	require.Len(t, userRepo.updated, 1)
	require.Equal(t, memberID, userRepo.updated[0].ID)
	require.Equal(t, StatusDisabled, userRepo.updated[0].Status)
	require.NotEqual(t, ownerID, userRepo.updated[0].ID)
	require.Equal(t, []int64{memberID}, invalidator.userIDs)
}

// TestContentModerationAdminBelowBanThresholdRecordsViolationOnly supplies an
// admin actor with the first hit below threshold. It checks one recorded
// violation and active fixture status with no user update or invalidation.
func TestContentModerationAdminBelowBanThresholdRecordsViolationOnly(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.BanThreshold = 2
	cfg.ViolationWindowHours = 24

	userID := int64(1001)
	repo := &contentModerationTestRepo{}
	userRepo := &contentModerationTestUserRepo{user: &User{ID: userID, Role: RoleAdmin, Status: StatusActive}}
	invalidator := &contentModerationTestAuthCacheInvalidator{}
	svc := newTestModeration(nil, repo, nil, nil, userRepo, invalidator, nil)
	svc.Start()

	svc.persistContentModerationLog(context.Background(), cfg, newContentModerationFlaggedLog(userID), "", false, true)

	logs := requireContentModerationLogCount(t, repo, 1)
	require.Equal(t, 1, logs[0].ViolationCount)
	require.False(t, logs[0].AutoBanned)
	require.Equal(t, StatusActive, userRepo.user.Status)
	require.Empty(t, userRepo.updated)
	require.Empty(t, invalidator.userIDs)
}

// TestContentModerationCheck_PreBlockFlaggedWritesRedisHashCache submits a
// threshold-hitting input and repeats it after hash publication in the
// mutex-backed fixture. It checks the first API block becomes a later hash
// block, with one endpoint call and distinct stored actions.
func TestContentModerationCheck_PreBlockFlaggedWritesRedisHashCache(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		// Return a hit for initial hash recording before the repeated cache check.
		// Writer errors are ignored here, while one API call and distinct recorded
		// actions are asserted.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{
			Results: []moderationAPIResult{{
				CategoryScores: map[string]float64{"sexual": 0.9},
			}},
		})
	}))
	defer server.Close()

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.PreHashCheckEnabled = true
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	cfg.BlockStatus = http.StatusConflict
	cfg.BlockMessage = "命中风险输入"
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestRepo{}
	hashCache := &contentModerationTestHashCache{}
	svc := newTestModeration(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		repo,
		hashCache,
		nil,
		nil,
		nil,
		nil,
	)
	svc.Start()

	body := []byte(`{"messages":[{"role":"user","content":"repeat blocked prompt"}]}`)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     body,
	})
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Equal(t, ContentModerationActionBlock, decision.Action)
	require.Equal(t, 1, requestCount)
	recorded := requireRecordedHashCount(t, hashCache, 1)
	requireContentModerationLogCount(t, repo, 1)

	decision, err = svc.Check(context.Background(), ContentModerationCheckInput{
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     body,
	})
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Equal(t, ContentModerationActionHashBlock, decision.Action)
	require.Equal(t, recorded[0], decision.InputHash)
	require.Equal(t, 1, requestCount)
	logs := requireContentModerationLogCount(t, repo, 2)
	require.Equal(t, ContentModerationActionBlock, logs[0].Action)
	require.Equal(t, ContentModerationActionHashBlock, logs[1].Action)
}

// TestContentModerationDeleteFlaggedInputHash_NormalizesAndDeletes deletes an
// uppercase version of a stored hexadecimal member in the hash fixture. It
// checks normalized identity, successful first removal, absent membership,
// recorded delete arguments, and false repeated removal.
func TestContentModerationDeleteFlaggedInputHash_NormalizesAndDeletes(t *testing.T) {
	existingHash := strings.Repeat("a", 64)
	hashCache := &contentModerationTestHashCache{hashes: map[string]struct{}{
		existingHash: {},
	}}
	svc := &ContentModerationService{runtime: testModerationRuntime(), hashCache: hashCache}

	result, err := svc.DeleteFlaggedInputHash(context.Background(), strings.ToUpper(existingHash))

	require.NoError(t, err)
	require.Equal(t, existingHash, result.InputHash)
	require.True(t, result.Deleted)
	require.False(t, hashCache.hasHash(existingHash))
	require.Equal(t, []string{existingHash}, hashCache.snapshotDeleted())

	result, err = svc.DeleteFlaggedInputHash(context.Background(), existingHash)

	require.NoError(t, err)
	require.Equal(t, existingHash, result.InputHash)
	require.False(t, result.Deleted)
}

// TestContentModerationClearFlaggedInputHashesAndStatusCount starts with two
// mutex-backed fixture members and reads status around a clear call. It checks
// count two before clearing, a returned count two, and zero afterward; it does
// not exercise Redis command interleaving.
func TestContentModerationClearFlaggedInputHashesAndStatusCount(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	hashCache := &contentModerationTestHashCache{hashes: map[string]struct{}{
		strings.Repeat("a", 64): {},
		strings.Repeat("b", 64): {},
	}}
	svc := &ContentModerationService{
		runtime: testModerationRuntime(),
		settingRepo: &contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		hashCache: hashCache,
		keyHealth: make(map[string]*contentModerationKeyHealth),
	}

	status, err := svc.GetStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), status.FlaggedHashCount)

	result, err := svc.ClearFlaggedInputHashes(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), result.Deleted)

	status, err = svc.GetStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), status.FlaggedHashCount)
}

// TestContentModerationCheck_AsyncFlaggedWritesRedisHashCache calls checkSync
// directly with an observe policy and a queue-delay value. It checks no block
// plus a recorded fixture hash and log, without scheduling the ordinary observe
// queue path.
func TestContentModerationCheck_AsyncFlaggedWritesRedisHashCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Supply the hit consumed by direct checkSync with queue-delay metadata. This
		// fixture ignores write errors, and the case checks hash/log publication from
		// that call.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{
			Results: []moderationAPIResult{{
				CategoryScores: map[string]float64{"sexual": 0.9},
			}},
		})
	}))
	defer server.Close()

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModeObserve
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestRepo{}
	hashCache := &contentModerationTestHashCache{}
	svc := newTestModeration(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		repo,
		hashCache,
		nil,
		nil,
		nil,
		nil,
	)
	svc.Start()

	decision := svc.checkSync(context.Background(), ContentModerationCheckInput{
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     []byte(`{"messages":[{"role":"user","content":"bad prompt"}]}`),
	}, cfg, ContentModerationInput{Text: "bad prompt"}, strings.Repeat("b", 64), contentModerationIntPtr(25), false)

	require.False(t, decision.Blocked)
	requireRecordedHashCount(t, hashCache, 1)
	requireContentModerationLogCount(t, repo, 1)
}

// TestContentModerationUnbanUser_ActivatesUserAndInvalidatesAuthCache supplies
// a disabled fixture user to administrator reactivation. It checks active
// result/status, one user update, and authentication invalidation for the
// selected actor ID.
func TestContentModerationUnbanUser_ActivatesUserAndInvalidatesAuthCache(t *testing.T) {
	userRepo := &contentModerationTestUserRepo{user: &User{ID: 1001, Email: "user@example.com", Status: StatusDisabled}}
	invalidator := &contentModerationTestAuthCacheInvalidator{}
	repo := &contentModerationTestRepo{}
	svc := newTestModeration(nil, repo, nil, nil, userRepo, invalidator, nil)
	svc.Start()

	result, err := svc.UnbanUser(context.Background(), 1001)

	require.NoError(t, err)
	require.Equal(t, int64(1001), result.UserID)
	require.Equal(t, StatusActive, result.Status)
	require.Len(t, userRepo.updated, 1)
	require.Equal(t, StatusActive, userRepo.updated[0].Status)
	require.Equal(t, []int64{1001}, invalidator.userIDs)
}

// TestContentModerationUnbanUser_ActiveUserOnlyInvalidatesAuthCache supplies an
// already-active fixture user to administrator reactivation. It checks active
// result and authentication invalidation with an empty status-update
// observation list.
func TestContentModerationUnbanUser_ActiveUserOnlyInvalidatesAuthCache(t *testing.T) {
	userRepo := &contentModerationTestUserRepo{user: &User{ID: 1001, Email: "user@example.com", Status: StatusActive}}
	invalidator := &contentModerationTestAuthCacheInvalidator{}
	repo := &contentModerationTestRepo{}
	svc := newTestModeration(nil, repo, nil, nil, userRepo, invalidator, nil)
	svc.Start()

	result, err := svc.UnbanUser(context.Background(), 1001)

	require.NoError(t, err)
	require.Equal(t, StatusActive, result.Status)
	require.Empty(t, userRepo.updated)
	require.Equal(t, []int64{1001}, invalidator.userIDs)
}

// TestContentModerationNoMediaRetention submits flagged image content under
// observe policy with sensitive retention enabled. It checks empty
// excerpt/items/media and retained score/action observations plus hash-cache
// recording after asynchronous publication.
func TestContentModerationNoMediaRetention(t *testing.T) {
	svc, repo, hashCache, imageDataURL := newCreativeNoMediaRetentionModerationService(t)

	body := []byte(`{"model":"gpt-image-2","prompt":"sexy picture please","images":[{"image_url":"` + imageDataURL + `"}]}`)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		RequestID:        "creative_mod_test_noretention",
		UserID:           7,
		BillingUserID:    7,
		Endpoint:         "/v1/creative/runs",
		Provider:         "openai",
		Protocol:         ContentModerationProtocolOpenAIImages,
		Body:             body,
		NoMediaRetention: true,
	})
	require.NoError(t, err)
	require.True(t, decision.Allowed, "observe 模式下命中只记录不阻断")

	logs := requireContentModerationLogCount(t, repo, 1)
	log := logs[0]
	require.True(t, log.Flagged)
	require.Equal(t, "sexual", log.HighestCategory)
	// Content and media fields stay empty, including media snapshot error records.
	require.Empty(t, log.InputExcerpt)
	require.Empty(t, log.InputItems)
	require.Empty(t, log.Media, "无媒体留存模式不得保存媒体快照")
	// The hash fixture observes recorded risk identity after review content is
	// stripped; score and action fields remain available for administrator review.
	require.NotEmpty(t, hashCache.snapshotRecorded())
	require.NotEmpty(t, log.CategoryScores)
	require.NotEmpty(t, log.Action)
}

// TestContentModerationMediaRetentionControl submits the same kind of flagged
// observe content with ordinary retention enabled. It checks retained
// summary/items and exactly one ready snapshot containing nonempty image bytes.
func TestContentModerationMediaRetentionControl(t *testing.T) {
	svc, repo, _, imageDataURL := newCreativeNoMediaRetentionModerationService(t)

	body := []byte(`{"model":"gpt-image-2","prompt":"sexy picture please","images":[{"image_url":"` + imageDataURL + `"}]}`)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		RequestID:     "creative_mod_test_control",
		UserID:        7,
		BillingUserID: 7,
		Endpoint:      "/v1/creative/runs",
		Provider:      "openai",
		Protocol:      ContentModerationProtocolOpenAIImages,
		Body:          body,
	})
	require.NoError(t, err)
	require.True(t, decision.Allowed)

	logs := requireContentModerationLogCount(t, repo, 1)
	log := logs[0]
	require.NotEmpty(t, log.InputExcerpt, "对照组必须保留正文摘录")
	require.NotEmpty(t, log.InputItems, "对照组必须保留输入项")
	require.Len(t, log.Media, 1, "对照组必须对命中媒体做快照")
	require.Equal(t, "ready", log.Media[0].SnapshotStatus)
	require.NotEmpty(t, log.Media[0].Content, "对照组快照必须包含图片字节")
}
