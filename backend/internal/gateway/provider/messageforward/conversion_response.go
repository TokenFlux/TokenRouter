package messageforward

import (
	"bufio"
	"crypto/rand"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"net/http"
	"time"
)

// forwardResponse 保留每条转换流的扫描器、64KiB 初始缓冲与既有大小限制。
func (s *Runtime) forwardResponse(resp *http.Response) forwardcore.Response {
	maxLineSize := 500 * 1024 * 1024
	if s.options.MaxLineSize > 0 {
		maxLineSize = s.options.MaxLineSize
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	return forwardcore.Response{
		StatusCode: resp.StatusCode,
		Close:      func() { _ = resp.Body.Close() },
		Runtime:    bridge.Runtime{Now: time.Now, ReadRandom: rand.Read},
		RequestID:  resp.Header.Get("x-request-id"),
		Headers:    resp.Header,
		Lines:      scanner,
	}
}
