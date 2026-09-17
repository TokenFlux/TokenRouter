// 取消限频保持原固定/滚动窗口和查询失败放行。
package payment

import (
	"context"
	"fmt"
	"strconv"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func (s *Checkout) CheckCancelRateLimit(ctx context.Context, userID int64, cfg *PaymentConfig) error {
	if !cfg.CancelRateLimitEnabled || cfg.CancelRateLimitMax <= 0 {
		return nil
	}
	windowStart := CancelRateLimitWindowStart(cfg, s.runtime.Now())
	operator := fmt.Sprintf("user:%d", userID)
	count, err := s.store.CancelledCount(ctx, operator, windowStart)
	if err != nil {
		s.runtime.Error("check cancel rate limit failed", "userID", userID, "error", err)
		return nil // fail open
	}
	if count >= cfg.CancelRateLimitMax {
		return infraerrors.TooManyRequests("CANCEL_RATE_LIMITED", "cancel rate limited").
			WithMetadata(map[string]string{
				"max":    strconv.Itoa(cfg.CancelRateLimitMax),
				"window": strconv.Itoa(cfg.CancelRateLimitWindow),
				"unit":   cfg.CancelRateLimitUnit,
			})
	}
	return nil
}
func CancelRateLimitWindowStart(cfg *PaymentConfig, now time.Time) time.Time {
	w := cfg.CancelRateLimitWindow
	if w <= 0 {
		w = 1
	}
	unit := cfg.CancelRateLimitUnit
	if unit == "" {
		unit = "day"
	}
	if cfg.CancelRateLimitMode == "fixed" {
		switch unit {
		case "minute":
			t := now.Truncate(time.Minute)
			return t.Add(-time.Duration(w-1) * time.Minute)
		case "day":
			y, m, d := now.Date()
			t := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
			return t.AddDate(0, 0, -(w - 1))
		default: // hour
			t := now.Truncate(time.Hour)
			return t.Add(-time.Duration(w-1) * time.Hour)
		}
	}
	// rolling window
	switch unit {
	case "minute":
		return now.Add(-time.Duration(w) * time.Minute)
	case "day":
		return now.AddDate(0, 0, -w)
	default: // hour
		return now.Add(-time.Duration(w) * time.Hour)
	}
}
