package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	mediaprovider "github.com/TokenFlux/TokenRouter/internal/gateway/media/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// ForwardGrokVoice 转发官方 xAI Voice HTTP API，包括 TTS、STT 和自定义 Voice 子资源。
// TTS 返回音频字节、STT 返回 JSON，且 xAI 可能附加格式专用响应头，因此响应保持透传。
func (s *GrokExecutor) ForwardGrokVoice(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, endpoint string, body []byte, contentType string) (*forwardcore.OpenAIResult, error) {
	if s == nil || account == nil {
		return nil, fmt.Errorf("grok voice service/account is required")
	}
	if account.Record.Platform != capability.PlatformGrok {
		return nil, fmt.Errorf("account platform %s is not supported for grok voice", account.Record.Platform)
	}
	var err error
	endpoint, baseEndpoint, err := grok.ValidateVoiceEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	token, _, err := s.Credentials.Resolve(ctx, RequestCredentialBudget(c), CredentialObserver{Context: c}, account)
	if err != nil {
		return nil, err
	}
	targetURL, err := s.Routes.Voice(account, endpoint)
	if err != nil {
		return nil, err
	}
	upstreamCtx, release := gatewayprovider.DetachUpstreamContext(ctx)
	defer release()
	upstreamCtx = upstreamcore.WithHTTPUpstreamProfile(upstreamCtx, upstreamcore.HTTPUpstreamProfileGrok)
	method := http.MethodPost
	if c != nil && c.Request != nil && strings.TrimSpace(c.Request.Method) != "" {
		method = c.Request.Method
	}
	req, err := grok.BuildVoiceRequest(upstreamCtx, method, targetURL, token, contentType, body, func(headers http.Header) {
		if account.View().IsGrokOAuth() && isGrokCLIProxyTarget(targetURL) {
			grok.ApplyCLIHeaders(headers)
		}
		accountprovider.ApplyAccountHeaderOverrides(gatewayprovider.ExecutionProtocolRecord(account), headers)
	})
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}
	var handledResult *forwardcore.OpenAIResult
	handled := false
	target := &mediaprovider.GrokVoiceOptions{
		AccountID:    account.Record.ID,
		Endpoint:     endpoint,
		BaseEndpoint: baseEndpoint,
		ContentType:  contentType,
		Request:      req,
		Enter:        s.Enter,
		Do: func(req *http.Request) (*http.Response, error) {
			return s.Transport.Do(req, proxyURL, account.Record.ID, account.Record.Concurrency)
		},
		AfterExchange: func(elapsed time.Duration, err error) error {
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, elapsed.Milliseconds())
			if err != nil {
				return s.Failure.Handle(ctx, c, account, err, false)
			}
			return nil
		},
		BeforeResponse: func(resp *http.Response) (bool, error) {
			if resp.StatusCode < 400 {
				return false, nil
			}
			handled = true
			var err error
			handledResult, err = s.handleGrokMediaErrorResponse(ctx, resp, c, account, resp.Header.Get("x-request-id"), endpoint)
			return true, err
		},
		ReadBody: func(reader io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(reader, s.Output.Options.ReadLimit, c, OpenAIResponseTooLarge)
		},
		CopyHeaders: func(dst, src http.Header) {
			WriteOpenAIPassthroughResponseHeaders(dst, src, s.Output.Headers)
		},
	}
	var sink upstreamcore.OutputSink
	if c != nil {
		sink = ResponseSink{Writer: c.Writer}
	}
	proto := protocol.ProtocolCustomVoices
	switch baseEndpoint {
	case "tts":
		proto = protocol.ProtocolTTS
	case "stt":
		proto = protocol.ProtocolSTT
	}
	result, err := (mediaprovider.GrokVoice{Options: *target}).Execute(upstreamCtx, upstreamcore.AttemptInput{Protocol: proto, Body: body}, sink)
	if handled {
		return handledResult, err
	}
	if err != nil {
		return nil, err
	}
	return &forwardcore.OpenAIResult{
		RequestID:       gatewayprovider.StableAudioBillingRequestID(result.RequestID),
		UpstreamHeaders: result.UpstreamHeaders,
		Model:           result.Model,
		UpstreamModel:   result.UpstreamModel,
		Duration:        result.Duration,
		AudioUsage:      result.AudioUsage,
	}, nil
}

// ProxyGrokRealtime 将 JSON Realtime 事件中继到 xAI 原生 Voice WebSocket。
// 音频以 base64 包含在 JSON 事件中，保持原始 JSON 字节即可，无需转换协议事件类型。
func (s *GrokExecutor) ProxyGrokRealtime(ctx context.Context, c *gin.Context, client *coderws.Conn, account *gatewayprovider.ExecutionAccount, token, model string) (bool, error) {
	if s == nil || client == nil || account == nil {
		return false, fmt.Errorf("realtime service, client, and account are required")
	}
	if account.Record.Platform != capability.PlatformGrok {
		return false, fmt.Errorf("account platform %s is not supported for grok realtime", account.Record.Platform)
	}
	upstream, err := s.OpenGrokRealtime(ctx, account, token, model)
	if err != nil {
		return false, err
	}
	defer func() { _ = upstream.Close() }()
	return s.ProxyGrokRealtimeConn(ctx, c, client, upstream)
}

