package httpapi

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	openaiwsv2 "github.com/TokenFlux/TokenRouter/internal/upstream/openai/wsrelay"
	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson" // 以下仅为既有测试保留旧拼装形状，全部同步过滤委托唯一核心实现。
)

type openAIWSPolicyEnforcingFrameConn struct {
	inner       openaiwsv2.FrameConn
	filter      func(coderws.MessageType, []byte) ([]byte, *tierpolicy.BlockedError, error)
	writeFilter func(coderws.MessageType, []byte) ([]byte, error)
	onBlock     func(*tierpolicy.BlockedError)
	once        sync.Once
	core        gatewayws.FrameConn
}

func (c *openAIWSPolicyEnforcingFrameConn) runtime() gatewayws.FrameConn {
	c.once.Do(func() {
		var filter func(int, []byte) ([]byte, *gatewayws.PolicyBlocked, error)
		if c.filter != nil {
			filter = func(typ int, body []byte) ([]byte, *gatewayws.PolicyBlocked, error) {
				out, blocked, err := c.filter(coderws.MessageType(typ), body)
				if blocked == nil {
					return out, nil, err
				}
				return out, &gatewayws.PolicyBlocked{Message: blocked.Message, Cause: blocked}, err
			}
		}
		var writeFilter func(int, []byte) ([]byte, error)
		if c.writeFilter != nil {
			writeFilter = func(typ int, body []byte) ([]byte, error) { return c.writeFilter(coderws.MessageType(typ), body) }
		}
		var block func(*gatewayws.PolicyBlocked)
		if c.onBlock != nil {
			block = func(b *gatewayws.PolicyBlocked) {
				original, ok := b.Cause.(*tierpolicy.BlockedError)
				if !ok {
					original = &tierpolicy.BlockedError{Message: b.Message}
				}
				c.onBlock(original)
			}
		}
		c.core = gatewayws.NewPolicyFrames(openAIWSCoreFrames{c.inner}, filter, writeFilter, block, func(status int, reason string, err error) error {
			return NewOpenAIWSClientCloseError(coderws.StatusCode(status), reason, err)
		}, upstreamopenai.ErrWSConnClosed)
	})
	return c.core
}
func (c *openAIWSPolicyEnforcingFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	if c == nil || c.inner == nil {
		return coderws.MessageText, nil, upstreamopenai.ErrWSConnClosed
	}
	typ, body, err := c.runtime().ReadFrame(ctx)
	return coderws.MessageType(typ), body, err
}
func (c *openAIWSPolicyEnforcingFrameConn) WriteFrame(ctx context.Context, typ coderws.MessageType, body []byte) error {
	if c == nil || c.inner == nil {
		return upstreamopenai.ErrWSConnClosed
	}
	return c.runtime().WriteFrame(ctx, int(typ), body)
}
func (c *openAIWSPolicyEnforcingFrameConn) Close() error {
	if c == nil || c.inner == nil {
		return nil
	}
	return c.runtime().Close()
}

type openAIWSClientFrameConn struct {
	interTurnStarted   chan struct{}
	waitingForNextTurn atomic.Bool
}

func (c *openAIWSClientFrameConn) markTurnStarted() {
	if c != nil {
		gatewayws.TurnActivity{Waiting: &c.waitingForNextTurn, Started: c.interTurnStarted}.MarkStarted()
	}
}
func (c *openAIWSClientFrameConn) markTurnCompleted() {
	if c != nil {
		gatewayws.TurnActivity{Waiting: &c.waitingForNextTurn, Started: c.interTurnStarted}.MarkCompleted()
	}
}

// openAIWSPassthroughPolicyModelForFrame returns the upstream-perspective
// model name that should be passed to evaluateOpenAIFastPolicy for a single
// passthrough WS frame. Mirrors the HTTP-side normalization
// (account.GetMappedModel + normalizeOpenAIModelForUpstream) so the WS path
// matches model whitelists identically.
func openAIWSPassthroughPolicyModelForFrame(account *gatewayprovider.ExecutionAccount, payload []byte) string {
	if account == nil || len(payload) == 0 {
		return ""
	}
	original := strings.TrimSpace(gjson.GetBytes(payload, "model").String())
	if original == "" {
		return ""
	}
	if account.View().IsOpenAIPassthroughEnabled() {
		return original
	}
	return gatewayprovider.ExecutionModelPolicy(account).NormalizeOpenAI(gatewayprovider.ExecutionModelPolicy(account).Mapped(original))
}

