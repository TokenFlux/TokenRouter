package service

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

// openAICodexTurnStateHeader 是 Codex 的回合状态头。上游在响应头中签发该
// 不透明值，客户端会在同一回合的后续请求中原样带回。
const openAICodexTurnStateHeader = "x-codex-turn-state"

// openAICodexTurnStateOrigin 记录最近向一个下游会话签发状态的账号。状态值与
// 上游账号身份绑定；故障转移后继续把旧账号的状态带给新账号会形成矛盾信号。
type openAICodexTurnStateOrigin struct {
	accountID int64
	expiresAt time.Time
}

// openAICodexTurnStateSeed 使用 API Key 与客户端原始会话标识作为追踪键。没有
// 会话标识时不追踪，保持既有透传行为。
func openAICodexTurnStateSeed(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	sessionID := openai.ExtractClientSessionID(c.Request.Header)
	if sessionID == "" {
		return ""
	}
	return strconv.FormatInt(gatewayhttp.APIKeyIDFromContext(c), 10) + "\x00" + sessionID
}

// relayOpenAICodexTurnState 将上游状态显式写回客户端，并只在响应真正会送达
// 客户端时记录签发账号。上游缺失时主动清理可能残留的上一次尝试状态。
func (s *OpenAIGatewayService) relayOpenAICodexTurnState(c *gin.Context, account *gatewayprovider.ExecutionAccount, upstream http.Header) {
	if c == nil || c.Writer == nil {
		return
	}
	canonical := http.CanonicalHeaderKey(openAICodexTurnStateHeader)
	state := extractOpenAICodexTurnState(upstream)
	if state == "" {
		c.Writer.Header().Del(canonical)
		return
	}
	c.Writer.Header().Set(canonical, state)
	s.noteOpenAICodexTurnStateProvenance(c, account)
}

// stageOpenAICodexTurnState 暂存首输出守卫中的状态头。守卫阶段尚可能故障转移，
// 因此这里不能记录签发账号，真正提交时才由对应函数记录。
func stageOpenAICodexTurnState(dst *http.Header, upstream http.Header) {
	if dst == nil {
		return
	}
	canonical := http.CanonicalHeaderKey(openAICodexTurnStateHeader)
	state := extractOpenAICodexTurnState(upstream)
	if state == "" {
		if *dst != nil {
			dst.Del(canonical)
		}
		return
	}
	if *dst == nil {
		*dst = http.Header{}
	}
	dst.Set(canonical, state)
}

// noteStagedOpenAICodexTurnStateCommitted 仅在暂存头实际写给客户端后记录来源，
// 避免被首输出超时丢弃的尝试污染后续回放判断。
func (s *OpenAIGatewayService) noteStagedOpenAICodexTurnStateCommitted(c *gin.Context, account *gatewayprovider.ExecutionAccount, staged http.Header) {
	if staged == nil || strings.TrimSpace(staged.Get(openAICodexTurnStateHeader)) == "" {
		return
	}
	s.noteOpenAICodexTurnStateProvenance(c, account)
}

func extractOpenAICodexTurnState(upstream http.Header) string {
	if upstream == nil {
		return ""
	}
	return strings.TrimSpace(upstream.Get(openAICodexTurnStateHeader))
}

// noteOpenAICodexTurnStateProvenance 记录下游会话与最近签发账号的对应关系。
func (s *OpenAIGatewayService) noteOpenAICodexTurnStateProvenance(c *gin.Context, account *gatewayprovider.ExecutionAccount) {
	if s == nil || account == nil || account.Record.ID <= 0 {
		return
	}
	seed := openAICodexTurnStateSeed(c)
	if seed == "" {
		return
	}
	s.openaiCodexTurnStateOrigins.Store(seed, openAICodexTurnStateOrigin{
		accountID: account.Record.ID,
		expiresAt: time.Now().Add(s.selection.SessionStickyTTL()),
	})
	s.sweepOpenAICodexTurnStateOrigins()
}

// guardOpenAICodexTurnStateEcho 移除已知由其它账号签发的客户端回带值。同账号
// 与未知来源保持不变；服务端绝不自行注入状态，避免改变真实 Codex 回合语义。
func (s *OpenAIGatewayService) guardOpenAICodexTurnStateEcho(c *gin.Context, account *gatewayprovider.ExecutionAccount, h http.Header) {
	if s == nil || h == nil || account == nil {
		return
	}
	if strings.TrimSpace(h.Get(openAICodexTurnStateHeader)) == "" {
		return
	}
	seed := openAICodexTurnStateSeed(c)
	if seed == "" {
		return
	}
	raw, ok := s.openaiCodexTurnStateOrigins.Load(seed)
	if !ok {
		return
	}
	origin, ok := raw.(openAICodexTurnStateOrigin)
	if !ok {
		s.openaiCodexTurnStateOrigins.Delete(seed)
		return
	}
	if !origin.expiresAt.IsZero() && time.Now().After(origin.expiresAt) {
		s.openaiCodexTurnStateOrigins.Delete(seed)
		return
	}
	if origin.accountID != account.Record.ID {
		h.Del(openAICodexTurnStateHeader)
	}
}

// sweepOpenAICodexTurnStateOrigins 每 256 次写入清扫一次过期记录，避免只依赖
// 读侧惰性删除造成会话来源表持续增长。
func (s *OpenAIGatewayService) sweepOpenAICodexTurnStateOrigins() {
	if s.openaiCodexTurnStateWrites.Add(1)%256 != 0 {
		return
	}
	now := time.Now()
	s.openaiCodexTurnStateOrigins.Range(func(key, value any) bool {
		origin, ok := value.(openAICodexTurnStateOrigin)
		if !ok || (!origin.expiresAt.IsZero() && now.After(origin.expiresAt)) {
			s.openaiCodexTurnStateOrigins.Delete(key)
		}
		return true
	})
}
