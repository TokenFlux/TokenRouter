// Package upstream 定义单次平台执行的输入输出边界，不持有业务服务或网络客户端。
package upstream

import (
	"encoding/json"
	"io"
	"net/http"
)

// OutputHead 是输出适配器需要的响应元数据，Header 在传递时复制。
type OutputHead struct {
	// Committed 只用于带入既有 HTTP 提交状态，不决定本次 attempt 的重试。
	Committed bool
	Status    int
	Header    http.Header
}

// OutputEvent 是逐段输出；Flush 不建立异步队列，错误同步返回执行方。
type OutputEvent struct {
	Data           []byte
	Flush          bool
	Semantic       bool
	CommitForRetry bool
	Terminal       bool
}

// OutputSink 由 HTTP 或其他调用适配器实现，拥有实际写入与刷新。
type OutputSink interface {
	Begin(OutputHead) error
	Emit(OutputEvent) error
}

// OutputContext 只在一次转换中持有字节输出适配，不保存入站 HTTP 请求或业务上下文。
type OutputContext struct{ Writer OutputWriter }

// OutputWriter 让现有逐段编解码器保持写入顺序；实际 I/O 仍只发生在注入的 sink。
type OutputWriter interface {
	io.Writer
	Header() http.Header
	WriteHeader(int)
	WriteHeaderNow()
	Written() bool
	Flush()
}

// NewOutputContext 为一次流转换建立独立元数据，禁止跨请求复用。
func NewOutputContext(sink OutputSink) *OutputContext {
	header := make(http.Header)
	status := http.StatusOK
	committed := false
	if source, ok := sink.(interface{ InitialOutput() OutputHead }); ok {
		head := source.InitialOutput()
		for key, values := range head.Header {
			header[key] = append([]string(nil), values...)
		}
		committed = head.Committed
		if committed && head.Status != 0 {
			status = head.Status
		}
	}
	return &OutputContext{Writer: &sinkWriter{sink: sink, header: header, status: status, written: committed}}
}

type sinkWriter struct {
	sink    OutputSink
	header  http.Header
	status  int
	started bool
	written bool
	err     error
	next    *OutputEvent
}

func (w *sinkWriter) Header() http.Header { return w.header }
func (w *sinkWriter) Written() bool       { return w.written }
func (w *sinkWriter) WriteHeader(status int) {
	if !w.started {
		w.status = status
	}
}
func (w *sinkWriter) begin() error {
	if w.err != nil {
		return w.err
	}
	if !w.started {
		w.started = true
		w.err = w.sink.Begin(OutputHead{Status: w.status, Header: w.header.Clone()})
		if w.err == nil {
			w.written = true
		}
	}
	return w.err
}
func (w *sinkWriter) Write(p []byte) (int, error) {
	if err := w.begin(); err != nil {
		return 0, err
	}
	w.written = true
	event := OutputEvent{CommitForRetry: true}
	if w.next != nil {
		event = *w.next
		w.next = nil
	}
	event.Data = p
	w.err = w.sink.Emit(event)
	if w.err != nil {
		return 0, w.err
	}
	return len(p), nil
}
func (w *sinkWriter) Flush() {
	if w.begin() == nil {
		w.err = w.sink.Emit(OutputEvent{Flush: true})
	}
}

// Header 只修改待发元数据，实际响应仍由 OutputSink 写入。
func (c *OutputContext) Header(key, value string) { c.Writer.Header().Set(key, value) }

// WriteHeaderNow 保留旧输出器提交空响应头的时机，不额外 Flush。
func (w *sinkWriter) WriteHeaderNow() { _ = w.begin() }

// Data 按既有 HTTP Data 规则设置缺省内容类型；无 body 状态只提交响应头。
func (c *OutputContext) Data(status int, contentType string, body []byte) {
	if len(c.Writer.Header()["Content-Type"]) == 0 {
		c.Writer.Header()["Content-Type"] = []string{contentType}
	}
	c.Writer.WriteHeader(status)
	if status < 200 || status == http.StatusNoContent || status == http.StatusNotModified {
		c.Writer.WriteHeaderNow()
		return
	}
	_, _ = c.Writer.Write(body)
}

// NextEvent 为下一次同步字节写入补充协议事实，不改变提交和刷新时点。
func (c *OutputContext) NextEvent(semantic, terminal bool) {
	if writer, ok := c.Writer.(*deferredOutputWriter); ok {
		(&OutputContext{Writer: writer.writer()}).NextEvent(semantic, terminal)
		return
	}
	if writer, ok := c.Writer.(*sinkWriter); ok {
		writer.next = &OutputEvent{Semantic: semantic, Terminal: terminal, CommitForRetry: true}
	}
}

// Status 只设置后续输出状态，不提前提交 HTTP 响应。
func (c *OutputContext) Status(status int) { c.Writer.WriteHeader(status) }

// JSON 保留原 Data 的提交时机，实际字节输出仍通过 sink。
func (c *OutputContext) JSON(status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		c.Writer.WriteHeader(status)
		c.Writer.WriteHeaderNow()
		return
	}
	c.Data(status, "application/json; charset=utf-8", body)
}

// Err 只读取本次同步输出失败，不改变各平台的继续读取或取消策略。
func (c *OutputContext) Err() error {
	if writer, ok := c.Writer.(*deferredOutputWriter); ok {
		if writer.output == nil {
			return nil
		}
		return (&OutputContext{Writer: writer.output}).Err()
	}
	if writer, ok := c.Writer.(*sinkWriter); ok {
		return writer.err
	}
	return nil
}