// openAIWSPassthroughPolicyModelFromSessionFrame returns the upstream model
// derived from a session.update frame's session.model field. Returns "" when
// the frame is not a session.update event or carries no session.model. Used
// by the per-frame policy filter (client→upstream direction) to keep
// capturedSessionModel in sync with the session-level model the client may
// rotate mid-session.
//
// Realtime / Responses WS lets the client change the session model after
// the WS handshake via:
//
//	{"type":"session.update","session":{"model":"gpt-5.5", ...}}
//
// If we only capture the model from the very first frame, a client can ship
// gpt-4o on the first response.create (whitelisted as pass), then
// session.update to gpt-5.5, then send response.create without "model" so
// the per-frame resolver returns "" and the stale capturedSessionModel falls
// back to gpt-4o — defeating the gpt-5.5 fast-policy filter.
func openAIWSPassthroughPolicyModelFromSessionFrame(account *gatewayprovider.ExecutionAccount, payload []byte) string {
	if account == nil || len(payload) == 0 {
		return ""
	}
	frameType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	if frameType != "session.update" {
		return ""
	}
	original := strings.TrimSpace(gjson.GetBytes(payload, "session.model").String())
	if original == "" {
		return ""
	}
	if account.View().IsOpenAIPassthroughEnabled() {
		return original
	}
	return gatewayprovider.ExecutionModelPolicy(account).NormalizeOpenAI(gatewayprovider.ExecutionModelPolicy(account).Mapped(original))
}

// 旧方法名仅委托原子会话元数据，不保留另一份状态。
type openAIWSPassthroughUsageMeta struct {
	*gatewayws.UsageMeta
	// 测试取得同一原子字段的指针，不复制会话状态。
	reasoningEffort *atomic.Pointer[string]
}

func newOpenAIWSPassthroughUsageMeta(model string, body []byte) *openAIWSPassthroughUsageMeta {
	meta := gatewayws.NewUsageMeta(model, body, gatewayws.RequestUsageDecoder{})
	return &openAIWSPassthroughUsageMeta{UsageMeta: meta, reasoningEffort: &meta.ReasoningEffort}
}
func (m *openAIWSPassthroughUsageMeta) initFromFirstFrame(body []byte, model string) {
	if m != nil {
		m.InitFromFirstFrame(body, model)
	}
}

func (m *openAIWSPassthroughUsageMeta) updateSessionRequestModel(body []byte) {
	if m != nil {
		m.UpdateSessionRequestModel(body)
	}
}

func (m *openAIWSPassthroughUsageMeta) updateFromResponseCreate(body []byte, mapped, requested string) {
	if m != nil {
		m.UpdateFromResponseCreate(body, mapped, requested)
	}
}

// 兼容方法只委托共享的 turn 屏障，不复制状态。
type openAIWSPassthroughTurnLifecycle struct{ *gatewayws.TurnLifecycle }

func newOpenAIWSPassthroughTurnLifecycle(inFlight bool) *openAIWSPassthroughTurnLifecycle {
	return &openAIWSPassthroughTurnLifecycle{gatewayws.NewTurnLifecycle(inFlight)}
}
func (l *openAIWSPassthroughTurnLifecycle) beginResponseCreate(fn func()) bool {
	if l == nil {
		return false
	}
	return l.BeginResponseCreate(fn)
}

func (l *openAIWSPassthroughTurnLifecycle) beginTerminalWrite() {
	if l != nil {
		l.BeginTerminalWrite()
	}
}
func (l *openAIWSPassthroughTurnLifecycle) finishTerminalWrite(ok bool, fn func()) {
	if l != nil {
		l.FinishTerminalWrite(ok, fn)
	}
}

// requestModelForFrame 为竞争测试读取会话的原子模型元数据。
func (m *openAIWSPassthroughUsageMeta) requestModelForFrame(body []byte) string {
	if m == nil {
		return gatewayws.RequestModelForFrame(body)
	}
	return m.RequestModelForFrame(body)
}
