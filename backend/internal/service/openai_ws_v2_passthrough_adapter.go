package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
	"github.com/BrandonVee/TokenRouter/internal/pkg/openai"
	openaiwsv2 "github.com/BrandonVee/TokenRouter/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type openAIWSClientFrameConn struct {
	conn                 *coderws.Conn
	controlCtx           context.Context
	interTurnIdleTimeout time.Duration
	interTurnStarted     chan struct{}
	waitingForNextTurn   atomic.Bool
}

// openAIWSPolicyEnforcingFrameConn wraps a client-side FrameConn and runs
// every client→upstream frame through the OpenAI Fast Policy. It is the
// passthrough-relay equivalent of the parseClientPayload integration in the
// ingress session path. filter returns:
//   - newPayload, nil, nil: forward the (possibly mutated) payload
//   - _, *OpenAIFastBlockedError, nil: block — the wrapper sends an error
//     event via onBlock and surfaces a transport-level error so the relay
//     stops reading from the client.
//   - _, _, err: a transport error other than block.
type openAIWSPolicyEnforcingFrameConn struct {
	inner       openaiwsv2.FrameConn
	filter      func(msgType coderws.MessageType, payload []byte) ([]byte, *OpenAIFastBlockedError, error)
	writeFilter func(msgType coderws.MessageType, payload []byte) ([]byte, error)
	onBlock     func(blocked *OpenAIFastBlockedError)
}

var _ openaiwsv2.FrameConn = (*openAIWSPolicyEnforcingFrameConn)(nil)

type openAIWSTurnPayload struct {
	RequestBody        []byte
	OriginalModel      string
	RoutingModel       string
	UpstreamModel      string
	ServiceTier        *string
	ReasoningEffort    *string
	PreviousResponseID string
	Source             string
}

type openAIWSTurnPayloadQueue struct {
	mu    sync.Mutex
	items []openAIWSTurnPayload
}

func newOpenAIWSTurnPayloadQueue() *openAIWSTurnPayloadQueue {
	return &openAIWSTurnPayloadQueue{}
}

func (q *openAIWSTurnPayloadQueue) Push(item openAIWSTurnPayload) {
	if q == nil {
		return
	}
	item.RequestBody = append([]byte(nil), item.RequestBody...)
	if item.ServiceTier != nil {
		value := *item.ServiceTier
		item.ServiceTier = &value
	}
	if item.ReasoningEffort != nil {
		value := *item.ReasoningEffort
		item.ReasoningEffort = &value
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, item)
}

// Len 返回尚未收到终态事件的 turn 数量。
func (q *openAIWSTurnPayloadQueue) Len() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Peek 返回当前 turn 的请求上下文但不出队，用于上游错误事件先于 turn 完成时复用同一模型。
func (q *openAIWSTurnPayloadQueue) Peek() openAIWSTurnPayload {
	if q == nil {
		return openAIWSTurnPayload{Source: "passthrough_missing"}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return openAIWSTurnPayload{Source: "passthrough_missing"}
	}
	return q.items[0]
}

func (q *openAIWSTurnPayloadQueue) Pop() openAIWSTurnPayload {
	if q == nil {
		return openAIWSTurnPayload{Source: "passthrough_missing"}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return openAIWSTurnPayload{Source: "passthrough_missing"}
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item
}

func (c *openAIWSPolicyEnforcingFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	if c == nil || c.inner == nil {
		return coderws.MessageText, nil, errOpenAIWSConnClosed
	}
	msgType, payload, err := c.inner.ReadFrame(ctx)
	if err != nil {
		return msgType, payload, err
	}
	if c.filter == nil {
		return msgType, payload, nil
	}
	updated, blocked, filterErr := c.filter(msgType, payload)
	if filterErr != nil {
		return msgType, payload, filterErr
	}
	if blocked != nil {
		if c.onBlock != nil {
			c.onBlock(blocked)
		}
		return msgType, nil, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, blocked.Message, blocked)
	}
	return msgType, updated, nil
}

func (c *openAIWSPolicyEnforcingFrameConn) WriteFrame(ctx context.Context, msgType coderws.MessageType, payload []byte) error {
	if c == nil || c.inner == nil {
		return errOpenAIWSConnClosed
	}
	if c.writeFilter != nil {
		updated, err := c.writeFilter(msgType, payload)
		if err != nil {
			return err
		}
		payload = updated
	}
	return c.inner.WriteFrame(ctx, msgType, payload)
}

func (c *openAIWSPolicyEnforcingFrameConn) Close() error {
	if c == nil || c.inner == nil {
		return nil
	}
	return c.inner.Close()
}

