package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

const CodexTurnStateHeader = "x-codex-turn-state"

// CodexTurnStateHeaders 在原响应提交位置记录来源，状态表由应用独立持有。
// @project-doc docs/interfaces/openai_upstream.md#openai_protocol_dispatch
type CodexTurnStateHeaders struct {
	Origins *session.CodexTurnOrigins
	TTL     func() time.Duration
}

func codexTurnStateSeed(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	id := openai.ExtractClientSessionID(c.Request.Header)
	if id == "" {
		return ""
	}
	return strconv.FormatInt(APIKeyIDFromContext(c), 10) + "\x00" + id
}
func ExtractCodexTurnState(headers http.Header) string {
	if headers == nil {
		return ""
	}
	return strings.TrimSpace(headers.Get(CodexTurnStateHeader))
}

// StageCodexTurnState 不发布来源，仍允许首输出守卫丢弃当前尝试。
func StageCodexTurnState(dst *http.Header, headers http.Header) {
	if dst == nil {
		return
	}
	canonical := http.CanonicalHeaderKey(CodexTurnStateHeader)
	value := ExtractCodexTurnState(headers)
	if value == "" {
		if *dst != nil {
			dst.Del(canonical)
		}
		return
	}
	if *dst == nil {
		*dst = http.Header{}
	}
	dst.Set(canonical, value)
}
func (s *CodexTurnStateHeaders) note(c *gin.Context, target *provider.ExecutionAccount) {
	if s == nil || target == nil || target.Record.ID <= 0 {
		return
	}
	seed := codexTurnStateSeed(c)
	if seed == "" {
		return
	}
	ttl := time.Hour
	if s.TTL != nil {
		ttl = s.TTL()
	}
	s.Origins.Record(seed, target.Record.ID, ttl)
}
func (s *CodexTurnStateHeaders) Relay(c *gin.Context, target *provider.ExecutionAccount, headers http.Header) {
	if c == nil || c.Writer == nil {
		return
	}
	canonical := http.CanonicalHeaderKey(CodexTurnStateHeader)
	value := ExtractCodexTurnState(headers)
	if value == "" {
		c.Writer.Header().Del(canonical)
		return
	}
	c.Writer.Header().Set(canonical, value)
	s.note(c, target)
}
func (s *CodexTurnStateHeaders) Commit(c *gin.Context, target *provider.ExecutionAccount, headers http.Header) {
	if headers == nil || strings.TrimSpace(headers.Get(CodexTurnStateHeader)) == "" {
		return
	}
	s.note(c, target)
}

// Guard 只删除已知来自另一账号的状态，未知、同账号与过期记录不阻止回放。
func (s *CodexTurnStateHeaders) Guard(c *gin.Context, target *provider.ExecutionAccount, headers http.Header) {
	if s == nil || headers == nil || target == nil || strings.TrimSpace(headers.Get(CodexTurnStateHeader)) == "" {
		return
	}
	seed := codexTurnStateSeed(c)
	if seed == "" {
		return
	}
	if id, ok := s.Origins.Owner(seed); ok && id != target.Record.ID {
		headers.Del(CodexTurnStateHeader)
	}
}
