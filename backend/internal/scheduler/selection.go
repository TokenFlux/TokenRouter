package scheduler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// SelectionCandidate 只携带候选快照和当次观测；关联号由外层当前选择调用独立管理。
type SelectionCandidate struct {
	Snapshot     *account.AccountSnapshot
	ProjectionID uint64
	Load         *AccountLoadInfo
	LoadKnown    bool
	Plan         *routing.CandidatePlan
}

// SelectionInput 固化最终分组与请求能力，不提前确定账号或保存到共享缓存。
type SelectionInput struct {
	GroupID               *int64
	RequestedModel        string
	RoutePlan             routing.RoutePlan
	Candidates            []SelectionCandidate
	ExcludedIDs           map[int64]struct{}
	RequireCompact        bool
	SessionHash           string
	PreserveStickyBinding bool
}

// ResolveCandidate 在每次候选、fresh/DB 复核点执行单步协议解析，不保存共享状态。
func (in SelectionInput) ResolveCandidate(candidate account.AccountSnapshot) (routing.CandidatePlan, bool) {
	return in.RoutePlan.ResolveCandidate(candidate)
}

// AttemptSelectionPorts 保留获取、fresh 和数据库复核的独立预算及查询时点。
// 供应商资格由只读端口给出，gateway 继续拥有真正的上游重试循环。
type AttemptSelectionPorts struct {
	Acquire        func(context.Context, int64, int) (*AcquireResult, bool, error)
	Fresh          func(context.Context, SelectionCandidate) (SelectionCandidate, bool)
	CanRecheck     func() bool
	Recheck        func(context.Context, SelectionCandidate) (SelectionCandidate, bool)
	CompactAllowed func(SelectionCandidate) bool
	BindSticky     func(context.Context, SelectionCandidate)
}
type SelectionResult struct {
	Candidate      SelectionCandidate
	Attempt        *AttemptLease
	CompactBlocked bool
}

// Select 接管每次已取得槽位，所有补全、协议与资格复核失败均通过 AttemptLease 归还。
// 本方法只推进已排列候选，不执行上游请求或建立另一套 failover。
func (l *Lease) Select(ctx context.Context, input SelectionInput, ports AttemptSelectionPorts) (SelectionResult, bool, error) {
	ctx = WithRequestLease(ctx, l)
	output := SelectionResult{}
	for _, candidate := range input.Candidates {
		if candidate.Snapshot == nil {
			continue
		}
		if _, excluded := input.ExcludedIDs[candidate.Snapshot.ID]; excluded {
			continue
		}
		if candidate.LoadKnown && candidate.Snapshot.Concurrency > 0 && candidate.Load.CurrentConcurrency >= candidate.Snapshot.Concurrency {
			continue
		}
		acquired, attempted, err := ports.Acquire(ctx, candidate.Snapshot.ID, candidate.Snapshot.Concurrency)
		if !attempted {
			break
		}
		if err != nil {
			return output, false, err
		}
		if acquired == nil || !acquired.Acquired {
			continue
		}
		attempt := NewAttemptLease(l, nil, acquired.ReleaseFunc)
		fresh, ok := ports.Fresh(ctx, candidate)
		if !ok {
			attempt.Release()
			continue
		}
		if !ports.CanRecheck() {
			attempt.Release()
			break
		}
		fresh, ok = ports.Recheck(ctx, fresh)
		if !ok {
			attempt.Release()
			continue
		}
		if input.RequireCompact && !ports.CompactAllowed(fresh) {
			output.CompactBlocked = true
			attempt.Release()
			continue
		}
		if fresh.Snapshot.Concurrency != candidate.Snapshot.Concurrency {
			attempt.Release()
			acquired, attempted, err = ports.Acquire(ctx, fresh.Snapshot.ID, fresh.Snapshot.Concurrency)
			if !attempted {
				continue
			}
			if err != nil {
				return output, false, err
			}
			if acquired == nil || !acquired.Acquired {
				continue
			}
			attempt = NewAttemptLease(l, nil, acquired.ReleaseFunc)
		}
		if input.SessionHash != "" && !input.PreserveStickyBinding && ports.BindSticky != nil {
			ports.BindSticky(ctx, fresh)
		}
		output.Candidate = fresh
		output.Attempt = attempt
		return output, true, nil
	}
	return output, false, nil
}
