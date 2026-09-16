package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	coderws "github.com/coder/websocket"
)

// wsIngressAdapter 绑定单个技术操作，不拥有会话循环或恢复决策。
type wsIngressAdapter struct {
	RecoverAcquireFn func(context.Context) error
	ParseFn          func(raw []byte, replace bool, turn int) (gatewayws.ClientPayload, error)
	ReadFn           func() ([]byte, error)
	GenerateHashFn   func(body []byte) string
	StoreDisabledFn  func(body []byte) bool
	ShouldBridgeFn   func(payload gatewayws.ClientPayload) bool
	InvalidFn        func(groupID int64, hash string) map[string]struct{}
	StripFn          func(body []byte, digests map[string]struct{}, key string, id int64, turn int) ([]byte, int)
	BridgeIdentityFn func(body []byte, model string) (string, error)
	BridgeFn         func(ctx context.Context, input gatewayws.ClientPayload, body []byte, identity string, turn int) (*gatewayws.ForwardResult, error)
	SetStateFn       func(state, hash string)
	OpenPoolFn       func(payload gatewayws.ClientPayload) error
	AcquireFn        func(turn int, preferred string, force bool, allowRecovery bool) (gatewayws.ConnLease, error)
	RelayFn          func(turn int, lease gatewayws.ConnLease, payload gatewayws.ClientPayload) (*gatewayws.ForwardResult, error)
	PinFn            func(id int64, conn string) bool
	UnpinFn          func(id int64, conn string)
	HeaderFn         func(key string) string
	UpdateHeadersFn  func(payload gatewayws.ClientPayload, state string)
	BindOwnerFn      func(ctx context.Context, responseID string)
}

func (p *wsIngressAdapter) Parse(raw []byte, replace bool, turn int) (gatewayws.ClientPayload, error) {
	return p.ParseFn(raw, replace, turn)
}
func (p *wsIngressAdapter) ReadClient() ([]byte, error)     { return p.ReadFn() }
func (p *wsIngressAdapter) GenerateHash(body []byte) string { return p.GenerateHashFn(body) }
func (p *wsIngressAdapter) StoreDisabled(body []byte) bool  { return p.StoreDisabledFn(body) }
func (p *wsIngressAdapter) ShouldBridge(payload gatewayws.ClientPayload) bool {
	return p.ShouldBridgeFn(payload)
}
func (p *wsIngressAdapter) InvalidDigests(groupID int64, hash string) map[string]struct{} {
	return p.InvalidFn(groupID, hash)
}
func (p *wsIngressAdapter) StripInvalid(body []byte, digests map[string]struct{}, key string, id int64, turn int) ([]byte, int) {
	return p.StripFn(body, digests, key, id, turn)
}
func (p *wsIngressAdapter) BridgeIdentity(body []byte, model string) (string, error) {
	return p.BridgeIdentityFn(body, model)
}
func (p *wsIngressAdapter) Bridge(ctx context.Context, input gatewayws.ClientPayload, body []byte, identity string, turn int) (*gatewayws.ForwardResult, error) {
	return p.BridgeFn(ctx, input, body, identity, turn)
}
func (p *wsIngressAdapter) SetRequestState(state, hash string) { p.SetStateFn(state, hash) }
func (p *wsIngressAdapter) OpenPool(payload gatewayws.ClientPayload) error {
	return p.OpenPoolFn(payload)
}
func (p *wsIngressAdapter) Acquire(turn int, preferred string, force bool, allowRecovery bool) (gatewayws.ConnLease, error) {
	return p.AcquireFn(turn, preferred, force, allowRecovery)
}
func (p *wsIngressAdapter) Relay(turn int, lease gatewayws.ConnLease, payload gatewayws.ClientPayload) (*gatewayws.ForwardResult, error) {
	return p.RelayFn(turn, lease, payload)
}
func (p *wsIngressAdapter) PinConn(id int64, conn string) bool { return p.PinFn(id, conn) }
func (p *wsIngressAdapter) UnpinConn(id int64, conn string)    { p.UnpinFn(id, conn) }
func (p *wsIngressAdapter) Header(key string) string           { return p.HeaderFn(key) }
func (p *wsIngressAdapter) UpdateHeaders(payload gatewayws.ClientPayload, state string) {
	p.UpdateHeadersFn(payload, state)
}
func (p *wsIngressAdapter) BindOwner(ctx context.Context, responseID string) {
	p.BindOwnerFn(ctx, responseID)
}

func (*wsIngressAdapter) IsDisconnect(err error) bool { return isOpenAIWSClientDisconnectError(err) }
func (*wsIngressAdapter) IsFailover(err error) bool {
	var value *UpstreamFailoverError
	return errors.As(err, &value) && value != nil
}
func (*wsIngressAdapter) CloseError(status int, reason string, err error) error {
	return NewOpenAIWSClientCloseError(coderws.StatusCode(status), reason, err)
}
func (*wsIngressAdapter) Log(message string)               { logOpenAIWSModeInfo("%s", message) }
func (*wsIngressAdapter) NormalizeLog(value string) string { return normalizeOpenAIWSLogValue(value) }
func (*wsIngressAdapter) TruncateLog(value string, limit int) string {
	return truncateOpenAIWSLogValue(value, limit)
}
func (*wsIngressAdapter) SummarizeClose(err error) (string, string) {
	return summarizeOpenAIWSReadCloseError(err)
}
func (*wsIngressAdapter) BindWarning(group, account int64, response string, err error) {
	logOpenAIWSBindResponseAccountWarn(group, account, response, err)
}

