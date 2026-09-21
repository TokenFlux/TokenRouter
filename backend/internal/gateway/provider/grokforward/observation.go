package grokforward

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// ObservationPorts 只发布本次额度事实或请求精确恢复；账号领域决定是否及如何写入。
type ObservationPorts interface {
	Stamp(*grok.QuotaSnapshot, string)
	Store(context.Context, *grok.QuotaSnapshot)
	Recovery(*grok.QuotaSnapshot) bool
	ClearRecovered(context.Context)
}

// ObserveResponse 保留先解析/标记再发布的顺序；没有额度头时不以空快照覆盖原数据。
func ObserveResponse(ctx context.Context, p ObservationPorts, headers http.Header, status int, model string) {
	snapshot := grok.ParseQuotaObservation(headers, status, time.Now())
	if snapshot != nil {
		p.Stamp(snapshot, model)
		p.Store(ctx, snapshot)
		return
	}
	if p.Recovery(&grok.QuotaSnapshot{StatusCode: status}) {
		p.ClearRecovered(ctx)
	}
}
