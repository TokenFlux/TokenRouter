package media

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// EmbeddingResult 固化本次已选账号执行的计费观测，不携带旧实体或连接。
type EmbeddingResult struct {
	RequestID, Model, BillingModel, UpstreamModel string
	Headers                                       map[string][]string
	Usage                                         protocolopenai.ForwardUsage
	Duration                                      time.Duration
}

// EmbeddingOutcome 分离输出窗口与可切换错误，核心不读取 HTTP Writer。
type EmbeddingOutcome struct {
	Result        *EmbeddingResult
	Err           error
	Failover      bool
	OutputChanged bool
	StatusCode    int
}

// EmbeddingEvent 只报告编排时点，日志和 HTTP 输出由入口处理。
type EmbeddingEvent struct {
	Kind                            string
	Account                         account.AccountSnapshot
	Outcome                         EmbeddingOutcome
	Excluded, Switches, MaxSwitches int
	Elapsed                         time.Duration
}

// EmbeddingsPorts 是单请求端口；选取与等待使用同一已获得的选择，不能再次选号。
type EmbeddingsPorts interface {
	SelectEmbedding(context.Context, map[int64]struct{}) (account.AccountSnapshot, bool, error)
	AcquireEmbedding(context.Context, account.AccountSnapshot) (func(), bool)
	ForwardEmbedding(context.Context, account.AccountSnapshot, []byte) EmbeddingOutcome
	ReportEmbedding(context.Context, account.AccountSnapshot, *EmbeddingResult, bool, error)
	CompleteEmbedding(context.Context, account.AccountSnapshot, *EmbeddingResult)
	SwitchEmbedding(account.AccountSnapshot)
	ObserveEmbedding(EmbeddingEvent)
	ClientGone() bool
}

// EmbeddingFailure 描述输出阶段，由 HTTP Adapter 保留原错误形状。
type EmbeddingFailure struct {
	Stage    string
	Err      error
	Outcome  EmbeddingOutcome
	Excluded int
}

// RunEmbeddings 唯一拥有 Embeddings 账号尝试循环；不会在等待失败后另起一次请求。
// 准入已按原顺序完成，返回前先释放账号槽，成功后仅提交一次完成处理。
func RunEmbeddings(ctx context.Context, body []byte, maxSwitches int, ports EmbeddingsPorts) *EmbeddingFailure {
	if maxSwitches <= 0 {
		maxSwitches = 3
	}
	excluded := make(map[int64]struct{})
	var last EmbeddingOutcome
	switches := 0
	routingStart := time.Now()
	for {
		selected, present, err := ports.SelectEmbedding(ctx, excluded)
		if err != nil {
			if ports.ClientGone() {
				ports.ObserveEmbedding(EmbeddingEvent{Kind: "select_canceled", Outcome: EmbeddingOutcome{Err: err}})
				return nil
			}
			ports.ObserveEmbedding(EmbeddingEvent{Kind: "select_failed", Outcome: EmbeddingOutcome{Err: err}, Excluded: len(excluded)})
			return &EmbeddingFailure{Stage: "selection", Err: err, Outcome: last, Excluded: len(excluded)}
		}
		if !present {
			return &EmbeddingFailure{Stage: "empty_selection"}
		}
		ports.ObserveEmbedding(EmbeddingEvent{Kind: "selected", Account: selected})
		release, acquired := ports.AcquireEmbedding(ctx, selected)
		if !acquired {
			return nil
		}
		ports.ObserveEmbedding(EmbeddingEvent{Kind: "routing", Elapsed: time.Since(routingStart)})
		started := time.Now()
		outcome := func() EmbeddingOutcome {
			if release != nil {
				defer release()
			}
			return ports.ForwardEmbedding(ctx, selected, body)
		}()
		ports.ObserveEmbedding(EmbeddingEvent{Kind: "response", Elapsed: time.Since(started)})
		if outcome.Err != nil {
			if outcome.Failover {
				if outcome.OutputChanged {
					return &EmbeddingFailure{Stage: "exhausted", Outcome: outcome}
				}
				ports.ReportEmbedding(ctx, selected, outcome.Result, false, outcome.Err)
				if ports.ClientGone() {
					ports.ObserveEmbedding(EmbeddingEvent{Kind: "forward_canceled", Account: selected, Outcome: outcome})
					return nil
				}
				ports.SwitchEmbedding(selected)
				excluded[selected.ID] = struct{}{}
				last = outcome
				if switches >= maxSwitches {
					return &EmbeddingFailure{Stage: "exhausted", Outcome: outcome}
				}
				switches++
				ports.ObserveEmbedding(EmbeddingEvent{Kind: "switch", Account: selected, Outcome: outcome, Switches: switches, MaxSwitches: maxSwitches})
				continue
			}
			ports.ReportEmbedding(ctx, selected, outcome.Result, false, outcome.Err)
			return &EmbeddingFailure{Stage: "forward", Outcome: outcome, Err: outcome.Err}
		}
		ports.ReportEmbedding(ctx, selected, outcome.Result, true, nil)
		ports.CompleteEmbedding(ctx, selected, outcome.Result)
		ports.ObserveEmbedding(EmbeddingEvent{Kind: "completed", Account: selected, Switches: switches})
		return nil
	}
}
