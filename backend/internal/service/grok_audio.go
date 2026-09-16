package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/protocol"

	nativeupstream "github.com/TokenFlux/TokenRouter/internal/upstream"
	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// DefaultGrokRealtimeDialTimeout 限制下游升级前的上游 WebSocket 握手时间；连接建立后不再中断会话。
const DefaultGrokRealtimeDialTimeout = 12 * time.Second

// ForwardGrokVoice 转发官方 xAI Voice HTTP API，包括 TTS、STT 和自定义 Voice 子资源。
// TTS 返回音频字节、STT 返回 JSON，且 xAI 可能附加格式专用响应头，因此响应保持透传。
func (s *OpenAIGatewayService) ForwardGrokVoice(ctx context.Context, c *gin.Context, account *Account, endpoint string, body []byte, contentType string) (*OpenAIForwardResult, error) {
	if s == nil || account == nil {
		return nil, fmt.Errorf("grok voice service/account is required")
	}
	if account.Platform != PlatformGrok {
		return nil, fmt.Errorf("account platform %s is not supported for grok voice", account.Platform)
	}
	var err error
	endpoint, baseEndpoint, err := nativegrok.ValidateVoiceEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, err
	}
	targetURL, err := buildGrokVoiceURL(account, s.cfg, endpoint)
	if err != nil {
		return nil, err
	}
	upstreamCtx, release := detachUpstreamContext(ctx)
	defer release()
	upstreamCtx = WithHTTPUpstreamProfile(upstreamCtx, HTTPUpstreamProfileGrok)
	method := http.MethodPost
	if c != nil && c.Request != nil && strings.TrimSpace(c.Request.Method) != "" {
		method = c.Request.Method
	}
	req, err := nativegrok.BuildVoiceRequest(upstreamCtx, method, targetURL, token, contentType, body, func(headers http.Header) {
		if account.IsGrokOAuth() && isGrokCLIProxyTarget(targetURL) {
			applyGrokCLIHeaders(headers)
		}
		account.ApplyHeaderOverrides(headers)
	})
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var handledResult *OpenAIForwardResult
	handled := false
	target := &nativegrok.VoiceTarget{
		AccountID:    account.ID,
		Endpoint:     endpoint,
		BaseEndpoint: baseEndpoint,
		ContentType:  contentType,
		Request:      req,
		Enter:        s.nativeAttemptActivity,
		Do: func(req *http.Request) (*http.Response, error) {
			return s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
		},
		AfterExchange: func(elapsed time.Duration, err error) error {
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, elapsed.Milliseconds())
			if err != nil {
				return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
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
			return ReadUpstreamResponseBody(reader, s.cfg, c, openAITooLargeError)
		},
		CopyHeaders: func(dst, src http.Header) { writeOpenAIPassthroughResponseHeaders(dst, src, s.responseHeaderFilter) },
	}
	var sink nativeupstream.OutputSink
	if c != nil {
		sink = gatewayhttp.ResponseSink{Writer: c.Writer}
	}
	proto := protocol.ProtocolCustomVoices
	switch baseEndpoint {
	case "tts":
		proto = protocol.ProtocolTTS
	case "stt":
		proto = protocol.ProtocolSTT
	}
	result, err := (nativegrok.VoiceExecutor{}).Execute(upstreamCtx, nativeupstream.AttemptInput{Protocol: proto, Body: body, Target: target}, sink)
	if handled {
		return handledResult, err
	}
	if err != nil {
		return nil, err
	}
	return &OpenAIForwardResult{
		RequestID:       StableGrokAudioBillingRequestID(result.RequestID),
		UpstreamHeaders: result.UpstreamHeaders,
		Model:           result.Model,
		UpstreamModel:   result.UpstreamModel,
		Duration:        result.Duration,
		AudioUsage:      result.AudioUsage,
	}, nil
}

// ProxyGrokRealtime 将 JSON Realtime 事件中继到 xAI 原生 Voice WebSocket。
// 音频以 base64 包含在 JSON 事件中，保持原始 JSON 字节即可，无需转换协议事件类型。
func (s *OpenAIGatewayService) ProxyGrokRealtime(ctx context.Context, c *gin.Context, client *coderws.Conn, account *Account, token, model string) (bool, error) {
	if s == nil || client == nil || account == nil {
		return false, fmt.Errorf("realtime service, client, and account are required")
	}
	if account.Platform != PlatformGrok {
		return false, fmt.Errorf("account platform %s is not supported for grok realtime", account.Platform)
	}
	upstream, err := s.OpenGrokRealtime(ctx, account, token, model)
	if err != nil {
		return false, err
	}
	defer func() { _ = upstream.Close() }()
	return s.ProxyGrokRealtimeConn(ctx, c, client, upstream)
}

