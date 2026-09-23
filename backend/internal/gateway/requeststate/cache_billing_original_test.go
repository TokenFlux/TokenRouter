//go:build unit

package requeststate

import (
	"context"
	"testing"
)

func TestIsForceCacheBilling(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected bool
	}{
		{
			name:     "context without force cache billing",
			ctx:      context.Background(),
			expected: false,
		},
		{
			name:     "context with force cache billing set to true",
			ctx:      context.WithValue(context.Background(), cacheBillingKey{}, true),
			expected: true,
		},
		{
			name:     "context with force cache billing set to false",
			ctx:      context.WithValue(context.Background(), cacheBillingKey{}, false),
			expected: false,
		},
		{
			name:     "context with wrong type value",
			ctx:      context.WithValue(context.Background(), cacheBillingKey{}, "true"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsForceCacheBilling(tt.ctx)
			if result != tt.expected {
				t.Errorf("IsForceCacheBilling() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestWithForceCacheBilling(t *testing.T) {
	ctx := context.Background()

	// 原始上下文没有标记
	if IsForceCacheBilling(ctx) {
		t.Error("original context should not have force cache billing")
	}

	// 使用 WithForceCacheBilling 后应该有标记
	newCtx := WithForceCacheBilling(ctx)
	if !IsForceCacheBilling(newCtx) {
		t.Error("new context should have force cache billing")
	}

	// 原始上下文应该不受影响
	if IsForceCacheBilling(ctx) {
		t.Error("original context should still not have force cache billing")
	}
}
