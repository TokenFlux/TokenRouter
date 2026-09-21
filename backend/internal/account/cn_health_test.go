package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 原顺序及同一输入契约在健康规则所属核心验证；旧适配只转换模型。
type cnHealthFailureStore struct {
	HealthStore
	order *[]string
}

func (s cnHealthFailureStore) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	*s.order = append(*s.order, "store")
	return errors.New("fixture persistence failed")
}
func TestCNConcurrencyRepositoryFailureKeepsRuntimeBlock(t *testing.T) {
	var order []string
	var blocked *Record
	now := time.Now()
	core := NewHealthService(cnHealthFailureStore{order: &order}, nil, HealthOptions{Now: func() time.Time { return now }, Block: func(v *Record, until time.Time, reason string) {
		order = append(order, "block")
		blocked = v
		require.Equal(t, now.Add(10*time.Minute), until)
		require.Equal(t, "cn_concurrency_limit", reason)
	}})
	original := &Record{ID: 406, Platform: PlatformKimi, Type: AccountTypeAPIKey}
	core.ApplyCNConcurrencyLimit(context.Background(), original, "fixture concurrency")
	require.Equal(t, []string{"block", "store"}, order)
	require.Same(t, original, blocked)
}
