package httpapi

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const openAIImagesJSONKeepaliveKey = "openai_images_json_keepalive"

// openAIImagesJSONKeepalive 在 OAuth 上游内部生成 SSE 时保持非流式图片请求连接。
// JSON 允许前导空白，因此每次心跳都不会破坏最终 JSON 文档的解析。
// 首次心跳会提交 200 状态；之后的上游错误仍以 OpenAI 兼容 JSON 返回，
// 与 compact SSE keepalive 路径的状态取舍保持一致。
type openAIImagesJSONKeepalive struct {
	mu      sync.Mutex
	writer  gin.ResponseWriter
	started bool
	stopped bool
	bytes   int
	stop    chan struct{}
}

// StartOpenAIImagesJSONKeepalive 为非流式图片请求启动空白心跳；非正间隔表示禁用。
func StartOpenAIImagesJSONKeepalive(c *gin.Context, interval time.Duration) func() {
	if c == nil || c.Writer == nil || interval <= 0 {
		return func() {}
	}
	originalWriter := c.Writer
	k := &openAIImagesJSONKeepalive{
		writer: originalWriter,
		stop:   make(chan struct{}),
	}
	c.Set(openAIImagesJSONKeepaliveKey, k)
	wrappedWriter := &openAIImagesJSONKeepaliveWriter{ResponseWriter: originalWriter, k: k}
	c.Writer = wrappedWriter

	var reqDone <-chan struct{}
	if c.Request != nil {
		reqDone = c.Request.Context().Done()
	}
	go func() {
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-k.stop:
				return
			case <-reqDone:
				return
			case <-timer.C:
			}
			if !k.beat() {
				return
			}
			timer.Reset(interval)
		}
	}()

	return func() {
		k.Stop()
		if current, ok := c.Writer.(*openAIImagesJSONKeepaliveWriter); ok && current == wrappedWriter {
			c.Writer = originalWriter
		}
	}
}

func (k *openAIImagesJSONKeepalive) beat() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.stopped {
		return false
	}
	if !k.started {
		header := k.writer.Header()
		header.Set("Content-Type", "application/json; charset=utf-8")
		header.Set("Cache-Control", "no-cache")
		header.Set("X-Accel-Buffering", "no")
		k.writer.WriteHeader(http.StatusOK)
		k.started = true
	}
	n, err := k.writer.Write([]byte(" \n"))
	k.bytes += n
	if err != nil {
		k.stopped = true
		return false
	}
	k.writer.Flush()
	return true
}

func (k *openAIImagesJSONKeepalive) Stop() {
	k.mu.Lock()
	k.markStoppedLocked()
	k.mu.Unlock()
}

func (k *openAIImagesJSONKeepalive) markStoppedLocked() {
	if k.stopped {
		return
	}
	k.stopped = true
	close(k.stop)
}

// StopOpenAIImagesJSONKeepaliveCommitted 停止心跳，并返回是否已经提交 200 响应。
func StopOpenAIImagesJSONKeepaliveCommitted(c *gin.Context) bool {
	k := openAIImagesJSONKeepaliveFromContext(c)
	if k == nil {
		return false
	}
	k.mu.Lock()
	k.markStoppedLocked()
	committed := k.started
	k.mu.Unlock()
	return committed
}

// OpenAIImagesJSONKeepalivePresent 判断当前响应是否安装了图片 JSON keepalive，
// 包括首个心跳尚未写出的快速响应阶段。
func OpenAIImagesJSONKeepalivePresent(c *gin.Context) bool {
	return openAIImagesJSONKeepaliveFromContext(c) != nil
}

// OpenAIImagesJSONKeepaliveAdjustedWrittenSize 从响应大小中排除心跳空白，
// 使账号重试和故障切换仍可继续执行。
func OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c *gin.Context) int {
	if c == nil || c.Writer == nil {
		return -1
	}
	k := openAIImagesJSONKeepaliveFromContext(c)
	if k == nil {
		return c.Writer.Size()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	size := k.writer.Size()
	if size < 0 {
		return size
	}
	if real := size - k.bytes; real > 0 {
		return real
	}
	return -1
}

func openAIImagesJSONKeepaliveFromContext(c *gin.Context) *openAIImagesJSONKeepalive {
	if c == nil {
		return nil
	}
	value, ok := c.Get(openAIImagesJSONKeepaliveKey)
	if !ok {
		return nil
	}
	k, _ := value.(*openAIImagesJSONKeepalive)
	return k
}

type openAIImagesJSONKeepaliveWriter struct {
	gin.ResponseWriter
	k *openAIImagesJSONKeepalive
}

func (w *openAIImagesJSONKeepaliveWriter) suspend() {
	if w.k != nil {
		w.k.Stop()
	}
}

func (w *openAIImagesJSONKeepaliveWriter) Header() http.Header {
	w.suspend()
	if w.ResponseWriter == nil {
		return http.Header{}
	}
	return w.ResponseWriter.Header()
}

func (w *openAIImagesJSONKeepaliveWriter) Write(data []byte) (int, error) {
	w.suspend()
	if w.ResponseWriter == nil {
		return 0, nil
	}
	return w.ResponseWriter.Write(data)
}

func (w *openAIImagesJSONKeepaliveWriter) WriteString(s string) (int, error) {
	w.suspend()
	if w.ResponseWriter == nil {
		return 0, nil
	}
	return w.ResponseWriter.WriteString(s)
}

func (w *openAIImagesJSONKeepaliveWriter) WriteHeader(code int) {
	w.suspend()
	if w.ResponseWriter != nil {
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *openAIImagesJSONKeepaliveWriter) WriteHeaderNow() {
	w.suspend()
	if w.ResponseWriter != nil {
		w.ResponseWriter.WriteHeaderNow()
	}
}

func (w *openAIImagesJSONKeepaliveWriter) Flush() {
	w.suspend()
	if w.ResponseWriter != nil {
		w.ResponseWriter.Flush()
	}
}

func (w *openAIImagesJSONKeepaliveWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.ResponseWriter == nil {
		return nil, nil, errors.New("response writer released")
	}
	return w.ResponseWriter.Hijack()
}

func (w *openAIImagesJSONKeepaliveWriter) CloseNotify() <-chan bool {
	if w.ResponseWriter == nil {
		ch := make(chan bool)
		close(ch)
		return ch
	}
	return w.ResponseWriter.CloseNotify()
}

func (w *openAIImagesJSONKeepaliveWriter) Pusher() http.Pusher {
	if w.ResponseWriter == nil {
		return nil
	}
	return w.ResponseWriter.Pusher()
}

func (w *openAIImagesJSONKeepaliveWriter) Status() int {
	if w.k == nil || w.ResponseWriter == nil {
		return 0
	}
	w.k.mu.Lock()
	defer w.k.mu.Unlock()
	return w.ResponseWriter.Status()
}

func (w *openAIImagesJSONKeepaliveWriter) Size() int {
	if w.k == nil || w.ResponseWriter == nil {
		return 0
	}
	w.k.mu.Lock()
	defer w.k.mu.Unlock()
	return w.ResponseWriter.Size()
}

func (w *openAIImagesJSONKeepaliveWriter) Written() bool {
	if w.k == nil || w.ResponseWriter == nil {
		return false
	}
	w.k.mu.Lock()
	defer w.k.mu.Unlock()
	return w.ResponseWriter.Written()
}