func (s *GrokExecutor) OpenGrokRealtime(ctx context.Context, account *gatewayprovider.ExecutionAccount, token, model string) (*grok.RealtimeSession, error) {
	if s == nil || account == nil || account.Record.Platform != capability.PlatformGrok {
		return nil, fmt.Errorf("grok realtime account is required")
	}
	base, err := s.Routes.Voice(account, "realtime")
	if err != nil {
		return nil, err
	}
	return mediaprovider.DialRealtime(ctx, s.grokRealtimeOptions(account, base, token, model))
}

// HandleGrokRealtimeUpstreamError 为下游升级前失败的 WebSocket 握手应用共享 Grok 账号策略。
func (s *GrokExecutor) HandleGrokRealtimeUpstreamError(ctx context.Context, account *gatewayprovider.ExecutionAccount, statusCode int, body []byte) {
	if statusCode <= 0 {
		statusCode = http.StatusBadGateway
	}
	_ = gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Health, account, statusCode, nil, body, "")
}

func (s *GrokExecutor) ProxyGrokRealtimeConn(ctx context.Context, c *gin.Context, client *coderws.Conn, upstream *grok.RealtimeSession) (bool, error) {
	if s == nil || client == nil || !upstream.Ready() {
		return false, fmt.Errorf("realtime connection is required")
	}
	return grok.RelayRealtime(ctx, grokClientFrames{client}, upstream)
}

func (s *GrokExecutor) ProbeGrokRealtime(ctx context.Context, account *gatewayprovider.ExecutionAccount, token, model string) error {
	if s == nil || account == nil {
		return fmt.Errorf("realtime service and account are required")
	}
	if account.Record.Platform != capability.PlatformGrok {
		return fmt.Errorf("account platform %s is not supported for grok realtime", account.Record.Platform)
	}
	base, err := s.Routes.Voice(account, "realtime")
	if err != nil {
		return err
	}
	return mediaprovider.ProbeRealtime(ctx, s.grokRealtimeOptions(account, base, token, model))
}

// HTTP/既有 WS SDK 只转换同步帧接口，升级与连接租约仍由原入口拥有。
type grokClientFrames struct{ conn *coderws.Conn }

func (c grokClientFrames) ReadFrame(ctx context.Context) (upstreamcore.FrameKind, []byte, error) {
	kind, data, err := c.conn.Read(ctx)
	return upstreamcore.FrameKind(kind), data, err
}
func (c grokClientFrames) WriteFrame(ctx context.Context, kind upstreamcore.FrameKind, data []byte) error {
	return c.conn.Write(ctx, coderws.MessageType(kind), data)
}
func (c grokClientFrames) Close() error { return c.conn.CloseNow() }

type grokUpstreamFrames struct{ conn openai.WSClientConn }

func (c grokUpstreamFrames) ReadFrame(ctx context.Context) (upstreamcore.FrameKind, []byte, error) {
	data, err := c.conn.ReadMessage(ctx)
	return upstreamcore.FrameText, data, err
}
func (c grokUpstreamFrames) WriteFrame(ctx context.Context, _ upstreamcore.FrameKind, data []byte) error {
	return c.conn.WriteJSON(ctx, json.RawMessage(data))
}
func (c grokUpstreamFrames) Close() error { return c.conn.Close() }

// 装配既有 WS dialer、代理和 TLS 快照，不更改共享客户端。
func (s *GrokExecutor) grokRealtimeOptions(account *gatewayprovider.ExecutionAccount, base, token, model string) mediaprovider.RealtimeOptions {
	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}
	options := mediaprovider.RealtimeOptions{
		BaseURL:      base,
		Token:        token,
		Model:        model,
		ApplyHeaders: gatewayprovider.BindExecutionHeaders(account),
		Enter:        s.Enter,
		Dial: func(ctx context.Context, target string, headers http.Header) (upstreamcore.FrameConn, int, error) {
			conn, status, _, err := s.Dialer.Dial(ctx, target, headers, proxyURL, s.TLSProfile(account))
			if conn == nil {
				return nil, status, err
			}
			return grokUpstreamFrames{conn}, status, err
		},
	}
	if account.View().IsGrokOAuth() {
		options.CLIHeaders = grok.ApplyCLIHeaders
	}
	return options
}

// ProxyGrokRealtimeFrames 接收受控帧连接，供 media 持有关闭和账号槽所有权。
func (s *GrokExecutor) ProxyGrokRealtimeFrames(ctx context.Context, client *coderws.Conn, conn upstreamcore.FrameConn) (bool, error) {
	if s == nil || client == nil || conn == nil {
		return false, fmt.Errorf("realtime connection is required")
	}
	return grok.RelayRealtime(ctx, grokClientFrames{client}, conn)
}

// RelayGrokRealtimeFrames 只连接原生帧中继；入站升级与槽位由媒体 HTTP/core 拥有。
func (s *GrokExecutor) RelayGrokRealtimeFrames(ctx context.Context, client, server upstreamcore.FrameConn) (bool, error) {
	return grok.RelayRealtime(ctx, client, server)
}
