package media

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// VoiceRequest 是完成原鉴权/审核后的报文投影，不负责重新读取请求体。
type VoiceRequest struct {
	Endpoint, ContentType string
	Body                  []byte
}
type VoiceResult struct {
	RequestID, Model, UpstreamModel string
	Headers                         map[string][]string
	Duration                        time.Duration
	AudioUsage                      *protocol.AudioUsage
}
type VoiceOutcome struct {
	Result    *VoiceResult
	Err       error
	RetryNext bool
}
type VoicePorts interface {
	SelectVoice(context.Context, map[int64]struct{}) (account.AccountSnapshot, bool, error)
	AcquireVoice(context.Context, account.AccountSnapshot) (func(), bool)
	ForwardVoice(context.Context, account.AccountSnapshot, VoiceRequest) VoiceOutcome
	CompleteVoice(context.Context, account.AccountSnapshot, VoiceRequest, *VoiceResult)
}

// VoiceFailure 只在候选耗尽时交给 HTTP 输出；其它错误仍由原生响应 Adapter 输出。
type VoiceFailure struct {
	NoAccounts bool
	Last       error
}

func RunVoice(ctx context.Context, request VoiceRequest, ports VoicePorts) *VoiceFailure {
	excluded := make(map[int64]struct{})
	var last error
	for range 4 {
		selected, present, err := ports.SelectVoice(ctx, excluded)
		if err != nil || !present {
			return &VoiceFailure{NoAccounts: last == nil, Last: last}
		}
		release, acquired := ports.AcquireVoice(ctx, selected)
		if !acquired {
			return nil
		}
		outcome := func() VoiceOutcome { defer release(); return ports.ForwardVoice(ctx, selected, request) }()
		if outcome.Err == nil {
			ports.CompleteVoice(ctx, selected, request, outcome.Result)
			return nil
		}
		if outcome.RetryNext {
			excluded[selected.ID] = struct{}{}
			last = outcome.Err
			continue
		}
		return nil
	}
	if last != nil {
		return &VoiceFailure{Last: last}
	}
	return nil
}
