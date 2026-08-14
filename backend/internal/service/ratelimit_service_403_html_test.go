//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

// countingOpenAI403CounterCache 记录连续 403 计数器是否被调用。
type countingOpenAI403CounterCache struct {
	openAI403CounterCacheStub
	increments int
}

func (s *countingOpenAI403CounterCache) IncrementOpenAI403Count(ctx context.Context, accountID int64, window int) (int64, error) {
	s.increments++
	return s.openAI403CounterCacheStub.IncrementOpenAI403Count(ctx, accountID, window)
}

type openAI403TestHarness struct {
	svc     *RateLimitService
	repo    *rateLimitAccountRepoStub
	counter *countingOpenAI403CounterCache
	blocker *runtimeBlockRecorder
	account *Account
}

func newOpenAI403TestHarness(t *testing.T, accountID int64, counts ...int64) *openAI403TestHarness {
	t.Helper()
	repo := &rateLimitAccountRepoStub{}
	counter := &countingOpenAI403CounterCache{openAI403CounterCacheStub: openAI403CounterCacheStub{counts: counts}}
	blocker := &runtimeBlockRecorder{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc.SetOpenAI403CounterCache(counter)
	svc.SetAccountRuntimeBlocker(blocker)
	return &openAI403TestHarness{
		svc:     svc,
		repo:    repo,
		counter: counter,
		blocker: blocker,
		account: &Account{ID: accountID, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
	}
}

func (h *openAI403TestHarness) handle(body string) bool {
	return h.svc.HandleUpstreamError(
		context.Background(), h.account, http.StatusForbidden, http.Header{}, []byte(body),
	)
}

func (h *openAI403TestHarness) requireNoAccountPenalty(t *testing.T) {
	t.Helper()
	require.Equal(t, 0, h.repo.setErrorCalls, "端点级 403 不得永久禁用账号")
	require.Equal(t, 0, h.repo.tempCalls, "端点级 403 不得把账号设为临时不可调度")
	require.Empty(t, h.blocker.accounts, "端点级 403 不得触发调度阻断通知")
	require.Equal(t, 0, h.counter.increments, "端点级 403 不得递增连续 403 计数")
}

// issue #5334 中的无效 Responses 子路径会在到达 OpenAI API 前收到 HTML 403。
const openAI403HTMLBody = "<!DOCTYPE html>\n<html><head><title>403 Forbidden</title></head>" +
	"<body><h1>403 Forbidden</h1></body></html>"

// TestHandleUpstreamError_OpenAIHTML403DoesNotPenalizeAccount 验证常见 HTML 外形均不处罚账号。
func TestHandleUpstreamError_OpenAIHTML403DoesNotPenalizeAccount(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"doctype_prefixed", openAI403HTMLBody},
		{"bare_html_tag", "<html><body>403 Forbidden</body></html>"},
		{"leading_whitespace_and_uppercase", "\n\t  <!DOCTYPE HTML><html><body>Forbidden</body></html>"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newOpenAI403TestHarness(t, 501, 1)

			shouldDisable := h.handle(tc.body)

			require.False(t, shouldDisable, "HTML 403 不得判定账号应下线")
			h.requireNoAccountPenalty(t)
		})
	}
}

// TestHandleUpstreamError_OpenAIHTML403RepeatedNeverEscalates 验证重复错误不会积累到永久禁用阈值。
func TestHandleUpstreamError_OpenAIHTML403RepeatedNeverEscalates(t *testing.T) {
	h := newOpenAI403TestHarness(t, 502, 1, 2, 3, 4, 5)

	for i := 0; i < openAI403DisableThresholdDefault+2; i++ {
		require.False(t, h.handle(openAI403HTMLBody), "第 %d 次 HTML 403 仍不得判定账号应下线", i+1)
	}

	h.requireNoAccountPenalty(t)
}

// TestHandleUpstreamError_OpenAIStructured403StillPenalizes 保护真实账号级 403 的既有处罚链路。
func TestHandleUpstreamError_OpenAIStructured403StillPenalizes(t *testing.T) {
	t.Run("first_hit_temp_unschedulable", func(t *testing.T) {
		h := newOpenAI403TestHarness(t, 503, 1)

		require.True(t, h.handle(`{"error":{"message":"Your account is not authorized"}}`))
		require.Equal(t, 1, h.counter.increments)
		require.Equal(t, 1, h.repo.tempCalls)
		require.Equal(t, 0, h.repo.setErrorCalls)
		require.Contains(t, h.repo.lastTempReason, "Your account is not authorized")
		require.Len(t, h.blocker.accounts, 1)
	})

	t.Run("threshold_disables", func(t *testing.T) {
		h := newOpenAI403TestHarness(t, 504, int64(openAI403DisableThresholdDefault))

		require.True(t, h.handle(`{"error":{"message":"workspace forbidden by policy"}}`))
		require.Equal(t, 1, h.repo.setErrorCalls)
		require.Contains(t, h.repo.lastErrorMsg, "workspace forbidden by policy")
	})

	t.Run("plain_text_body_unchanged", func(t *testing.T) {
		h := newOpenAI403TestHarness(t, 505, 1)

		require.True(t, h.handle("Forbidden"))
		require.Equal(t, 1, h.repo.tempCalls)
	})
}

// TestHandleUpstreamError_HTML403OnOtherPlatformsUnchanged 验证豁免只作用于 OpenAI。
func TestHandleUpstreamError_HTML403OnOtherPlatformsUnchanged(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			repo := &rateLimitAccountRepoStub{}
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			account := &Account{ID: 506, Platform: platform, Type: AccountTypeAPIKey}

			shouldDisable := svc.HandleUpstreamError(
				context.Background(), account, http.StatusForbidden, http.Header{}, []byte(openAI403HTMLBody),
			)

			require.True(t, shouldDisable)
			require.Equal(t, 1, repo.setErrorCalls, "其他平台保持原有 SetError 行为")
		})
	}
}

// TestIsHTMLResponse 固化只识别 HTML 前缀而不扩大到其它文本格式。
func TestIsHTMLResponse(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"doctype_lower", "<!doctype html><html></html>", true},
		{"doctype_upper", "<!DOCTYPE HTML>", true},
		{"bare_html", "<html lang=\"en\">", true},
		{"leading_whitespace", "\n\n   <html>", true},
		{"json_error", `{"error":{"message":"forbidden"}}`, false},
		{"plain_text", "Forbidden", false},
		{"empty", "", false},
		{"xml_declaration", `<?xml version="1.0"?><error/>`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isHTMLResponse([]byte(tc.body)))
		})
	}
}
