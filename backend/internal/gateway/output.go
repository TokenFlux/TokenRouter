package gateway

import "github.com/TokenFlux/TokenRouter/internal/upstream"

// OutputTracker 分别保存 HTTP 提交、当前尝试重试边界及语义输出；等待心跳不会提交新尝试。
type OutputTracker struct {
	Sink             upstream.OutputSink
	HTTPCommitted    bool
	AttemptCommitted bool
	Semantic         bool
	Disconnected     bool
}

func (o *OutputTracker) BeginAttempt() {
	o.AttemptCommitted = false
	o.HTTPCommitted = o.HTTPCommitted || o.InitialOutput().Committed
}
func (o *OutputTracker) Begin(h upstream.OutputHead) error {
	err := o.Sink.Begin(h)
	if err != nil {
		o.Disconnected = true
	}
	return err
}
func (o *OutputTracker) Emit(e upstream.OutputEvent) error {
	if len(e.Data) > 0 || e.Flush {
		o.HTTPCommitted = true
	}
	if len(e.Data) > 0 && e.CommitForRetry {
		o.AttemptCommitted = true
	}
	if e.Semantic {
		o.Semantic = true
	}
	err := o.Sink.Emit(e)
	if err != nil {
		o.Disconnected = true
	}
	return err
}

func (o *OutputTracker) InitialOutput() upstream.OutputHead {
	if source, ok := o.Sink.(interface{ InitialOutput() upstream.OutputHead }); ok {
		return source.InitialOutput()
	}
	return upstream.OutputHead{Committed: o.HTTPCommitted}
}
