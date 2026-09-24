package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// ReadReplayableError 保留错误体关闭、回卷及脱敏顺序。
// ReadReplayableError 读取上游错误体并把 resp.Body 回卷为可重读的副本
// （下游 handleXxxErrorResponse 需要再次读取），返回原始错误体与脱敏后的
// 上游错误消息。
func (s *OpenAIResponseOutput) ReadReplayableError(resp *http.Response) ([]byte, string) {
	respBody := s.ReadErrorBody(resp)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(respBody))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	return respBody, upstreamMsg
}
