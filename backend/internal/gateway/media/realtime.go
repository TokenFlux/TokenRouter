package media

import (
	"context"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// RealtimePorts 仅在下游升级前取得凭据和连接，保持四次候选预算。
type RealtimePorts interface {
	SelectRealtime(context.Context, map[int64]struct{}) (account.AccountSnapshot, bool, error)
	AcquireRealtime(context.Context, account.AccountSnapshot) (func(), bool)
	RealtimeCredential(context.Context, account.AccountSnapshot) (string, error)
	OpenRealtime(context.Context, account.AccountSnapshot, string, string) (upstream.FrameConn, error)
	RealtimeOpenFailed(context.Context, account.AccountSnapshot, error)
}

// RealtimeLease 明确拥有上游连接及账号槽；下游连接由 HTTP Adapter 关闭。
type RealtimeLease struct {
	Account account.AccountSnapshot
	Conn    upstream.FrameConn
	release func()
	once    sync.Once
	err     error
}

func (l *RealtimeLease) Close() error {
	l.once.Do(func() {
		if l.Conn != nil {
			l.err = l.Conn.Close()
		}
		if l.release != nil {
			l.release()
		}
	})
	return l.err
}

// RealtimeAdmission 区分没有候选、上游不可用与等待层已经输出错误。
type RealtimeAdmission struct {
	Lease         *RealtimeLease
	CandidateSeen bool
	WaitRejected  bool
}

func OpenRealtime(ctx context.Context, model string, dialTimeout time.Duration, ports RealtimePorts) RealtimeAdmission {
	excluded := make(map[int64]struct{})
	result := RealtimeAdmission{}
	for range 4 {
		candidate, present, err := ports.SelectRealtime(ctx, excluded)
		if err != nil || !present {
			break
		}
		result.CandidateSeen = true
		release, acquired := ports.AcquireRealtime(ctx, candidate)
		if !acquired {
			result.WaitRejected = true
			return result
		}
		token, err := ports.RealtimeCredential(ctx, candidate)
		if err != nil {
			release()
			excluded[candidate.ID] = struct{}{}
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		conn, err := ports.OpenRealtime(probeCtx, candidate, token, model)
		cancel()
		if err != nil {
			ports.RealtimeOpenFailed(ctx, candidate, err)
			release()
			excluded[candidate.ID] = struct{}{}
			continue
		}
		if release == nil || conn == nil {
			return result
		}
		result.Lease = &RealtimeLease{Account: candidate, Conn: conn, release: release}
		return result
	}
	return result
}