// openAIWSPassthroughPolicyModelForFrame returns the upstream-perspective
// model name that should be passed to evaluateOpenAIFastPolicy for a single
// passthrough WS frame. Mirrors the HTTP-side normalization
// (account.GetMappedModel + normalizeOpenAIModelForUpstream) so the WS path
// matches model whitelists identically.
func openAIWSPassthroughPolicyModelForFrame(account *Account, payload []byte) string {
	if account == nil || len(payload) == 0 {
		return ""
	}
	original := strings.TrimSpace(gjson.GetBytes(payload, "model").String())
	if original == "" {
		return ""
	}
	return normalizeOpenAIModelForUpstream(account, account.GetMappedModel(original))
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
func openAIWSPassthroughPolicyModelFromSessionFrame(account *Account, payload []byte) string {
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
	return normalizeOpenAIModelForUpstream(account, account.GetMappedModel(original))
}

type openAIWSPassthroughUsageMeta struct {
	serviceTier     atomic.Pointer[string]
	reasoningEffort atomic.Pointer[string]

	// 仅在 client->upstream filter goroutine 中读写；Load 侧通过上方原子指针同步。
	sessionRequestModel string
}

func newOpenAIWSPassthroughUsageMeta(initialRequestModel string, firstFrame []byte) *openAIWSPassthroughUsageMeta {
	meta := &openAIWSPassthroughUsageMeta{
		sessionRequestModel: strings.TrimSpace(initialRequestModel),
	}
	if meta.sessionRequestModel == "" {
		meta.sessionRequestModel = openAIWSPassthroughRequestModelForFrame(firstFrame)
	}
	return meta
}

func (m *openAIWSPassthroughUsageMeta) initFromFirstFrame(policyOutput []byte, mappedModel string) {
	if m == nil {
		return
	}
	m.serviceTier.Store(extractOpenAIServiceTierFromBody(policyOutput))
	m.reasoningEffort.Store(extractOpenAIReasoningEffortFromBody(policyOutput, mappedModel, m.sessionRequestModel))
}

func (m *openAIWSPassthroughUsageMeta) updateSessionRequestModel(payload []byte) {
	if m == nil {
		return
	}
	if model := openAIWSPassthroughRequestModelFromSessionFrame(payload); model != "" {
		m.sessionRequestModel = model
	}
}

func (m *openAIWSPassthroughUsageMeta) requestModelForFrame(payload []byte) string {
	if m == nil {
		return openAIWSPassthroughRequestModelForFrame(payload)
	}
	if model := openAIWSPassthroughRequestModelForFrame(payload); model != "" {
		return model
	}
	return m.sessionRequestModel
}

func (m *openAIWSPassthroughUsageMeta) updateFromResponseCreate(policyOutput []byte, mappedModel string, requestModelForFrame string) {
	if m == nil {
		return
	}
	m.serviceTier.Store(extractOpenAIServiceTierFromBody(policyOutput))
	m.reasoningEffort.Store(extractOpenAIReasoningEffortFromBody(policyOutput, mappedModel, requestModelForFrame))
}

func openAIWSPassthroughRequestModelForFrame(payload []byte) string {
	if len(payload) == 0 || strings.TrimSpace(gjson.GetBytes(payload, "type").String()) != "response.create" {
		return ""
	}
	return strings.TrimSpace(gjson.GetBytes(payload, "model").String())
}

func openAIWSPassthroughRequestModelFromSessionFrame(payload []byte) string {
	if len(payload) == 0 || strings.TrimSpace(gjson.GetBytes(payload, "type").String()) != "session.update" {
		return ""
	}
	return strings.TrimSpace(gjson.GetBytes(payload, "session.model").String())
}

// replaceOpenAIWSPassthroughRequestModel 把已解析的最终模型 U 写入待转发帧。
// session.update 使用嵌套字段，其余 response.create 使用顶层 model。
func replaceOpenAIWSPassthroughRequestModel(payload []byte, eventType string, upstreamModel string) ([]byte, error) {
	path := "model"
	if eventType == "session.update" {
		path = "session.model"
	}
	updated, err := sjson.SetBytes(payload, path, upstreamModel)
	if err != nil {
		return nil, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", err)
	}
	return updated, nil
}

const openaiWSV2PassthroughModeFields = "ws_mode=passthrough ws_router=v2"

var errOpenAIWSPassthroughFirstOutputTimeout = errors.New("openai websocket passthrough first output timeout")
var errOpenAIWSPassthroughActiveTurnTimeout = errors.New("openai websocket passthrough active turn read timeout")

type openAIWSPassthroughDeadlinePhase uint8

const (
	openAIWSPassthroughDeadlinePhaseFirstSemantic openAIWSPassthroughDeadlinePhase = iota + 1
	openAIWSPassthroughDeadlinePhaseActiveRead
)

type openAIWSPassthroughFirstOutputDeadline struct {
	timeout         time.Duration
	startedAt       time.Time
	requestModel    string
	reasoningEffort string
	phase           openAIWSPassthroughDeadlinePhase
}

type openAIWSPassthroughFirstOutputTimeoutError struct {
	deadline openAIWSPassthroughFirstOutputDeadline
}

func (e *openAIWSPassthroughFirstOutputTimeoutError) Error() string {
	return errOpenAIWSPassthroughFirstOutputTimeout.Error()
}

func (e *openAIWSPassthroughFirstOutputTimeoutError) Unwrap() error {
	return errOpenAIWSPassthroughFirstOutputTimeout
}

type openAIWSPassthroughActiveTurnTimeoutError struct{}

func (e *openAIWSPassthroughActiveTurnTimeoutError) Error() string {
	return errOpenAIWSPassthroughActiveTurnTimeout.Error()
}

func (e *openAIWSPassthroughActiveTurnTimeoutError) Unwrap() error {
	return errOpenAIWSPassthroughActiveTurnTimeout
}

type openAIWSPassthroughFirstOutputDeadlineState struct {
	armed      bool
	generation uint64
	deadline   openAIWSPassthroughFirstOutputDeadline
}

type openAIWSPassthroughTurnLifecycle struct {
	mu       sync.Mutex
	inFlight bool
}

func newOpenAIWSPassthroughTurnLifecycle(inFlight bool) *openAIWSPassthroughTurnLifecycle {
	return &openAIWSPassthroughTurnLifecycle{inFlight: inFlight}
}

func (l *openAIWSPassthroughTurnLifecycle) beginResponseCreate(onAccepted func()) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight {
		return false
	}
	l.inFlight = true
	if onAccepted != nil {
		onAccepted()
	}
	return true
}

func (l *openAIWSPassthroughTurnLifecycle) cancelResponseCreate() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.inFlight = false
	l.mu.Unlock()
}

func (l *openAIWSPassthroughTurnLifecycle) beginTerminalWrite() {
	if l != nil {
		l.mu.Lock()
	}
}

func (l *openAIWSPassthroughTurnLifecycle) finishTerminalWrite(succeeded bool, onSucceeded func()) {
	if l == nil {
		return
	}
	if succeeded {
		if onSucceeded != nil {
			onSucceeded()
		}
		l.inFlight = false
	}
	l.mu.Unlock()
}

type openAIWSPassthroughFirstOutputFrameConn struct {
	inner             openaiwsv2.FrameConn
	resolveDeadline   func(payload []byte) openAIWSPassthroughFirstOutputDeadline
	activeReadTimeout time.Duration

	mu              sync.Mutex
	state           openAIWSPassthroughFirstOutputDeadlineState
	deadlineChanged chan struct{}
}

func (c *openAIWSPassthroughFirstOutputFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	if c == nil || c.inner == nil {
		return coderws.MessageText, nil, errOpenAIWSConnClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}

	type readResult struct {
		msgType coderws.MessageType
		payload []byte
		err     error
	}
	readCtx, cancelRead := context.WithCancel(ctx)
	readResultCh := make(chan readResult, 1)
	go func() {
		msgType, payload, err := c.inner.ReadFrame(readCtx)
		readResultCh <- readResult{msgType: msgType, payload: payload, err: err}
	}()

	var timer *time.Timer
	var timerCh <-chan time.Time
	resetTimer := func() {
		state := c.deadlineState()
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		if !state.armed || state.deadline.timeout <= 0 {
			timerCh = nil
			return
		}
		remaining := time.Until(state.deadline.startedAt.Add(state.deadline.timeout))
		if remaining < 0 {
			remaining = 0
		}
		if timer == nil {
			timer = time.NewTimer(remaining)
		} else {
			timer.Reset(remaining)
		}
		timerCh = timer.C
	}
	resetTimer()

	defer func() {
		cancelRead()
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case result := <-readResultCh:
			if result.err == nil {
				c.observeUpstreamActivity(result.msgType, result.payload)
			}
			return result.msgType, result.payload, result.err
		case <-c.deadlineChanged:
			resetTimer()
		case <-timerCh:
			state := c.deadlineState()
			if !state.armed || state.deadline.timeout <= 0 || time.Now().Before(state.deadline.startedAt.Add(state.deadline.timeout)) {
				resetTimer()
				continue
			}
			if ctx.Err() != nil {
				cancelRead()
				<-readResultCh
				return coderws.MessageText, nil, ctx.Err()
			}
			cancelRead()
			<-readResultCh
			if state.deadline.phase == openAIWSPassthroughDeadlinePhaseActiveRead {
				return coderws.MessageText, nil, &openAIWSPassthroughActiveTurnTimeoutError{}
			}
			return coderws.MessageText, nil, &openAIWSPassthroughFirstOutputTimeoutError{deadline: state.deadline}
		case <-ctx.Done():
			cancelRead()
			<-readResultCh
			return coderws.MessageText, nil, ctx.Err()
		}
	}
}

func (c *openAIWSPassthroughFirstOutputFrameConn) WriteFrame(ctx context.Context, msgType coderws.MessageType, payload []byte) error {
	if c == nil || c.inner == nil {
		return errOpenAIWSConnClosed
	}
	generation := uint64(0)
	if msgType == coderws.MessageText && strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "response.create" {
		generation = c.armDeadline(payload)
	}
	if err := c.inner.WriteFrame(ctx, msgType, payload); err != nil {
		c.disarmDeadline(generation)
		return err
	}
	return nil
}

func (c *openAIWSPassthroughFirstOutputFrameConn) Close() error {
	if c == nil || c.inner == nil {
		return nil
	}
	return c.inner.Close()
}

func (c *openAIWSPassthroughFirstOutputFrameConn) armDeadline(payload []byte) uint64 {
	if c == nil || c.resolveDeadline == nil {
		return 0
	}
	deadline := c.resolveDeadline(payload)
	if deadline.timeout <= 0 {
		return 0
	}
	if deadline.startedAt.IsZero() {
		deadline.startedAt = time.Now()
	}
	deadline.phase = openAIWSPassthroughDeadlinePhaseFirstSemantic
	c.mu.Lock()
	c.state.generation++
	generation := c.state.generation
	c.state.armed = true
	c.state.deadline = deadline
	c.mu.Unlock()
	c.notifyDeadlineChanged()
	return generation
}

