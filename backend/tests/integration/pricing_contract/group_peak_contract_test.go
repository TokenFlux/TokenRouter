package pricingcontract

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// TestPeakMultiplier_GatewayBillingSequence 调用 gateway_service.recordUsageCore 与
// openai_gateway_service.RecordUsage 共用的 computePeakAwareMultipliers，验证计费叠加顺序：
// 图片按次倍率基于基础倍率算出且不受高峰影响，高峰因子只乘入 token 倍率。
// 若有人调换叠加顺序或把高峰并入 imageMultiplier，此测试会失败。
func TestPeakMultiplier_GatewayBillingSequence(t *testing.T) {
	const baseMultiplier = 0.8
	apiKey := &apikey.APIKey{Group: newPeakGroup(true, "14:00", "18:00", 3.0)}
	keySnapshot := gatewayprovider.ProjectCompletionKey(apiKey)
	// 原合同在UTC运行，显式指定快照时区，避免改变同进程其他测试。
	keySnapshot.Group.Location = time.UTC
	approxEq := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

	t.Run("peak hour amplifies token multiplier only", func(t *testing.T) {
		now := at(15, 30) // 处于 [14:00, 18:00)
		tokenMultiplier, imageMultiplier := completion.ComputePeakAwareMultipliers(keySnapshot, baseMultiplier, now)
		if !approxEq(imageMultiplier, baseMultiplier) {
			t.Fatalf("image multiplier must not be affected by peak: got %v, want %v", imageMultiplier, baseMultiplier)
		}
		if want := baseMultiplier * 3.0; !approxEq(tokenMultiplier, want) {
			t.Fatalf("token multiplier should include peak factor: got %v, want %v", tokenMultiplier, want)
		}
	})

	t.Run("off-peak leaves both multipliers at base", func(t *testing.T) {
		now := at(20, 0)
		tokenMultiplier, imageMultiplier := completion.ComputePeakAwareMultipliers(keySnapshot, baseMultiplier, now)
		if !approxEq(imageMultiplier, baseMultiplier) {
			t.Fatalf("image multiplier: got %v, want %v", imageMultiplier, baseMultiplier)
		}
		if !approxEq(tokenMultiplier, baseMultiplier) {
			t.Fatalf("token multiplier should equal base off-peak: got %v, want %v", tokenMultiplier, baseMultiplier)
		}
	})

	t.Run("nil api key degrades to base multipliers", func(t *testing.T) {
		now := at(15, 30)
		tokenMultiplier, imageMultiplier := completion.ComputePeakAwareMultipliers(nil, baseMultiplier, now)
		if !approxEq(tokenMultiplier, baseMultiplier) {
			t.Fatalf("nil group token multiplier: got %v, want %v", tokenMultiplier, baseMultiplier)
		}
		if !approxEq(imageMultiplier, baseMultiplier) {
			t.Fatalf("nil group image multiplier: got %v, want %v", imageMultiplier, baseMultiplier)
		}
	})
}

// TestPeakMultiplier_SnapshotRoundTrip 防回归：认证缓存快照（APIKeyAuthGroupSnapshot）
// 必须携带高峰倍率 4 字段，否则扣费路径拿到的 apiKey.Group 会缺字段、PeakMultiplierAt 恒降级为 1.0。
// 调用真实链路 snapshotFromAPIKey → snapshotToAPIKey，验证 peak 配置经快照往返后仍生效。
func TestPeakMultiplier_SnapshotRoundTrip(t *testing.T) {
	apiKey := &apikey.APIKey{
		User:  &identity.User{ID: 1, Status: billing.StatusActive, Role: identity.RoleUser},
		Group: newPeakGroup(true, "14:00", "18:00", 3.0),
	}
	svc := apikey.NewAPIKeyService(nil, nil, nil, nil, nil, nil, &apikey.Options{})

	snapshot := svc.KeySnapshotFromAPIKey(context.Background(), apiKey)
	if snapshot == nil || snapshot.Group == nil {
		t.Fatalf("snapshot or snapshot.Group must not be nil")
	}
	restored := svc.KeySnapshotToAPIKey("k", snapshot)
	if restored.Group == nil {
		t.Fatalf("restored.Group must not be nil")
	}

	if !restored.Group.PeakRateEnabled ||
		restored.Group.PeakStart != "14:00" ||
		restored.Group.PeakEnd != "18:00" ||
		restored.Group.PeakRateMultiplier != 3.0 {
		t.Fatalf("peak fields lost in snapshot round-trip: %+v", restored.Group)
	}
	if got := restored.Group.PeakMultiplierAt(at(15, 30)); got != 3.0 {
		t.Fatalf("peak hour multiplier after round-trip: got %v, want 3.0", got)
	}
	if got := restored.Group.PeakMultiplierAt(at(20, 0)); got != 1.0 {
		t.Fatalf("off-peak multiplier after round-trip: got %v, want 1.0", got)
	}
}

func newPeakGroup(enabled bool, start, end string, mult float64) *routing.Group {
	return &routing.Group{
		PeakRateEnabled:    enabled,
		PeakStart:          start,
		PeakEnd:            end,
		PeakRateMultiplier: mult,
	}
}

func at(hour, min int) time.Time {
	return time.Date(2026, 6, 29, hour, min, 0, 0, time.UTC)
}
