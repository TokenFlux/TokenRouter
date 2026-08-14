package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

// TestIsOpenAIWSTokenEvent_TerminalEventsExcluded 覆盖 isOpenAIWSTokenEvent 的回归用例。
// 重点验证终止事件（response.completed / response.done）不再被当作 token event，
// 否则当上游没有可识别的 delta 时，firstTokenMs 会被填到终止时刻，
// 等于把"总耗时"误报为"首 token 延迟"（issue #2651）。
func TestIsOpenAIWSTokenEvent_TerminalEventsExcluded(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		want      bool
	}{
		{name: "empty", eventType: "", want: false},
		{name: "whitespace_trimmed_empty", eventType: "   ", want: false},

		{name: "response.created", eventType: "response.created", want: false},
		{name: "response.in_progress", eventType: "response.in_progress", want: false},
		{name: "response.output_item.added", eventType: "response.output_item.added", want: false},
		{name: "response.output_item.done", eventType: "response.output_item.done", want: false},

		{name: "terminal_response.completed", eventType: "response.completed", want: false},
		{name: "terminal_response.done", eventType: "response.done", want: false},
		{name: "terminal_response.completed_padded", eventType: "  response.completed  ", want: false},
		{name: "terminal_response.done_padded", eventType: "  response.done  ", want: false},

		{name: "delta_text", eventType: "response.output_text.delta", want: true},
		{name: "delta_audio_transcript", eventType: "response.audio_transcript.delta", want: true},
		{name: "delta_function_call_arguments", eventType: "response.function_call_arguments.delta", want: true},

		{name: "output_text_done", eventType: "response.output_text.done", want: true},
		{name: "output_text_annotation_added", eventType: "response.output_text.annotation.added", want: true},

		{name: "output_audio_done", eventType: "response.output_audio.done", want: true},

		{name: "reasoning_summary_delta", eventType: "response.reasoning_summary_text.delta", want: true},

		{name: "unrelated_event_error", eventType: "error", want: false},
		{name: "unknown_event_without_match", eventType: "response.reasoning_summary_part.added", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := isOpenAIWSTokenEvent(tc.eventType)
			require.Equal(t, tc.want, got, "isOpenAIWSTokenEvent(%q)", tc.eventType)
		})
	}
}

func TestOpenAIForwardResultSucceededForScheduling_TerminalEvents(t *testing.T) {
	tests := []struct {
		name     string
		result   *OpenAIForwardResult
		expected bool
	}{
		{name: "nil legacy result", result: nil, expected: true},
		{name: "non websocket zero value", result: &OpenAIForwardResult{}, expected: true},
		{name: "websocket legacy empty terminal", result: &OpenAIForwardResult{OpenAIWSMode: true}, expected: true},
		{name: "completed", result: &OpenAIForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.completed"}, expected: true},
		{name: "done", result: &OpenAIForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.done"}, expected: true},
		{name: "failed", result: &OpenAIForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.failed"}, expected: false},
		{name: "incomplete", result: &OpenAIForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.incomplete"}, expected: false},
		{name: "cancelled", result: &OpenAIForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.cancelled"}, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.result.SucceededForScheduling())
		})
	}
}

func TestOpenAIWSTerminalEvent_ResponseFailedRecordsModelTransient(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 5201, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"server_error","message":"Internal error"}}}`)

	for range 2 {
		terminalPolicy := svc.handleOpenAIWSTerminalTransientFailure(context.Background(), account, "gpt-5.5", http.Header{}, payload)
		require.Equal(t, "response.failed", terminalPolicy.TerminalEvent)
		require.Equal(t, http.StatusBadGateway, terminalPolicy.StatusCode)
	}

	require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.5"))
}

// TestOpenAIWSTerminalFailureReturnsExplicitPolicyDecision 验证 response.failed
// 不只写账号状态，还把显式策略结果返回给写客户端事件的调用方。
func TestOpenAIWSTerminalFailureReturnsExplicitPolicyDecision(t *testing.T) {
	svc := &OpenAIGatewayService{}
	repo := &openAIWSPolicyRepo{}
	svc.rateLimitService = NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       5206,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusUnprocessableEntity)},
		},
	}
	payload := []byte(`{"type":"response.failed","response":{"error":{"status_code":422,"message":"configured"}}}`)

	terminalPolicy := svc.handleOpenAIWSTerminalTransientFailure(
		context.Background(), account, "gpt-5.5", http.Header{}, payload,
	)

	require.Equal(t, "response.failed", terminalPolicy.TerminalEvent)
	require.Equal(t, http.StatusUnprocessableEntity, terminalPolicy.StatusCode)
	require.Equal(t, ErrorPolicyCustomMatched, terminalPolicy.Decision.Policy)
	require.True(t, terminalPolicy.Decision.ShouldFailover(account, terminalPolicy.StatusCode, false))
	require.Equal(t, 1, repo.setErrorCalls)
}

// TestOpenAIWSTerminalContentPolicyBypassesAccountPolicy 验证内容安全拒绝
// 即使被语义映射为 502，也不会误命中账号自定义错误码。
func TestOpenAIWSTerminalContentPolicyBypassesAccountPolicy(t *testing.T) {
	svc := &OpenAIGatewayService{}
	repo := &openAIWSPolicyRepo{}
	svc.rateLimitService = NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       5207,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusBadGateway)},
		},
	}
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"content_policy","message":"request blocked by policy"}}}`)

	terminalPolicy := svc.handleOpenAIWSTerminalTransientFailure(
		context.Background(), account, "gpt-5.5", http.Header{}, payload,
	)

	require.Equal(t, http.StatusBadGateway, terminalPolicy.StatusCode)
	require.Equal(t, ErrorPolicyNone, terminalPolicy.Decision.Policy)
	require.False(t, terminalPolicy.Decision.ShouldFailover(
		account, terminalPolicy.StatusCode, openAIStreamFailedEventShouldFailover(payload, "request blocked by policy"),
	))
	require.Zero(t, repo.setErrorCalls)
}

