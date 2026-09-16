package service

import (
	"context"

	gatewaylive "github.com/TokenFlux/TokenRouter/internal/gateway/live"
	coderws "github.com/coder/websocket"
)

// livePorts 仅把现有依赖和展示值投影给纯 Live 编排。
type livePorts struct{ service *OpenAIGatewayService }

func (p livePorts) Store() (LiveCallStore, error)         { return p.service.liveStore() }
func (p livePorts) Leases() (LiveConcurrencyCache, error) { return p.service.liveConcurrencyCache() }
func (p livePorts) BeginObserver(owner string) (context.Context, func(), bool) {
	return p.service.beginLiveObserver(owner)
}
func (p livePorts) Target(ctx context.Context, record *LiveCallRecord) (gatewaylive.Target, error) {
	account, err := p.service.liveSidebandAccount(ctx, record)
	if err != nil {
		return nil, err
	}
	return liveTarget{service: p.service, record: record, account: account}, nil
}
func (p livePorts) RecordZeroUsage(ctx context.Context, record *LiveCallRecord, duration int) {
	if p.service.usageLogRepo == nil {
		return
	}
	inboundEndpoint := record.InboundEndpoint
	upstreamEndpoint := "/backend-api/codex/realtime/calls"
	userAgent := record.UserAgent
	ipAddress := record.IPAddress
	billingType := int8(BillingTypeBalance)
	if record.SubscriptionID > 0 {
		billingType = BillingTypeSubscription
	}
	actorUserID := record.ActorUserID
	if actorUserID <= 0 {
		actorUserID = record.UserID
	}
	// TODO(billing): Live 当前只记录零费用用量，尚未进入标准计费管道；若后续按时长
	// 或 token 计费，应在这里接入统一扣费逻辑并补充余额与订阅模式回归测试。
	// Live finalize 只有一次落库机会，复用批量写入与同步 Create 兜底，避免队列故障吞掉记录。
	writeUsageLogBestEffort(context.Background(), p.service.usageLogRepo, &UsageLog{
		UserID:            actorUserID,
		BillingUserID:     record.UserID,
		TeamID:            liveOptionalID(record.TeamID),
		APIKeyID:          record.APIKeyID,
		AccountID:         record.AccountID,
		RequestID:         record.CallHash,
		Model:             record.Model,
		RequestedModel:    firstNonEmpty(record.RequestedModel, record.Model),
		UpstreamModel:     liveOptionalString(record.UpstreamModel),
		ModelMappingChain: liveOptionalString(record.ModelMappingChain),
		GroupID:           liveOptionalID(record.GroupID),
		SubscriptionID:    liveOptionalID(record.SubscriptionID),
		RateMultiplier:    1,
		BillingType:       billingType,
		RequestType:       RequestTypeLive,
		DurationMs:        &duration,
		UserAgent:         &userAgent,
		IPAddress:         &ipAddress,
		InboundEndpoint:   &inboundEndpoint,
		UpstreamEndpoint:  &upstreamEndpoint,
		CreatedAt:         record.CreatedAt,
	}, "service.openai_live")
}

// liveTarget 保留账号执行凭据和平台拨号，核心只能使用受控帧接口。
type liveTarget struct {
	service *OpenAIGatewayService
	record  *LiveCallRecord
	account *Account
}

func (t liveTarget) Dial(ctx context.Context) (gatewaylive.FrameConn, error) {
	conn, err := t.service.dialLiveSidebandForAccount(ctx, t.record, t.account)
	if err != nil {
		return nil, err
	}
	return liveUpstreamFrames{conn}, nil
}
func (t liveTarget) Rewrite(ctx context.Context, payload []byte) ([]byte, string, []string, error) {
	return t.service.rewriteLiveSidebandClientPayload(ctx, t.record, t.account, payload)
}

// liveUpstreamFrames 只转换帧枚举和正常关闭错误，底层连接由 Live 编排关闭。
type liveUpstreamFrames struct{ liveFrameConn }

func (c liveUpstreamFrames) ReadFrame(ctx context.Context) (int, []byte, error) {
	typ, body, err := c.liveFrameConn.ReadFrame(ctx)
	return int(typ), body, liveSidebandReadError(err)
}
func (c liveUpstreamFrames) WriteFrame(ctx context.Context, typ int, body []byte) error {
	return c.liveFrameConn.WriteFrame(ctx, coderws.MessageType(typ), body)
}
func (c liveUpstreamFrames) SetReadLimit(limit int64) {}

// liveDownstreamFrames 不接管 HTTP 层的关闭帧和升级责任。
type liveDownstreamFrames struct{ conn *coderws.Conn }

func (c liveDownstreamFrames) ReadFrame(ctx context.Context) (int, []byte, error) {
	typ, body, err := c.conn.Read(ctx)
	return int(typ), body, err
}
func (c liveDownstreamFrames) WriteFrame(ctx context.Context, typ int, body []byte) error {
	return c.conn.Write(ctx, coderws.MessageType(typ), body)
}
func (c liveDownstreamFrames) SetReadLimit(limit int64) { c.conn.SetReadLimit(limit) }
func (c liveDownstreamFrames) Close() error             { return c.conn.CloseNow() }

// liveModelResolver 适配当前账号能力与路由结果，不实施报文改写。
type liveModelResolver struct {
	service *OpenAIGatewayService
	account *Account
}

func (r liveModelResolver) ResolveModel(ctx context.Context, groupID *int64, model string) (string, string, error) {
	routing, err := r.service.ResolveOpenAIWSRoutingModelForAccount(ctx, groupID, r.account, model, OpenAIEndpointCapabilityLive)
	if err != nil {
		return "", "", err
	}
	return routing, resolveOpenAIAccountUpstreamModelForRequest(r.account, routing, false, false), nil
}
