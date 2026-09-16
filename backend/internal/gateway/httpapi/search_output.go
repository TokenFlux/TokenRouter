package httpapi

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// SearchOutput 保留原 SSE Header、逐事件 Flush 和非流 JSON 写出形状。
type SearchOutput struct{ Context *gin.Context }

func (o SearchOutput) StartStream() {
	header := o.Context.Writer.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	o.Context.Writer.WriteHeader(http.StatusOK)
}
func (o SearchOutput) WriteEvent(event string, data []byte) error {
	if _, err := fmt.Fprintf(o.Context.Writer, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	o.Context.Writer.Flush()
	return nil
}
func (o SearchOutput) WriteJSON(data []byte) { o.Context.Data(http.StatusOK, "application/json", data) }
func (o SearchOutput) Flush()                { o.Context.Writer.Flush() }
