// 延迟输出适配保持尚未交付时的 Header 访问时点，避免停止原 HTTP 等待心跳。
package upstream

import "net/http"

// NewDeferredOutputContext 直到真正触碰输出时才取得响应 Header，不预先提交。
func NewDeferredOutputContext(sink OutputSink) *OutputContext {
	return &OutputContext{Writer: &deferredOutputWriter{sink: sink}}
}

type deferredOutputWriter struct {
	sink   OutputSink
	output OutputWriter
}

func (w *deferredOutputWriter) writer() OutputWriter {
	if w.output == nil {
		w.output = NewOutputContext(w.sink).Writer
	}
	return w.output
}
func (w *deferredOutputWriter) Header() http.Header            { return w.writer().Header() }
func (w *deferredOutputWriter) Write(data []byte) (int, error) { return w.writer().Write(data) }
func (w *deferredOutputWriter) WriteHeader(status int)         { w.writer().WriteHeader(status) }
func (w *deferredOutputWriter) WriteHeaderNow()                { w.writer().WriteHeaderNow() }
func (w *deferredOutputWriter) Flush()                         { w.writer().Flush() }
func (w *deferredOutputWriter) Written() bool {
	if w.output != nil {
		return w.output.Written()
	}
	if state, ok := w.sink.(interface{ OutputState() OutputHead }); ok {
		return state.OutputState().Committed
	}
	return false
}