type GrokRealtimeUpstream = nativegrok.RealtimeSession

type GrokRealtimeDialError = nativegrok.RealtimeDialError

func (s *OpenAIGatewayService) OpenGrokRealtime(ctx context.Context, account *Account, token, model string) (*GrokRealtimeUpstream, error) {
	if s == nil || account == nil || account.Platform != PlatformGrok {
		return nil, fmt.Errorf("grok realtime account is required")
	}
	base, err := buildGrokVoiceURL(account, s.cfg, "realtime")
	if err != nil {
		return nil, err
	}
	return nativegrok.DialRealtime(ctx, s.grokRealtimeOptions(account, base, token, model))
}

// HandleGrokRealtimeUpstreamError 为下游升级前失败的 WebSocket 握手应用共享 Grok 账号策略。
func (s *OpenAIGatewayService) HandleGrokRealtimeUpstreamError(ctx context.Context, account *Account, statusCode int, body []byte) {
	if statusCode <= 0 {
		statusCode = http.StatusBadGateway
	}
	_ = s.applyGrokAccountUpstreamError(ctx, account, statusCode, nil, body)
}

func (s *OpenAIGatewayService) ProxyGrokRealtimeConn(ctx context.Context, c *gin.Context, client *coderws.Conn, upstream *GrokRealtimeUpstream) (bool, error) {
	if s == nil || client == nil || !upstream.Ready() {
		return false, fmt.Errorf("realtime connection is required")
	}
	return nativegrok.RelayRealtime(ctx, grokClientFrames{client}, upstream)
}

func (s *OpenAIGatewayService) ProbeGrokRealtime(ctx context.Context, account *Account, token, model string) error {
	if s == nil || account == nil {
		return fmt.Errorf("realtime service and account are required")
	}
	if account.Platform != PlatformGrok {
		return fmt.Errorf("account platform %s is not supported for grok realtime", account.Platform)
	}
	base, err := buildGrokVoiceURL(account, s.cfg, "realtime")
	if err != nil {
		return err
	}
	return nativegrok.ProbeRealtime(ctx, s.grokRealtimeOptions(account, base, token, model))
}

func awaitGrokRealtimeAudioObserved(errCh <-chan error, audioObserved *atomic.Bool) (bool, error) {
	return nativegrok.AwaitGrokRealtimeAudioObserved(errCh, audioObserved)
}

func grokRealtimeEventHasAudio(msg []byte) bool { return nativegrok.GrokRealtimeEventHasAudio(msg) }

// HTTP/既有 WS SDK 只转换同步帧接口，升级与连接租约仍由原入口拥有。
type grokClientFrames struct{ conn *coderws.Conn }

func (c grokClientFrames) ReadFrame(ctx context.Context) (nativeupstream.FrameKind, []byte, error) {
	kind, data, err := c.conn.Read(ctx)
	return nativeupstream.FrameKind(kind), data, err
}
func (c grokClientFrames) WriteFrame(ctx context.Context, kind nativeupstream.FrameKind, data []byte) error {
	return c.conn.Write(ctx, coderws.MessageType(kind), data)
}
func (c grokClientFrames) Close() error { return c.conn.CloseNow() }

type grokUpstreamFrames struct{ conn openAIWSClientConn }

func (c grokUpstreamFrames) ReadFrame(ctx context.Context) (nativeupstream.FrameKind, []byte, error) {
	data, err := c.conn.ReadMessage(ctx)
	return nativeupstream.FrameText, data, err
}
func (c grokUpstreamFrames) WriteFrame(ctx context.Context, _ nativeupstream.FrameKind, data []byte) error {
	return c.conn.WriteJSON(ctx, json.RawMessage(data))
}
func (c grokUpstreamFrames) Close() error { return c.conn.Close() }

// 装配既有 WS dialer、代理和 TLS 快照，不更改共享客户端。
func (s *OpenAIGatewayService) grokRealtimeOptions(account *Account, base, token, model string) nativegrok.RealtimeDialOptions {
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	options := nativegrok.RealtimeDialOptions{
		BaseURL:      base,
		Token:        token,
		Model:        model,
		ApplyHeaders: account.ApplyHeaderOverrides,
		Enter:        s.nativeAttemptActivity,
		Dial: func(ctx context.Context, target string, headers http.Header) (nativeupstream.FrameConn, int, error) {
			conn, status, _, err := s.getOpenAIWSPassthroughDialer().Dial(ctx, target, headers, proxyURL, s.resolveOpenAITLSProfile(account))
			if conn == nil {
				return nil, status, err
			}
			return grokUpstreamFrames{conn}, status, err
		},
	}
	if account.IsGrokOAuth() {
		options.CLIHeaders = applyGrokCLIHeaders
	}
	return options
}