func (c *openAIWSPassthroughFirstOutputFrameConn) observeUpstreamActivity(msgType coderws.MessageType, payload []byte) {
	if c == nil {
		return
	}
	if msgType == coderws.MessageText && openAIWSPassthroughIsTerminalOutput(payload) {
		c.disarmDeadline(0)
		return
	}
	state := c.deadlineState()
	if state.armed && state.deadline.phase == openAIWSPassthroughDeadlinePhaseActiveRead {
		c.armActiveReadDeadline()
		return
	}
	if msgType == coderws.MessageText && openAIWSPassthroughStartsSemanticOutput(payload) {
		c.armActiveReadDeadline()
	}
}

func (c *openAIWSPassthroughFirstOutputFrameConn) armActiveReadDeadline() {
	if c == nil {
		return
	}
	if c.activeReadTimeout <= 0 {
		c.disarmDeadline(0)
		return
	}
	c.mu.Lock()
	c.state.generation++
	c.state.armed = true
	c.state.deadline = openAIWSPassthroughFirstOutputDeadline{
		timeout:   c.activeReadTimeout,
		startedAt: time.Now(),
		phase:     openAIWSPassthroughDeadlinePhaseActiveRead,
	}
	c.mu.Unlock()
	c.notifyDeadlineChanged()
}

func (c *openAIWSPassthroughFirstOutputFrameConn) disarmDeadline(generation uint64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if !c.state.armed || (generation != 0 && generation != c.state.generation) {
		c.mu.Unlock()
		return
	}
	c.state.armed = false
	c.mu.Unlock()
	c.notifyDeadlineChanged()
}

