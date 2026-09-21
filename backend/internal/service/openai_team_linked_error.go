package service

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/tidwall/gjson"
)

// 工作区联动旧入口只识别供应商错误并投影账号。
func (s *RateLimitService) maybeHandleOpenAITeamLinkedError(ctx context.Context, value *Account, status int, body []byte) {
	if s == nil {
		return
	}
	s.TeamLinkedHealth().HandleWorkspaceDeactivated(ctx, AccountRecordView(value), status == http.StatusPaymentRequired && gjson.GetBytes(body, "detail.code").String() == "deactivated_workspace")
}

// BindTeamLinkedHealth 由 app 绑定唯一原生共享状态。
func (s *RateLimitService) BindTeamLinkedHealth(core *account.TeamLinkedHealth) { s.teamLinked = core }

// TeamLinkedHealth 只为旧独立构造保留原生拥有者，不保留另一份去重状态。
func (s *RateLimitService) TeamLinkedHealth() *account.TeamLinkedHealth {
	s.teamLinkedOnce.Do(func() {
		if s.teamLinked != nil {
			return
		}
		var store account.TeamLinkedStore
		if s.accountRepo != nil {
			store = teamLinkedStoreProjection{s.accountRepo}
		}
		s.teamLinked = account.NewTeamLinkedHealth(store, account.TeamLinkedOptions{Now: time.Now, Warn: slog.Warn, Block: func(value *account.Record, until time.Time, reason string) {
			s.notifyAccountSchedulingBlocked(AccountFromRecord(value), until, reason)
		}})
	})
	return s.teamLinked
}

type teamLinkedStoreProjection struct{ AccountRepository }

func (s teamLinkedStoreProjection) ListByPlatform(ctx context.Context, platform string) ([]account.Record, error) {
	values, err := s.AccountRepository.ListByPlatform(ctx, platform)
	if values == nil {
		return nil, err
	}
	out := make([]account.Record, len(values))
	for i := range values {
		out[i] = *AccountRecordView(&values[i])
	}
	return out, err
}
