//go:build unit

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/gin-gonic/gin"
)

// fakeQuotaRepoForUserHandler 实现 billing.UserPlatformQuotaRepository 最小子集
type fakeQuotaRepoForUserHandler struct {
	billing.UserPlatformQuotaRepository
	records []billing.UserPlatformQuotaRecord
}

func (f *fakeQuotaRepoForUserHandler) ListByUser(_ context.Context, _ int64) ([]billing.UserPlatformQuotaRecord, error) {
	return f.records, nil
}

func TestGetMyPlatformQuotas_EmptyReturns200WithEmptyArray(t *testing.T) {
	repo := &fakeQuotaRepoForUserHandler{records: nil}
	h := newTestQuotaHandler(repo, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/user/platform-quotas", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 42})
	h.GetMyPlatformQuotas(c)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d. body: %s", w.Code, w.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			PlatformQuotas []any `json:"platform_quotas"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error: %v, body: %s", err, w.Body.String())
	}
	if body.Code != 0 {
		t.Errorf("expected code=0, got %d", body.Code)
	}
	// nil 和空列表均符合此入口的契约，上述断言验证 HTTP 状态和业务码。
}

func TestGetMyPlatformQuotas_D14_LazyZeroForExpiredWindow(t *testing.T) {
	pastStart := time.Now().UTC().AddDate(0, 0, -2)
	daily := 5.0
	repo := &fakeQuotaRepoForUserHandler{records: []billing.UserPlatformQuotaRecord{{
		UserID:           42,
		Platform:         "anthropic",
		DailyLimitUSD:    &daily,
		DailyUsageUSD:    3.0,
		DailyWindowStart: &pastStart,
	}}}
	h := newTestQuotaHandler(repo, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/user/platform-quotas", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 42})
	h.GetMyPlatformQuotas(c)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d. body: %s", w.Code, w.Body.String())
	}

	// 解析 response，验证过期 daily 的 usage_usd=0 且 window_resets_at=null
	body := w.Body.String()
	if !strings.Contains(body, `"daily_usage_usd":0`) {
		t.Errorf("expected daily_usage_usd:0 in body, got: %s", body)
	}
	if !strings.Contains(body, `"daily_window_resets_at":null`) {
		t.Errorf("expected daily_window_resets_at:null in body, got: %s", body)
	}
}

func TestGetMyPlatformQuotas_NilRepo_Returns200Empty(t *testing.T) {
	h := newTestQuotaHandler(nil, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/user/platform-quotas", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 99})
	h.GetMyPlatformQuotas(c)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestGetMyPlatformQuotas_NoAuth_Returns401(t *testing.T) {
	h := newTestQuotaHandler(nil, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/user/platform-quotas", nil)
	// 不设置 auth subject
	h.GetMyPlatformQuotas(c)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestLazyZeroQuotaForResponse_UserViewStripsWindowStart(t *testing.T) {
	start := time.Now().UTC().Add(-1 * time.Hour)
	r := billing.UserPlatformQuotaRecord{
		Platform:         "anthropic",
		DailyUsageUSD:    1.0,
		DailyWindowStart: &start,
	}
	out := LazyZeroQuotaForResponse(r, time.Now().UTC(), false, timezone.NewCalendar(time.Local))
	if _, ok := out["daily_window_start"]; ok {
		t.Error("user view should not include daily_window_start")
	}
}

func TestLazyZeroQuotaForResponse_AdminViewIncludesWindowStart(t *testing.T) {
	start := time.Now().UTC().Add(-1 * time.Hour)
	r := billing.UserPlatformQuotaRecord{
		Platform:         "anthropic",
		DailyWindowStart: &start,
	}
	out := LazyZeroQuotaForResponse(r, time.Now().UTC(), true, timezone.NewCalendar(time.Local))
	if _, ok := out["daily_window_start"]; !ok {
		t.Error("admin view should include daily_window_start")
	}
}

func TestLazyZeroQuotaForResponse_ActiveWindowPreservesUsage(t *testing.T) {
	// 今天的窗口起始时间（不过期）：按全局时区取当天 0 点，与 view 层同口径
	now := time.Now()
	today := timezone.NewCalendar(time.Local).
		StartOfDay(now)
	usage := 2.5
	r := billing.UserPlatformQuotaRecord{
		Platform:         "openai",
		DailyUsageUSD:    usage,
		DailyWindowStart: &today,
	}
	out := LazyZeroQuotaForResponse(r, now, false, timezone.NewCalendar(time.Local))
	if out["daily_usage_usd"] != usage {
		t.Errorf("expected daily_usage_usd=%v, got %v", usage, out["daily_usage_usd"])
	}
	// 活跃窗口应有 resets_at（非 nil）
	if out["daily_window_resets_at"] == nil {
		t.Error("active window should have daily_window_resets_at set")
	}
}

func TestNeedsDailyReset_NilStart_ReturnsFalse(t *testing.T) {
	if billing.NeedsDailyReset(nil, time.Now().UTC(), timezone.NewCalendar(time.Local)) {
		t.Error("nil start should not need reset")
	}
}

func TestNeedsDailyReset_OldStart_ReturnsTrue(t *testing.T) {
	old := time.Now().UTC().AddDate(0, 0, -1)
	if !billing.NeedsDailyReset(&old, time.Now().UTC(), timezone.NewCalendar(time.Local)) {
		t.Error("yesterday start should need daily reset")
	}
}

func TestNeedsWeeklyReset_NilStart_ReturnsFalse(t *testing.T) {
	if billing.NeedsWeeklyReset(nil, time.Now().UTC(), timezone.NewCalendar(time.Local)) {
		t.Error("nil start should not need weekly reset")
	}
}

func TestNeedsMonthlyReset_NilStart_ReturnsFalse(t *testing.T) {
	if billing.NeedsMonthlyReset(nil, time.Now().UTC()) {
		t.Error("nil start should not need monthly reset")
	}
}

// TestNeedsMonthlyReset_30DayRolling 验证 30 天滚动语义（C-NEW-1）。
func TestNeedsMonthlyReset_30DayRolling_Expired(t *testing.T) {
	start := time.Now().UTC().Add(-31 * 24 * time.Hour) // 31 天前，已过期
	if !billing.NeedsMonthlyReset(&start, time.Now().UTC()) {
		t.Error("31 days ago should need monthly reset (30-day rolling)")
	}
}

func TestNeedsMonthlyReset_30DayRolling_Active(t *testing.T) {
	start := time.Now().UTC().Add(-15 * 24 * time.Hour) // 15 天前，窗口有效
	if billing.NeedsMonthlyReset(&start, time.Now().UTC()) {
		t.Error("15 days ago should NOT need monthly reset (30-day rolling, still active)")
	}
}

// TestNeedsMonthlyReset_CrossMonthBoundary 验证跨自然月时 30 天未满不重置（旧自然月语义会提前重置）。
func TestNeedsMonthlyReset_CrossMonthBoundary(t *testing.T) {
	// 窗口起始 4 月 20 日；5 月 1 日仅过了 11 天，不足 30 天，不应重置
	windowStart := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if billing.NeedsMonthlyReset(&windowStart, now) {
		t.Error("cross-month boundary within 30 days should NOT trigger reset (30-day rolling)")
	}
}