func (c *openAIWSPassthroughFirstOutputFrameConn) deadlineState() openAIWSPassthroughFirstOutputDeadlineState {
	if c == nil {
		return openAIWSPassthroughFirstOutputDeadlineState{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *openAIWSPassthroughFirstOutputFrameConn) notifyDeadlineChanged() {
	if c == nil || c.deadlineChanged == nil {
		return
	}
	select {
	case c.deadlineChanged <- struct{}{}:
	default:
	}
}

func openAIWSPassthroughStartsSemanticOutput(payload []byte) bool {
	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	switch eventType {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		return true
	case "", "response.created", "response.in_progress", "response.output_item.added", "response.output_item.done":
		return false
	}
	return strings.Contains(eventType, ".delta") ||
		strings.HasPrefix(eventType, "response.output_text") ||
		strings.HasPrefix(eventType, "response.output")
}

func openAIWSPassthroughIsTerminalOutput(payload []byte) bool {
	switch strings.TrimSpace(gjson.GetBytes(payload, "type").String()) {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		return true
	default:
		return false
	}
}

var _ openaiwsv2.FrameConn = (*openAIWSClientFrameConn)(nil)
var _ openaiwsv2.FrameConn = (*openAIWSPassthroughFirstOutputFrameConn)(nil)

func (c *openAIWSClientFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	if c == nil || c.conn == nil {
		return coderws.MessageText, nil, errOpenAIWSConnClosed
	}
	controlCtx := ctx
	if c.controlCtx != nil {
		controlCtx = c.controlCtx
	}
	msgType, payload, err := readOpenAIWSClientMessageWithTimeoutStart(
		controlCtx,
		c.conn,
		c.interTurnIdleTimeout,
		coderws.StatusNormalClosure,
		"websocket idle timeout",
		c.interTurnStarted,
		func() bool { return c.waitingForNextTurn.Load() },
	)
	return msgType, payload, err
}

func (c *openAIWSClientFrameConn) markTurnStarted() {
	if c != nil {
		c.waitingForNextTurn.Store(false)
	}
}

func (c *openAIWSClientFrameConn) markTurnCompleted() {
	if c == nil {
		return
	}
	c.waitingForNextTurn.Store(true)
	select {
	case c.interTurnStarted <- struct{}{}:
	default:
	}
}

func (c *openAIWSClientFrameConn) WriteFrame(ctx context.Context, msgType coderws.MessageType, payload []byte) error {
	if c == nil || c.conn == nil {
		return errOpenAIWSConnClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if msgType == coderws.MessageText {
		if normalized, changed := normalizeCompletedImageGenerationStatus(payload); changed {
			payload = normalized
		}
	}
	// 控制面取消必须由读路径发送带原因的关闭帧；若直接继承父取消，coder/websocket
	// 可能在当前帧已经到达客户端但 Write 尚未返回时硬关 TCP。这里保留原 deadline，
	// 仅让已经开始的写入完成，再由关闭握手统一结束连接。
	writeCtx := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		writeCtx, cancel = context.WithDeadline(writeCtx, deadline)
		defer cancel()
	}
	return c.conn.Write(writeCtx, msgType, payload)
}

func (c *openAIWSClientFrameConn) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	_ = c.conn.Close(coderws.StatusNormalClosure, "")
	_ = c.conn.CloseNow()
	return nil
}

func (s *OpenAIGatewayService) proxyResponsesWebSocketV2Passthrough(
	ctx context.Context,
	c *gin.Context,
	clientConn *coderws.Conn,
	account *Account,
	token string,
	firstClientMessage []byte,
	hooks *OpenAIWSIngressHooks,
	wsDecision OpenAIWSProtocolDecision,
	tlsRouterMatch TLSFingerprintRouterMatchResult,
) error {
	if s == nil {
		return errors.New("service is nil")
	}
	if clientConn == nil {
		return errors.New("client websocket is nil")
	}
	if account == nil {
		return errors.New("account is nil")
	}
	if err := validateOpenAIWSBearerToken(account, token); err != nil {
		return err
	}
	if isOpenAIResponsesLiteWebSocketPayload(firstClientMessage) {
		liteFirstMessage, _, liteErr := normalizeOpenAIResponsesLitePayloadForAccount(account, firstClientMessage)
		if liteErr != nil {
			return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, liteErr.Error(), liteErr)
		}
		firstClientMessage = liteFirstMessage
	}
	if hooks != nil && (hooks.MaxReasoningEffort != "" || len(hooks.ReasoningEffortMappings) > 0) {
		if capped, changed := ApplyOpenAIReasoningEffortPolicy(firstClientMessage, hooks.MaxReasoningEffort, hooks.ReasoningEffortMappings); changed {
			firstClientMessage = capped
		}
	}
	requestModel := strings.TrimSpace(gjson.GetBytes(firstClientMessage, "model").String())
	requestPreviousResponseID := strings.TrimSpace(gjson.GetBytes(firstClientMessage, "previous_response_id").String())
	initialRequestModel := ""
	if hooks != nil {
		initialRequestModel = hooks.InitialRequestModel
	}
	// usage 元数据必须在改写为 U 前捕获客户端模型 R。
	usageMeta := newOpenAIWSPassthroughUsageMeta(initialRequestModel, firstClientMessage)
	logOpenAIWSV2Passthrough(
		"relay_start account_id=%d model=%s previous_response_id=%s first_message_type=%s first_message_bytes=%d",
		account.ID,
		truncateOpenAIWSLogValue(requestModel, openAIWSLogValueMaxLen),
		truncateOpenAIWSLogValue(requestPreviousResponseID, openAIWSIDValueMaxLen),
		openaiwsv2RelayMessageTypeName(coderws.MessageText),
		len(firstClientMessage),
	)

	// 在首个 response.create 帧上应用 OpenAI Fast Policy。后续帧会通过下方的
	// FrameConn 包装器过滤，确保每个 client -> upstream 帧都经过与 HTTP 入口相同的
	// 策略评估、归一化和 scope 处理。
	//
	// 这里从首帧分别捕获渠道模型 C 和最终模型 U，供后续省略 model 的帧回退使用。
	// Realtime 客户端允许发送不重复声明 model 的 response.create，此时上游会使用
	// session.update 协商得到的 model。没有这个 fallback 时，空 model 会绕过管理员
	// 配置的模型白名单并被静默透传，导致首帧之后的每一帧都无法命中该策略。
	firstRoutingModel, firstUpstreamModel, resolveModelErr := resolveOpenAIWSTurnModels(account, hooks, 1, requestModel, firstClientMessage)
	if resolveModelErr != nil {
		return resolveModelErr
	}
	// passthrough 的上下游 relay 分属两个 goroutine，逐轮模型快照必须原子发布。
	var capturedSessionRequestedModel atomic.Pointer[string]
	var capturedSessionRoutingModel atomic.Pointer[string]
	var capturedSessionUpstreamModel atomic.Pointer[string]
	storeCapturedSessionModels := func(requestedModel string, routingModel string, upstreamModel string) {
		requestedCopy := requestedModel
		routingCopy := routingModel
		upstreamCopy := upstreamModel
		capturedSessionRequestedModel.Store(&requestedCopy)
		capturedSessionRoutingModel.Store(&routingCopy)
		capturedSessionUpstreamModel.Store(&upstreamCopy)
	}
	loadCapturedModel := func(model *atomic.Pointer[string]) string {
		if value := model.Load(); value != nil {
			return *value
		}
		return ""
	}
	storeCapturedSessionModels(requestModel, firstRoutingModel, firstUpstreamModel)

	firstClientMessage, resolveModelErr = replaceOpenAIWSPassthroughRequestModel(firstClientMessage, "response.create", firstUpstreamModel)
	if resolveModelErr != nil {
		return resolveModelErr
	}
	firstPolicyCtx := openAIWSFastModePolicyContext(ctx, hooks, 1)
	updatedFirst, blocked, policyErr := s.applyOpenAIFastPolicyToWSResponseCreate(firstPolicyCtx, account, firstUpstreamModel, firstClientMessage)
	if policyErr != nil {
		return fmt.Errorf("apply openai fast policy on first ws frame: %w", policyErr)
	}
	if blocked != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		// coder/websocket@v1.8.14 Conn.Write is synchronous: it acquires
		// writeFrameMu, writes the entire frame, and Flushes the underlying
		// bufio writer before returning (write.go:42 → write.go:307-311).
		// The subsequent close handshake re-acquires the same writeFrameMu
		// to send the close frame, so the error event is guaranteed to
		// reach the kernel send buffer before any close frame is queued.
		// No explicit flush hop is required here.
		eventBytes := buildOpenAIFastPolicyBlockedWSEvent(blocked)
		if eventBytes != nil {
			writeCtx, cancelWrite := context.WithTimeout(ctx, s.openAIWSWriteTimeout())
			_ = clientConn.Write(writeCtx, coderws.MessageText, eventBytes)
			cancelWrite()
		}
		return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, blocked.Message, blocked)
	}
	firstClientMessage = updatedFirst

	// 在 policy filter 之后再提取 service_tier / reasoning_effort 用于
	// usage 上报：filter
	// 命中时 service_tier 已经从 firstClientMessage 中删除，billing 应当
	// 反映上游实际处理的 tier（nil = default），而不是用户最初请求的
	// "priority"。HTTP 入口（line ~2728 extractOpenAIServiceTier(reqBody)）
	// 与 WS ingress（openai_ws_forwarder.go:2991 取自 payload）的语义一致。
	//
	// 多轮 passthrough：OpenAI Realtime / Responses WS 协议允许客户端在
	// 同一连接的不同 response.create 帧上发送不同 service_tier（参考
	// codex-rs/core/src/client.rs build_responses_request 每次重新填值）。
	// filter 会把每轮值固化到 turn 队列；原子值仅保存最新会话状态，供缺失
	// turn 快照的异常和最终汇总路径兜底使用。
	usageMeta.initFromFirstFrame(firstClientMessage, firstUpstreamModel)
	promptCacheKey := strings.TrimSpace(gjson.GetBytes(firstClientMessage, "prompt_cache_key").String())
	turnPayloads := newOpenAIWSTurnPayloadQueue()
	turnPayloads.Push(openAIWSTurnPayload{
		RequestBody:        firstClientMessage,
		OriginalModel:      requestModel,
		RoutingModel:       firstRoutingModel,
		UpstreamModel:      firstUpstreamModel,
		ServiceTier:        usageMeta.serviceTier.Load(),
		ReasoningEffort:    usageMeta.reasoningEffort.Load(),
		PreviousResponseID: requestPreviousResponseID,
		Source:             "passthrough",
	})

	wsURL, err := s.buildOpenAIResponsesWSURL(account)
	if err != nil {
		return fmt.Errorf("build ws url: %w", err)
	}
	wsHost := "-"
	wsPath := "-"
	if parsedURL, parseErr := url.Parse(wsURL); parseErr == nil && parsedURL != nil {
		wsHost = normalizeOpenAIWSLogValue(parsedURL.Host)
		wsPath = normalizeOpenAIWSLogValue(parsedURL.Path)
	}
	logOpenAIWSV2Passthrough(
		"relay_dial_start account_id=%d ws_host=%s ws_path=%s proxy_enabled=%v",
		account.ID,
		wsHost,
		wsPath,
		account.ProxyID != nil && account.Proxy != nil,
	)

	isCodexCLI := false
	if c != nil {
		isCodexCLI = openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator"))
	}
	if s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
		isCodexCLI = true
	}
	turnState := ""
	turnMetadata := ""
	if c != nil {
		turnState = strings.TrimSpace(c.GetHeader(openAIWSTurnStateHeader))
		turnMetadata = strings.TrimSpace(c.GetHeader(openAIWSTurnMetadataHeader))
	}
	headers, _, buildHdrErr := s.buildOpenAIWSHeaders(
		ctx,
		c,
		account,
		token,
		wsDecision,
		isCodexCLI,
		turnState,
		turnMetadata,
		promptCacheKey,
		gjson.GetBytes(firstClientMessage, "model").String(),
		gjson.GetBytes(firstClientMessage, "service_tier").String(),
		tlsRouterMatch,
	)
	if buildHdrErr != nil {
		return fmt.Errorf("build ws headers: %w", buildHdrErr)
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	dialer := s.getOpenAIWSPassthroughDialer()
	if dialer == nil {
		return errors.New("openai ws passthrough dialer is nil")
	}

	agentTaskRecoveryTried := false
	var upstreamConn openAIWSClientConn
	statusCode := 0
	var handshakeHeaders http.Header
	for {
		headers, err = s.refreshOpenAIAgentIdentityHeaders(ctx, account, headers)
		if err != nil {
			return fmt.Errorf("refresh ws authentication headers: %w", err)
		}
		dialCtx, cancelDial := context.WithTimeout(ctx, s.openAIWSDialTimeout())
		upstreamConn, statusCode, handshakeHeaders, err = dialer.Dial(dialCtx, wsURL, headers, proxyURL, s.resolveOpenAITLSProfile(account, tlsRouterMatch))
		cancelDial()
		if err == nil {
			break
		}
		var handshakeErr *openAIWSHandshakeError
		responseBody := []byte(nil)
		if errors.As(err, &handshakeErr) && handshakeErr != nil {
			responseBody = handshakeErr.Body
		}
		dialErr := &openAIWSDialError{StatusCode: statusCode, ResponseHeaders: cloneHeader(handshakeHeaders), ResponseBody: responseBody, Err: err}
		if s.isAgentIdentityAccount(ctx, account) && isAgentIdentityTaskInvalidWSDialError(dialErr) && !agentTaskRecoveryTried {
			agentTaskRecoveryTried = true
			if recoveryErr := s.recoverAgentIdentityTask(ctx, account, account.GetCredential("task_id")); recoveryErr != nil {
				return fmt.Errorf("agent identity task recovery failed: %w", recoveryErr)
			}
			continue
		}
		logOpenAIWSV2Passthrough(
			"relay_dial_failed account_id=%d status_code=%d err=%s",
			account.ID,
			statusCode,
			truncateOpenAIWSLogValue(err.Error(), openAIWSLogValueMaxLen),
		)
		errorDecision := s.handleOpenAIWSDialTransientFailure(ctx, account, loadCapturedModel(&capturedSessionRoutingModel), dialErr)
		if statusCode != 0 && errorDecision.ShouldReturnGenericError() {
			return openAIWSGenericPolicyCloseError(statusCode)
		}
		if statusCode != 0 && errorDecision.ShouldFailoverWithDefaults(
			account,
			statusCode,
			statusCode == http.StatusTooManyRequests,
			s.shouldFailoverOpenAIWSError(account, statusCode, responseBody),
		) {
			return newOpenAIUpstreamFailoverError(
				statusCode,
				handshakeHeaders,
				responseBody,
				extractUpstreamErrorMessage(responseBody),
				errorDecision.RetryableOnSameAccount(account, statusCode),
			)
		}
		return s.mapOpenAIWSPassthroughDialError(err, statusCode, handshakeHeaders)
	}
	defer func() {
		_ = upstreamConn.Close()
	}()
	logOpenAIWSV2Passthrough(
		"relay_dial_ok account_id=%d status_code=%d upstream_request_id=%s",
		account.ID,
		statusCode,
		openAIWSHeaderValueForLog(handshakeHeaders, "x-request-id"),
	)

	upstreamFrameConn, ok := upstreamConn.(openaiwsv2.FrameConn)
	if !ok {
		return errors.New("openai ws passthrough upstream connection does not support frame relay")
	}
	relayUpstreamFrameConn := &openAIWSPassthroughFirstOutputFrameConn{
		inner:             upstreamFrameConn,
		activeReadTimeout: s.openAIWSPassthroughIdleTimeout(),
		deadlineChanged:   make(chan struct{}, 1),
		resolveDeadline: func(payload []byte) openAIWSPassthroughFirstOutputDeadline {
			reasoningEffort := ""
			if current := usageMeta.reasoningEffort.Load(); current != nil {
				reasoningEffort = *current
			}
			timeout := s.openAIFirstOutputTimeout(reasoningEffort)
			if timeout <= 0 {
				timeout = s.openAIWSPassthroughIdleTimeout()
			}
			model := openAIWSPassthroughRequestModelForFrame(payload)
			if model == "" {
				model = usageMeta.requestModelForFrame(payload)
			}
			if model == "" {
				model = requestModel
			}
			return openAIWSPassthroughFirstOutputDeadline{
				timeout:         timeout,
				startedAt:       time.Now(),
				requestModel:    model,
				reasoningEffort: reasoningEffort,
			}
		},
	}

	completedTurns := atomic.Int32{}
	var terminalWritePayload atomic.Pointer[openAIWSTurnPayload]
	turnLifecycle := newOpenAIWSPassthroughTurnLifecycle(true)
	clientFrameConn := &openAIWSClientFrameConn{
		conn:                 clientConn,
		controlCtx:           ctx,
		interTurnIdleTimeout: s.openAIWSIngressInterTurnIdleTimeout(),
		interTurnStarted:     make(chan struct{}, 1),
	}
	policyClientConn := &openAIWSPolicyEnforcingFrameConn{
		inner: clientFrameConn,
		// filter 仅在 runClientToUpstream 这一条 goroutine 中执行；
		// 会话模型还会被上游回调读取，因此通过上方原子快照同步。
		filter: func(msgType coderws.MessageType, payload []byte) (out []byte, blocked *OpenAIFastBlockedError, filterErr error) {
			if msgType != coderws.MessageText {
				return payload, nil, nil
			}
			// 后续 response.create 帧在策略过滤和上游转发前执行同一套用户提示词替换。
			payload = s.ApplyUserPromptReplacement(ctx, payload, "openai_responses")
			eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
			isResponseCreate := eventType == "response.create"
			acceptedTurn := false
			if isResponseCreate {
				if !turnLifecycle.beginResponseCreate(clientFrameConn.markTurnStarted) {
					err := errors.New("overlapping response.create is not supported")
					return payload, nil, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, err.Error(), err)
				}
				defer func() {
					if !acceptedTurn {
						turnLifecycle.cancelResponseCreate()
					}
				}()
			}
			if isResponseCreate && isOpenAIResponsesLiteWebSocketPayload(payload) {
				litePayload, _, liteErr := normalizeOpenAIResponsesLitePayloadForAccount(account, payload)
				if liteErr != nil {
					return payload, nil, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, liteErr.Error(), liteErr)
				}
				payload = litePayload
			}
			if isResponseCreate && hooks != nil && (hooks.MaxReasoningEffort != "" || len(hooks.ReasoningEffortMappings) > 0) {
				if capped, changed := ApplyOpenAIReasoningEffortPolicy(payload, hooks.MaxReasoningEffort, hooks.ReasoningEffortMappings); changed {
					payload = capped
				}
			}
			turnNo := int(completedTurns.Load()) + 1
			if turnNo < 2 {
				turnNo = 2
			}
			if isResponseCreate && hooks != nil && hooks.BeforeRequest != nil {
				requestModel := usageMeta.requestModelForFrame(payload)
				if requestModel == "" {
					requestModel = strings.TrimSpace(usageMeta.sessionRequestModel)
				}
				previousResponseID := strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String())
				updatedPayload, err := hooks.BeforeRequest(turnNo, payload, requestModel, previousResponseID)
				if err != nil {
					return payload, nil, err
				}
				if len(updatedPayload) > 0 {
					payload = updatedPayload
				}
			}

			// 在写入 U 前先保存客户端会话模型 R，避免后续省略 model 时把上游模型当成新请求再次映射。
			usageMeta.updateSessionRequestModel(payload)
			requestModelForThisFrame := usageMeta.requestModelForFrame(payload)
			routingModel := loadCapturedModel(&capturedSessionRoutingModel)
			model := loadCapturedModel(&capturedSessionUpstreamModel)
			switch eventType {
			case "response.create":
				resolvedRoutingModel, upstreamModel, resolveErr := resolveOpenAIWSTurnModels(account, hooks, turnNo, requestModelForThisFrame, payload)
				if resolveErr != nil {
					return payload, nil, resolveErr
				}
				payload, resolveErr = replaceOpenAIWSPassthroughRequestModel(payload, eventType, upstreamModel)
				if resolveErr != nil {
					return payload, nil, resolveErr
				}
				routingModel = resolvedRoutingModel
				model = upstreamModel
				storeCapturedSessionModels(requestModelForThisFrame, resolvedRoutingModel, upstreamModel)
			case "session.update":
				sessionRequestedModel := openAIWSPassthroughRequestModelFromSessionFrame(payload)
				if sessionRequestedModel != "" {
					resolvedRoutingModel, upstreamModel, resolveErr := resolveOpenAIWSTurnModels(account, hooks, turnNo, sessionRequestedModel, payload)
					if resolveErr != nil {
						return payload, nil, resolveErr
					}
					payload, resolveErr = replaceOpenAIWSPassthroughRequestModel(payload, eventType, upstreamModel)
					if resolveErr != nil {
						return payload, nil, resolveErr
					}
					routingModel = resolvedRoutingModel
					model = upstreamModel
					storeCapturedSessionModels(sessionRequestedModel, resolvedRoutingModel, upstreamModel)
				}
			}
			policyCtx := ctx
			if eventType == "response.create" {
				policyCtx = openAIWSFastModePolicyContext(ctx, hooks, turnNo)
			}
			out, blocked, policyErr := s.applyOpenAIFastPolicyToWSResponseCreate(policyCtx, account, model, payload)
			// 多轮 passthrough usage：仅在成功（non-block / non-err）
			// 的 response.create 帧上更新 usageMeta，使用
			// filter 处理后的 payload，与首帧 policy-after-extract 语义
			// 保持一致（参见上方 extractOpenAIServiceTierFromBody 注释）。
			//   - 非 response.create 帧（response.cancel /
			//     conversation.item.create / session.update 等）不携带
			//     per-response metadata，不应覆盖前一轮值。
			//   - blocked != nil：该帧不会发送上游，usage metadata 应保持
			//     上一轮值。
			//   - policyErr != nil：异常路径，保持上一轮值。
			//   - 不带 service_tier 的 response.create 会让
			//     extractOpenAIServiceTierFromBody 返回 nil；这里有意
			//     覆盖（Store(nil)），因为 OpenAI 上游对该帧实际不传
			//     service_tier 时按 default 处理，billing 应如实反映。
			if policyErr == nil && blocked == nil && isResponseCreate {
				if hooks != nil && hooks.BeforeTurn != nil {
					if err := hooks.BeforeTurn(turnNo); err != nil {
						return payload, nil, err
					}
				}
				usageMeta.updateFromResponseCreate(out, model, requestModelForThisFrame)
				turnPayloads.Push(openAIWSTurnPayload{
					RequestBody:        out,
					OriginalModel:      requestModelForThisFrame,
					RoutingModel:       routingModel,
					UpstreamModel:      model,
					ServiceTier:        usageMeta.serviceTier.Load(),
					ReasoningEffort:    usageMeta.reasoningEffort.Load(),
					PreviousResponseID: strings.TrimSpace(gjson.GetBytes(out, "previous_response_id").String()),
					Source:             "passthrough",
				})
				acceptedTurn = true
			}
			return out, blocked, policyErr
		},
		// 客户端事件继续展示该轮请求模型 R，最终上游模型 U 只保留在内部结果和用量字段中。
		writeFilter: func(msgType coderws.MessageType, payload []byte) ([]byte, error) {
			if msgType != coderws.MessageText {
				return payload, nil
			}
			eventType, _, _ := parseOpenAIWSEventEnvelope(payload)
			turnPayload := turnPayloads.Peek()
			if isOpenAIWSTerminalEvent(eventType) {
				if completed := terminalWritePayload.Swap(nil); completed != nil {
					turnPayload = *completed
				}
			}
			if !openAIWSEventMayContainModel(eventType) {
				return payload, nil
			}
			requestedModel := strings.TrimSpace(turnPayload.OriginalModel)
			upstreamModel := strings.TrimSpace(turnPayload.UpstreamModel)
			if requestedModel == "" {
				requestedModel = loadCapturedModel(&capturedSessionRequestedModel)
			}
			if upstreamModel == "" {
				upstreamModel = loadCapturedModel(&capturedSessionUpstreamModel)
			}
			if requestedModel == "" || upstreamModel == "" || requestedModel == upstreamModel || !bytes.Contains(payload, []byte(upstreamModel)) {
				return payload, nil
			}
			return replaceOpenAIWSMessageModel(payload, upstreamModel, requestedModel), nil
		},
		onBlock: func(blocked *OpenAIFastBlockedError) {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			// Conn.Write 会同步刷新帧，因此错误事件会先于关闭帧到达，无需显式刷新。
			eventBytes := buildOpenAIFastPolicyBlockedWSEvent(blocked)
			if eventBytes == nil {
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, s.openAIWSWriteTimeout())
			_ = clientConn.Write(writeCtx, coderws.MessageText, eventBytes)
			cancel()
		},
	}
	upstreamFirstMessageSent := false
	firstWriteCtx, cancelFirstWrite := context.WithTimeout(ctx, s.openAIWSWriteTimeout())
	firstWriteErr := relayUpstreamFrameConn.WriteFrame(firstWriteCtx, coderws.MessageText, firstClientMessage)
	cancelFirstWrite()
	if firstWriteErr != nil {
		return wrapOpenAIWSIngressTurnError(
			"write_upstream",
			fmt.Errorf("write first upstream websocket request: %w", firstWriteErr),
			false,
		)
	}
	upstreamFirstMessageSent = true

	readNextClientFrame := func(readCtx context.Context, conn openaiwsv2.FrameConn) (coderws.MessageType, []byte, error) {
		for {
			msgType, payload, readErr := conn.ReadFrame(readCtx)
			if readErr != nil {
				return msgType, payload, readErr
			}
			if msgType == coderws.MessageText && strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "response.create" {
				return msgType, payload, nil
			}
			if writeErr := upstreamFrameConn.WriteFrame(readCtx, msgType, payload); writeErr != nil {
				return msgType, payload, writeErr
			}
		}
	}

	relayResult, relayExit := openaiwsv2.RunEntry(openaiwsv2.EntryInput{
		Ctx:                ctx,
		ClientConn:         policyClientConn,
		UpstreamConn:       relayUpstreamFrameConn,
		FirstClientMessage: firstClientMessage,
		Options: openaiwsv2.RelayOptions{
			WriteTimeout: s.openAIWSWriteTimeout(),
			// passthrough 的空闲超时仅由 clientFrameConn 在一轮完成后检测；
			// relay 全局活动看门狗会误终止仍在正常处理的上游轮次。
			IdleTimeout:                     0,
			FirstMessageType:                coderws.MessageText,
			FirstMessageSent:                upstreamFirstMessageSent,
			StartClientAfterFirstDownstream: true,
			ReadClientFrame:                 readNextClientFrame,
			OnUsageParseFailure: func(eventType string, usageRaw string) {
				logOpenAIWSV2Passthrough(
					"usage_parse_failed event_type=%s usage_raw=%s",
					truncateOpenAIWSLogValue(eventType, openAIWSLogValueMaxLen),
					truncateOpenAIWSLogValue(usageRaw, openAIWSLogValueMaxLen),
				)
			},
			OnUpstreamEvent: func(eventType string, payload []byte) {
				warning := buildOpenAIWSUpstreamWarning(eventType, payload)
				if warning != nil && hooks != nil && hooks.OnUpstreamError != nil {
					turnNo := int(completedTurns.Load()) + 1
					if turnNo < 1 {
						turnNo = 1
					}
					turnPayload := turnPayloads.Peek()
					hooks.OnUpstreamError(turnNo, turnPayload.OriginalModel, warning.StatusCode, warning.ResponseBody, warning.Message)
				}
			},
			OnTurnComplete: func(turn openaiwsv2.RelayTurnResult) {
				turnNo := int(completedTurns.Add(1))
				turnPayload := turnPayloads.Pop()
				turnPayloadForWrite := turnPayload
				terminalWritePayload.Store(&turnPayloadForWrite)
				turnOriginalModel := strings.TrimSpace(turnPayload.OriginalModel)
				if turnOriginalModel == "" {
					turnOriginalModel = turn.RequestModel
				}
				turnResult := &OpenAIForwardResult{
					RequestID: turn.RequestID,
					Usage: OpenAIUsage{
						InputTokens:              turn.Usage.InputTokens,
						OutputTokens:             turn.Usage.OutputTokens,
						CacheCreationInputTokens: turn.Usage.CacheCreationInputTokens,
						CacheReadInputTokens:     turn.Usage.CacheReadInputTokens,
						ImageOutputTokens:        turn.Usage.ImageOutputTokens,
					},
					Model:                 turnOriginalModel,
					UpstreamModel:         turnPayload.UpstreamModel,
					ServiceTier:           turnPayload.ServiceTier,
					ReasoningEffort:       turnPayload.ReasoningEffort,
					Stream:                true,
					OpenAIWSMode:          true,
					UpstreamTerminalEvent: normalizeOpenAIWSTerminalEvent(turn.TerminalEventType),
					ResponseHeaders:       cloneHeader(handshakeHeaders),
					ResponseBody:          cloneDataSharingRequestBody(turn.TerminalResponseBody),
					Duration:              turn.Duration,
					FirstTokenMs:          turn.FirstTokenMs,
				}
				logOpenAIWSV2Passthrough(
					"relay_turn_completed account_id=%d turn=%d request_id=%s terminal_event=%s duration_ms=%d first_token_ms=%d input_tokens=%d output_tokens=%d cache_read_tokens=%d",
					account.ID,
					turnNo,
					truncateOpenAIWSLogValue(turnResult.RequestID, openAIWSIDValueMaxLen),
					truncateOpenAIWSLogValue(turn.TerminalEventType, openAIWSLogValueMaxLen),
					turnResult.Duration.Milliseconds(),
					openAIWSFirstTokenMsForLog(turnResult.FirstTokenMs),
					turnResult.Usage.InputTokens,
					turnResult.Usage.OutputTokens,
					turnResult.Usage.CacheReadInputTokens,
				)
				if hooks != nil && hooks.AfterTurn != nil {
					hooks.AfterTurn(OpenAIWSTurnCapture{
						Turn:               turnNo,
						RequestBody:        turnPayload.RequestBody,
						OriginalModel:      turnPayload.OriginalModel,
						PreviousResponseID: turnPayload.PreviousResponseID,
						Result:             turnResult,
						PayloadSource:      turnPayload.Source,
					})
				}
			},
			BeforeClientWrite: func(msgType coderws.MessageType, payload []byte) {
				if msgType == coderws.MessageText && openAIWSPassthroughIsTerminalOutput(payload) {
					turnLifecycle.beginTerminalWrite()
				}
			},
			AfterClientWrite: func(msgType coderws.MessageType, payload []byte, writeErr error) {
				if msgType == coderws.MessageText && openAIWSPassthroughIsTerminalOutput(payload) {
					turnLifecycle.finishTerminalWrite(writeErr == nil, clientFrameConn.markTurnCompleted)
				}
			},
			BeforeRelayCancel: func(exit openaiwsv2.RelayExit) {
				if context.Cause(ctx) != nil {
					return
				}
				status, reason, ok := openAIWSPassthroughRelayClientClose(exit, int(completedTurns.Load()))
				if !ok {
					return
				}
				_ = clientConn.Close(status, reason)
				_ = clientConn.CloseNow()
			},
			BeforeWriteClient: func(msgType coderws.MessageType, payload []byte, wroteDownstream bool) error {
				if msgType != coderws.MessageText {
					return nil
				}
				eventType, _, _ := parseOpenAIWSEventEnvelope(payload)
				turnPayload := turnPayloads.Peek()
				routingModel := strings.TrimSpace(turnPayload.RoutingModel)
				if routingModel == "" {
					routingModel = loadCapturedModel(&capturedSessionRoutingModel)
				}
				if eventType == "response.failed" {
					terminalPolicy := s.handleOpenAIWSTerminalTransientFailure(ctx, account, routingModel, handshakeHeaders, payload)
					if terminalPolicy.Decision.ShouldReturnGenericError() {
						return openAIWSGenericPolicyCloseError(terminalPolicy.StatusCode)
					}
					if !wroteDownstream && terminalPolicy.Decision.ShouldFailoverWithDefaults(
						account,
						terminalPolicy.StatusCode,
						false,
						s.shouldFailoverOpenAIWSError(account, terminalPolicy.StatusCode, payload),
					) {
						return newOpenAIUpstreamFailoverError(
							terminalPolicy.StatusCode,
							handshakeHeaders,
							payload,
							extractOpenAISSEErrorMessage(payload),
							terminalPolicy.Decision.RetryableOnSameAccount(account, terminalPolicy.StatusCode),
						)
					}
				}
				if eventType == "error" {
					errorDecision := s.handleOpenAIWSErrorEventTransientFailure(ctx, account, routingModel, handshakeHeaders, payload)
					if wroteDownstream {
						return nil
					}
					errCodeRaw, errTypeRaw, errMsgRaw := parseOpenAIWSErrorEventFields(payload)
					errorStatus := openAIWSErrorPolicyStatus(payload)
					if errorDecision.ShouldReturnGenericError() {
						return openAIWSGenericPolicyCloseError(errorStatus)
					}
					defaultFailover := s.shouldFailoverOpenAIWSError(account, errorStatus, payload)
					if errorStatus == 0 || !errorDecision.ShouldFailoverWithDefaults(
						account,
						errorStatus,
						errorStatus == http.StatusTooManyRequests,
						defaultFailover,
					) {
						return nil
					}
					logOpenAIWSV2Passthrough(
						"relay_error_failover account_id=%d status=%d err_code=%s err_type=%s err_message=%s",
						account.ID,
						errorStatus,
						truncateOpenAIWSLogValue(errCodeRaw, openAIWSLogValueMaxLen),
						truncateOpenAIWSLogValue(errTypeRaw, openAIWSLogValueMaxLen),
						truncateOpenAIWSLogValue(errMsgRaw, openAIWSLogValueMaxLen),
					)
					return newOpenAIUpstreamFailoverError(
						errorStatus,
						handshakeHeaders,
						append([]byte(nil), payload...),
						errMsgRaw,
						errorDecision.RetryableOnSameAccount(account, errorStatus),
					)
				}
				return nil
			},
			OnTrace: func(event openaiwsv2.RelayTraceEvent) {
				logOpenAIWSV2Passthrough(
					"relay_trace account_id=%d stage=%s direction=%s msg_type=%s bytes=%d graceful=%v wrote_downstream=%v err=%s",
					account.ID,
					truncateOpenAIWSLogValue(event.Stage, openAIWSLogValueMaxLen),
					truncateOpenAIWSLogValue(event.Direction, openAIWSLogValueMaxLen),
					truncateOpenAIWSLogValue(event.MessageType, openAIWSLogValueMaxLen),
					event.PayloadBytes,
					event.Graceful,
					event.WroteDownstream,
					truncateOpenAIWSLogValue(event.Error, openAIWSLogValueMaxLen),
				)
			},
		},
	})
	if cause := context.Cause(ctx); cause != nil {
		status := coderws.StatusGoingAway
		reason := "websocket request canceled"
		if errors.Is(cause, ErrOpenAIWSIngressLeaseLost) {
			status = coderws.StatusTryAgainLater
			reason = "websocket ingress capacity lease lost; please reconnect"
		}
		_ = clientConn.Close(status, reason)
		_ = clientConn.CloseNow()
		return NewOpenAIWSClientCloseError(status, reason, cause)
	}

	result := &OpenAIForwardResult{
		RequestID: relayResult.RequestID,
		Usage: OpenAIUsage{
			InputTokens:              relayResult.Usage.InputTokens,
			OutputTokens:             relayResult.Usage.OutputTokens,
			CacheCreationInputTokens: relayResult.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     relayResult.Usage.CacheReadInputTokens,
			ImageOutputTokens:        relayResult.Usage.ImageOutputTokens,
		},
		Model:                 relayResult.RequestModel,
		ServiceTier:           usageMeta.serviceTier.Load(),
		ReasoningEffort:       usageMeta.reasoningEffort.Load(),
		Stream:                true,
		OpenAIWSMode:          true,
		UpstreamTerminalEvent: normalizeOpenAIWSTerminalEvent(relayResult.TerminalEventType),
		ResponseHeaders:       cloneHeader(handshakeHeaders),
		ResponseBody:          cloneDataSharingRequestBody(relayResult.TerminalResponseBody),
		Duration:              relayResult.Duration,
		FirstTokenMs:          relayResult.FirstTokenMs,
	}

	turnCount := int(completedTurns.Load())
	if relayExit == nil {
		logOpenAIWSV2Passthrough(
			"relay_completed account_id=%d request_id=%s terminal_event=%s duration_ms=%d c2u_frames=%d u2c_frames=%d dropped_frames=%d turns=%d",
			account.ID,
			truncateOpenAIWSLogValue(result.RequestID, openAIWSIDValueMaxLen),
			truncateOpenAIWSLogValue(relayResult.TerminalEventType, openAIWSLogValueMaxLen),
			result.Duration.Milliseconds(),
			relayResult.ClientToUpstreamFrames,
			relayResult.UpstreamToClientFrames,
			relayResult.DroppedDownstreamFrames,
			turnCount,
		)
		// 正常路径按 terminal 事件逐 turn 已回调；仅在零 turn 场景兜底回调一次。
		if turnCount == 0 && hooks != nil && hooks.AfterTurn != nil {
			turnPayload := turnPayloads.Pop()
			hooks.AfterTurn(OpenAIWSTurnCapture{
				Turn:               1,
				RequestBody:        turnPayload.RequestBody,
				OriginalModel:      turnPayload.OriginalModel,
				PreviousResponseID: turnPayload.PreviousResponseID,
				Result:             result,
				PayloadSource:      turnPayload.Source,
			})
		}
		return nil
	}
	logOpenAIWSV2Passthrough(
		"relay_failed account_id=%d stage=%s wrote_downstream=%v err=%s duration_ms=%d c2u_frames=%d u2c_frames=%d dropped_frames=%d turns=%d",
		account.ID,
		truncateOpenAIWSLogValue(relayExit.Stage, openAIWSLogValueMaxLen),
		relayExit.WroteDownstream,
		truncateOpenAIWSLogValue(relayErrorText(relayExit.Err), openAIWSLogValueMaxLen),
		result.Duration.Milliseconds(),
		relayResult.ClientToUpstreamFrames,
		relayResult.UpstreamToClientFrames,
		relayResult.DroppedDownstreamFrames,
		turnCount,
	)

	relayErr := relayExit.Err
	var firstOutputTimeoutErr *openAIWSPassthroughFirstOutputTimeoutError
	if errors.As(relayErr, &firstOutputTimeoutErr) {
		deadline := firstOutputTimeoutErr.deadline
		failoverErr := s.newOpenAIFirstOutputTimeoutError(
			ctx,
			c,
			account,
			deadline.startedAt,
			deadline.requestModel,
			deadline.reasoningEffort,
			deadline.timeout,
			"websocket_first_semantic_output",
			handshakeHeaders,
		)
		if turnCount == 0 && !relayExit.WroteDownstream {
			relayErr = failoverErr
		} else {
			// handler 在账号重试之间只保留首个 response.create；后续轮次超时后重放它
			// 会重复执行首轮，因此后续轮次直接结束客户端会话。
			relayErr = NewOpenAIWSClientCloseError(
				coderws.StatusGoingAway,
				"upstream produced no semantic output; please reconnect",
				firstOutputTimeoutErr,
			)
		}
	}
	var activeTurnTimeoutErr *openAIWSPassthroughActiveTurnTimeoutError
	if errors.As(relayErr, &activeTurnTimeoutErr) {
		relayErr = NewOpenAIWSClientCloseError(
			coderws.StatusGoingAway,
			"upstream websocket read timeout; please reconnect",
			activeTurnTimeoutErr,
		)
	}
	if relayExit.Stage == "idle_timeout" {
		relayErr = NewOpenAIWSClientCloseError(
			coderws.StatusPolicyViolation,
			"client websocket idle timeout",
			relayErr,
		)
	}
	turnErr := wrapOpenAIWSIngressTurnError(
		relayExit.Stage,
		relayErr,
		relayExit.WroteDownstream,
	)
	if hooks != nil && hooks.AfterTurn != nil && turnPayloads.Len() > 0 {
		turnPayload := turnPayloads.Pop()
		hooks.AfterTurn(OpenAIWSTurnCapture{
			Turn:               turnCount + 1,
			RequestBody:        turnPayload.RequestBody,
			OriginalModel:      turnPayload.OriginalModel,
			PreviousResponseID: turnPayload.PreviousResponseID,
			Err:                turnErr,
			PayloadSource:      turnPayload.Source,
		})
	}
	return turnErr
}