// wsIngressLease 是池资源句柄；核心没有账号凭据或具体客户端访问能力。
type wsIngressLease struct{ lease *openAIWSConnLease }

func (l *wsIngressLease) ConnID() string { return l.lease.ConnID() }
func (l *wsIngressLease) MarkBroken()    { l.lease.MarkBroken() }
func (l *wsIngressLease) Release()       { l.lease.Release() }
func (l *wsIngressLease) SupportsIdlePingWithoutReader() bool {
	return l.lease.SupportsIdlePingWithoutReader()
}
func (l *wsIngressLease) PingWithTimeout(timeout time.Duration) error {
	return l.lease.PingWithTimeout(timeout)
}

// wsReplayCodec 只委托供应商的唯一报文算法，所有重放条件和次数由新核心决定。
type wsReplayCodec struct{}

func (wsReplayCodec) Extract(body []byte) ([]json.RawMessage, bool, error) {
	return openAIWSExtractNormalizedInputSequence(body)
}
func (wsReplayCodec) BuildFromItems(a []json.RawMessage, b bool, c []json.RawMessage, d, e bool) ([]json.RawMessage, bool) {
	return buildOpenAIWSReplayInputSequenceFromItems(a, b, c, d, e)
}
func (wsReplayCodec) Build(a []json.RawMessage, b bool, c []byte, d bool) ([]json.RawMessage, bool, error) {
	return buildOpenAIWSReplayInputSequence(a, b, c, d)
}
func (wsReplayCodec) SetInput(a []byte, b []json.RawMessage, c bool) ([]byte, error) {
	return setOpenAIWSPayloadInputSequence(a, b, c)
}
func (wsReplayCodec) RetryPayload(a []byte, b []json.RawMessage, c bool, d string) ([]byte, bool, error) {
	return buildOpenAIWSCurrentTurnRetryPayload(a, b, c, d)
}
func (wsReplayCodec) Combine(a, b []json.RawMessage) []json.RawMessage {
	return combineOpenAIWSReplayItems(a, b)
}
func (wsReplayCodec) HasOutput(body []byte) bool { return openAIWSRawPayloadHasToolCallOutput(body) }
func (wsReplayCodec) ItemsHaveOutput(items []json.RawMessage) bool {
	return openAIWSRawItemsHasFunctionCallOutput(items)
}
func (wsReplayCodec) ItemsCoverOutput(items []json.RawMessage) bool {
	return openAIWSRawItemsHaveToolCallContextForOutputs(items)
}
func (wsReplayCodec) DropPrevious(body []byte) ([]byte, bool, error) {
	return dropPreviousResponseIDFromRawPayload(body)
}
func (wsReplayCodec) SetPrevious(body []byte, id string) ([]byte, error) {
	return setPreviousResponseIDToRawPayload(body, id)
}
func (wsReplayCodec) BuildStrict(body []byte) (gatewayws.PreviousTurn, error) {
	state, err := buildOpenAIWSIngressPreviousTurnStrictState(body)
	if err != nil || state == nil {
		return nil, err
	}
	return wsStrictTurn{state}, nil
}
func (wsReplayCodec) KeepPrevious(a, b []byte, c string, d bool) (bool, string, error) {
	return shouldKeepIngressPreviousResponseID(a, b, c, d)
}
func (wsReplayCodec) StripItems(a []json.RawMessage, b map[string]struct{}) ([]json.RawMessage, int) {
	return stripOpenAIInvalidEncryptedContentFromReplayItems(a, b)
}
func (wsReplayCodec) ShouldInfer(a bool, b int, c wire.ToolContinuationSignals, d, e string) bool {
	return shouldInferIngressFunctionCallOutputPreviousResponseID(a, b, c, d, e)
}
func (wsReplayCodec) ClassifyPrevious(id string) string {
	return ClassifyOpenAIPreviousResponseIDKind(id)
}

// wsStrictTurn 只封装纯协议比较状态，不包含账号、配置或 I/O。
type wsStrictTurn struct {
	state *openAIWSIngressPreviousTurnStrictState
}

func (s wsStrictTurn) Keep(a []byte, b string, c bool) (bool, string, error) {
	return shouldKeepIngressPreviousResponseIDWithStrictState(s.state, a, b, c)
}

func wsIngressHooks(h *OpenAIWSIngressHooks) *gatewayws.IngressHooks {
	if h == nil {
		return nil
	}
	out := &gatewayws.IngressHooks{TurnStarted: h.TurnStarted, BeforeTurn: h.BeforeTurn, BeforeRequest: h.BeforeRequest}
	if h.AfterTurn != nil {
		out.AfterTurn = func(c gatewayws.TurnCapture) {
			h.AfterTurn(OpenAIWSTurnCapture{Turn: c.Turn, StartedAt: c.StartedAt, RequestBody: c.RequestBody, OriginalModel: c.OriginalModel, PreviousResponseID: c.PreviousResponseID, Result: legacyWSForwardResult(c.Result), Err: c.Err, PayloadSource: c.PayloadSource})
		}
	}
	return out
}

func (*wsIngressAdapter) Debug(message string) { logOpenAIWSModeDebug("%s", message) }

func (p *wsIngressAdapter) RecoverAcquire(ctx context.Context) error { return p.RecoverAcquireFn(ctx) }
