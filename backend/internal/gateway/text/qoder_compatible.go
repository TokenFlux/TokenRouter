package text

import "context"

// QoderCompatibleOutcome 固化该协议入口原有的字节提交与部分用量边界。
type QoderCompatibleOutcome struct {
	Err                                                       error
	OutputChanged, Partial, Canceled, CanRefresh, CanFailover bool
}
type QoderRefreshResult struct{ Ready, Pending bool }

// QoderCompatiblePorts 承接 Messages/Responses 的单步执行。Chat 的 S09 契约独立保留，
// 此入口不追加等待后权益复查，且任意实际输出都关闭当前尝试的重试窗口。
type QoderCompatiblePorts interface {
	Context() context.Context
	Select(map[int64]struct{}) (Selection, error)
	SelectionFailed(error, bool, bool, error)
	Acquire(bool) bool
	Forward() QoderCompatibleOutcome
	Refresh() QoderRefreshResult
	RefreshPending(bool)
	Partial(QoderCompatibleOutcome)
	Canceled(bool, error)
	Failure(QoderCompatibleOutcome) bool
	Exhausted(error)
	Success()
	Switched()
}

// RunQoderCompatible 只拥有当前请求的账号循环，不执行第二层全局 failover。
func RunQoderCompatible(p QoderCompatiblePorts, maxAccounts int) {
	excluded := make(map[int64]struct{})
	pending := false
	var last error
	for {
		selected, err := p.Select(excluded)
		if err != nil {
			p.SelectionFailed(err, pending, len(excluded) == 0, last)
			return
		}
		pending = false
		if !p.Acquire(false) {
			return
		}
		result := p.Forward()
		if result.Partial {
			p.Partial(result)
			return
		}
		if result.Canceled {
			p.Canceled(false, result.Err)
			return
		}
		if result.Err != nil && !result.OutputChanged && result.CanRefresh {
			refresh := p.Refresh()
			if refresh.Ready {
				if !p.Acquire(true) {
					return
				}
				result = p.Forward()
				if result.Partial {
					p.Partial(result)
					return
				}
				if result.Canceled {
					p.Canceled(true, result.Err)
					return
				}
			} else if refresh.Pending {
				if !result.OutputChanged {
					excluded[selected.Account.ID] = struct{}{}
					if len(excluded) < maxAccounts {
						pending = true
						p.Switched()
						continue
					}
				}
				p.RefreshPending(false)
				return
			}
		}
		if result.Err == nil {
			p.Success()
			return
		}
		if !result.OutputChanged && result.CanFailover {
			excluded[selected.Account.ID] = struct{}{}
			if len(excluded) < maxAccounts {
				last = result.Err
				p.Switched()
				continue
			}
		}
		// 原入口先尝试已知错误展示，只有未分类且尚未输出的错误继续换号。
		if p.Failure(result) {
			return
		}
		excluded[selected.Account.ID] = struct{}{}
		if len(excluded) < maxAccounts {
			last = result.Err
			p.Switched()
			continue
		}
		p.Exhausted(result.Err)
		return
	}
}