func openAIWSPassthroughRelayClientClose(exit openaiwsv2.RelayExit, completedTurns int) (coderws.StatusCode, string, bool) {
	var closeErr *OpenAIWSClientCloseError
	if errors.As(exit.Err, &closeErr) {
		return closeErr.StatusCode(), closeErr.Reason(), true
	}
	var activeTurnTimeoutErr *openAIWSPassthroughActiveTurnTimeoutError
	if errors.As(exit.Err, &activeTurnTimeoutErr) {
		return coderws.StatusGoingAway, "upstream websocket read timeout; please reconnect", true
	}
	var firstOutputTimeoutErr *openAIWSPassthroughFirstOutputTimeoutError
	if errors.As(exit.Err, &firstOutputTimeoutErr) {
		if completedTurns > 0 || exit.WroteDownstream {
			return coderws.StatusGoingAway, "upstream produced no semantic output; please reconnect", true
		}
		return 0, "", false
	}
	if !exit.Graceful && exit.Stage == "read_upstream" {
		return coderws.StatusInternalError, "upstream websocket proxy failed", true
	}
	return 0, "", false
}

func (s *OpenAIGatewayService) mapOpenAIWSPassthroughDialError(
	err error,
	statusCode int,
	handshakeHeaders http.Header,
) error {
	if err == nil {
		return nil
	}
	wrappedErr := err
	var dialErr *openAIWSDialError
	if !errors.As(err, &dialErr) {
		var handshakeErr *openAIWSHandshakeError
		var responseBody []byte
		if errors.As(err, &handshakeErr) && handshakeErr != nil {
			responseBody = append([]byte(nil), handshakeErr.Body...)
		}
		wrappedErr = &openAIWSDialError{
			StatusCode:      statusCode,
			ResponseHeaders: cloneHeader(handshakeHeaders),
			ResponseBody:    responseBody,
			Err:             err,
		}
	}

	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewOpenAIWSClientCloseError(
			coderws.StatusTryAgainLater,
			"upstream websocket connect timeout",
			wrappedErr,
		)
	}
	if statusCode == http.StatusTooManyRequests {
		return NewOpenAIWSClientCloseError(
			coderws.StatusTryAgainLater,
			"upstream websocket is busy, please retry later",
			wrappedErr,
		)
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return NewOpenAIWSClientCloseError(
			coderws.StatusPolicyViolation,
			"upstream websocket authentication failed",
			wrappedErr,
		)
	}
	if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
		return NewOpenAIWSClientCloseError(
			coderws.StatusPolicyViolation,
			"upstream websocket handshake rejected",
			wrappedErr,
		)
	}
	return fmt.Errorf("openai ws passthrough dial: %w", wrappedErr)
}

func openaiwsv2RelayMessageTypeName(msgType coderws.MessageType) string {
	switch msgType {
	case coderws.MessageText:
		return "text"
	case coderws.MessageBinary:
		return "binary"
	default:
		return fmt.Sprintf("unknown(%d)", msgType)
	}
}

func relayErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func openAIWSFirstTokenMsForLog(firstTokenMs *int) int {
	if firstTokenMs == nil {
		return -1
	}
	return *firstTokenMs
}

func logOpenAIWSV2Passthrough(format string, args ...any) {
	logger.LegacyPrintf(
		"service.openai_ws_v2",
		"[OpenAI WS v2 passthrough] %s "+format,
		append([]any{openaiWSV2PassthroughModeFields}, args...)...,
	)
}