func TestOpenAIWSErrorEvent_ServerErrorRecordsModelTransient(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 5203, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	payload := []byte(`{"type":"error","error":{"code":"server_error","type":"server_error","message":"Internal error"}}`)

	for range 2 {
		svc.handleOpenAIWSErrorEventTransientFailure(context.Background(), account, "gpt-5.5", http.Header{}, payload)
	}

	require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.5"))
}

func TestOpenAIWSPayloadTransientStatus_Explicit529IsNotModelTransient(t *testing.T) {
	payload := []byte(`{"type":"response.failed","response":{"error":{"status_code":529,"code":"server_error","message":"overloaded"}}}`)

	require.Zero(t, openAIWSPayloadTransientStatus(payload))
}

func TestOpenAIWSErrorPolicyStatus_PreservesExplicitStatusAndFallbackMapping(t *testing.T) {
	require.Equal(t, 529, openAIWSErrorPolicyStatus([]byte(`{"type":"response.failed","response":{"error":{"status_code":529,"code":"server_error"}}}`)))
	require.Equal(t, http.StatusBadGateway, openAIWSErrorPolicyStatus([]byte(`{"type":"error","error":{"type":"server_error"}}`)))
}

func TestOpenAIWSDial5xxRecordsModelTransient(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 5202, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	dialErr := &openAIWSDialError{
		StatusCode:      http.StatusBadGateway,
		ResponseHeaders: http.Header{"X-Request-Id": []string{"req-ws-502"}},
		ResponseBody:    []byte(`{"error":{"message":"bad gateway"}}`),
	}

	for range 2 {
		svc.handleOpenAIWSDialTransientFailure(context.Background(), account, "gpt-5.5", dialErr)
	}

	require.Eventually(t, func() bool {
		return svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.5")
	}, time.Second, 10*time.Millisecond)
}

// TestOpenAIWSPoolModeErrorUsesConfiguredRetry 验证原生 WebSocket 错误也使用
// 池模式统一决策，不再写入默认模型瞬态冷却。
func TestOpenAIWSPoolModeErrorUsesConfiguredRetry(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       5204,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(http.StatusBadGateway)},
		},
	}

	decision := svc.applyOpenAIWSEventErrorPolicy(
		context.Background(), account, "gpt-5.5", http.StatusBadGateway, http.Header{}, []byte(`{"error":{"message":"bad gateway"}}`),
	)

	require.Equal(t, ErrorPolicyPoolBypassed, decision.Policy)
	require.True(t, decision.RetryableOnSameAccount(account, http.StatusBadGateway))
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.5"))
}

// openAIWSPolicyRepo 记录 WebSocket 显式策略触发的账号错误写入。
type openAIWSPolicyRepo struct {
	transientCooldownAccountRepo
	setErrorCalls int
}

func (r *openAIWSPolicyRepo) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

// TestOpenAIWSCustomNonFailoverStatusStopsScheduling 验证 WebSocket 派生出的
// 非默认故障转移状态也执行管理员显式策略，并禁止同账号重试。
func TestOpenAIWSCustomNonFailoverStatusStopsScheduling(t *testing.T) {
	svc := &OpenAIGatewayService{}
	repo := &openAIWSPolicyRepo{}
	svc.rateLimitService = NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       5205,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode":                  true,
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusUnprocessableEntity)},
		},
	}

	decision := svc.applyOpenAIWSEventErrorPolicy(
		context.Background(), account, "gpt-5.5", http.StatusUnprocessableEntity, http.Header{}, []byte(`{"error":{"message":"configured"}}`),
	)

	require.Equal(t, ErrorPolicyCustomMatched, decision.Policy)
	require.True(t, decision.StopScheduling)
	require.False(t, decision.RetryableOnSameAccount(account, http.StatusUnprocessableEntity))
	require.Equal(t, 1, repo.setErrorCalls)
}

// TestIsOpenAIWSTokenEvent_DisjointWithTerminal 守护「token 事件集合与终止事件集合互斥」的不变量。
// firstTokenMs 的计算依赖于 isTokenEvent && !isTerminalEvent；
// 若两者再次出现交集，则 issue #2651 描述的 latency 误报会重现。
func TestIsOpenAIWSTokenEvent_DisjointWithTerminal(t *testing.T) {
	terminalEvents := []string{
		"response.completed",
		"response.done",
		"response.failed",
		"response.incomplete",
		"response.cancelled",
		"response.canceled",
	}
	for _, ev := range terminalEvents {
		ev := ev
		t.Run(ev, func(t *testing.T) {
			require.True(t, isOpenAIWSTerminalEvent(ev), "expected terminal event %q to be classified as terminal", ev)
			require.False(t, isOpenAIWSTokenEvent(ev), "terminal event %q must NOT be classified as token event (issue #2651)", ev)
		})
	}
}
