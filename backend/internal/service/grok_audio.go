package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// supportedGrokVoiceHTTPEndpoints 列出原样转发的 xAI Voice HTTP 路径。
var supportedGrokVoiceHTTPEndpoints = map[string]struct{}{
	"tts":           {},
	"stt":           {},
	"custom-voices": {},
}

// ForwardGrokVoice 转发官方 xAI Voice HTTP API，包括 TTS、STT 和自定义 Voice 子资源。
// TTS 返回音频字节、STT 返回 JSON，且 xAI 可能附加格式专用响应头，因此响应保持透传。
func (s *OpenAIGatewayService) ForwardGrokVoice(ctx context.Context, c *gin.Context, account *Account, endpoint string, body []byte, contentType string) (*OpenAIForwardResult, error) {
	if s == nil || account == nil {
		return nil, fmt.Errorf("grok voice service/account is required")
	}
	if account.Platform != PlatformGrok {
		return nil, fmt.Errorf("account platform %s is not supported for grok voice", account.Platform)
	}
	endpoint = strings.Trim(strings.TrimSpace(endpoint), "/")
	parts := strings.Split(endpoint, "/")
	baseEndpoint := parts[0]
	if _, ok := supportedGrokVoiceHTTPEndpoints[baseEndpoint]; !ok {
		return nil, fmt.Errorf("unsupported grok voice endpoint: %s", endpoint)
	}
	if len(parts) > 1 && baseEndpoint != "custom-voices" {
		return nil, fmt.Errorf("unsupported grok voice endpoint: %s", endpoint)
	}
	if baseEndpoint == "custom-voices" {
		if len(parts) > 3 || (len(parts) == 3 && parts[2] != "audio") {
			return nil, fmt.Errorf("unsupported grok voice endpoint: %s", endpoint)
		}
	}
	for _, part := range parts[1:] {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "?#\\") {
			return nil, fmt.Errorf("invalid grok voice endpoint path")
		}
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
	method := http.MethodPost
	if c != nil && c.Request != nil && strings.TrimSpace(c.Request.Method) != "" {
		method = c.Request.Method
	}
	req, err := http.NewRequestWithContext(upstreamCtx, method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json, audio/*")
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	// 与媒体路径一致，仅对 CLI chat proxy 写入 CLI 身份请求头；官方 api.x.ai Voice
	// 在携带这些请求头时可能拒绝或错误处理 OAuth。
	if account.IsGrokOAuth() && isGrokCLIProxyTarget(targetURL) {
		applyGrokCLIHeaders(req.Header)
	}
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	started := time.Now()
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(started).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return s.handleGrokMediaErrorResponse(ctx, resp, c, account, resp.Header.Get("x-request-id"), endpoint)
	}
	data, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	writeGrokMediaResponse(c, resp, data, s.responseHeaderFilter)
	audioUsage := estimateGrokVoiceAudioUsage(baseEndpoint, body, contentType, data, time.Since(started))
	upstreamID := firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id"))
	return &OpenAIForwardResult{
		// 强制生成持久结算 ID，避免 usage_billing_dedup 因复用客户端 ID 合并调用。
		RequestID:     StableGrokAudioBillingRequestID(upstreamID),
		Model:         baseEndpoint,
		UpstreamModel: baseEndpoint,
		Duration:      time.Since(started),
		AudioUsage:    audioUsage,
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
	base, err := buildGrokVoiceURL(account, s.cfg, "realtime")
	if err != nil {
		return false, err
	}
	u, err := url.Parse(base)
	if err != nil {
		return false, err
	}
	u.Scheme = "wss"
	u.RawQuery = "model=" + url.QueryEscape(firstNonEmpty(model, "grok-voice-latest"))
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	// 与媒体和 Voice HTTP 一致，仅对 CLI proxy 主机写入 CLI 请求头。
	if account.IsGrokOAuth() && isGrokCLIProxyTarget(u.String()) {
		applyGrokCLIHeaders(headers)
	}
	if account != nil {
		account.ApplyHeaderOverrides(headers)
	}

	dialer := s.getOpenAIWSPassthroughDialer()
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstream, _, _, err := dialer.Dial(ctx, u.String(), headers, proxyURL, s.resolveOpenAITLSProfile(account))
	if err != nil {
		return false, err
	}
	defer func() { _ = upstream.Close() }()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)
	var audioObserved atomic.Bool

	// 上游到客户端。
	go func() {
		for {
			msg, readErr := upstream.ReadMessage(ctx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if grokRealtimeEventHasAudio(msg) {
				audioObserved.Store(true)
			}
			if writeErr := client.Write(ctx, coderws.MessageText, msg); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()

	// 客户端到上游，仅接受 JSON 事件。
	go func() {
		for {
			kind, msg, readErr := client.Read(ctx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if kind != coderws.MessageText && kind != coderws.MessageBinary {
				continue
			}
			if grokRealtimeEventHasAudio(msg) {
				audioObserved.Store(true)
			}
			var raw json.RawMessage
			if unmarshalErr := json.Unmarshal(msg, &raw); unmarshalErr != nil {
				errCh <- fmt.Errorf("invalid realtime event: %w", unmarshalErr)
				return
			}
			if writeErr := upstream.WriteJSON(ctx, raw); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()

	return awaitGrokRealtimeAudioObserved(errCh, &audioObserved)
}

// awaitGrokRealtimeAudioObserved 在任一中继方向结束时返回本次会话是否真正传输过音频。
func awaitGrokRealtimeAudioObserved(errCh <-chan error, audioObserved *atomic.Bool) (bool, error) {
	err := <-errCh
	if audioObserved == nil {
		return false, err
	}
	return audioObserved.Load(), err
}

// grokRealtimeEventHasAudio 仅把包含非空音频负载的事件视为可计费音频，转录文本不计入。
func grokRealtimeEventHasAudio(msg []byte) bool {
	if !gjson.ValidBytes(msg) {
		return false
	}
	eventType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(msg, "type").String()))
	if !strings.Contains(eventType, "audio") || strings.Contains(eventType, "transcript") {
		return false
	}
	for _, path := range []string{"audio", "delta", "data"} {
		value := gjson.GetBytes(msg, path)
		if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
			return true
		}
	}
	return false
}

// estimateGrokVoiceAudioUsage 从请求和响应推导计费单位：TTS 按百万字符，
// STT 在时长未知时按请求体大小估算小时数，自定义 Voice 不产生用量。
func estimateGrokVoiceAudioUsage(endpoint string, reqBody []byte, contentType string, respBody []byte, elapsed time.Duration) *AudioUsage {
	switch strings.TrimSpace(endpoint) {
	case "tts":
		// 优先读取 JSON input/text 字段，否则回退到原始请求体长度。
		chars := 0
		if gjson.ValidBytes(reqBody) {
			for _, key := range []string{"input", "text", "prompt"} {
				if s := strings.TrimSpace(gjson.GetBytes(reqBody, key).String()); s != "" {
					chars = len([]rune(s))
					break
				}
			}
		}
		if chars <= 0 {
			chars = len(reqBody)
		}
		if chars <= 0 {
			return nil
		}
		return &AudioUsage{Mode: "tts", DurationOrUnits: float64(chars) / 1_000_000.0}
	case "stt":
		// 优先采用响应时长，不能只信任客户端 duration_seconds；同时以请求体估算和实际耗时作为下限。
		secs := 0.0
		if gjson.ValidBytes(respBody) {
			for _, path := range []string{"duration", "duration_seconds", "audio_duration", "usage.seconds"} {
				if v := gjson.GetBytes(respBody, path); v.Exists() && v.Type == gjson.Number && v.Float() > 0 {
					secs = v.Float()
					break
				}
			}
		}
		// multipart 请求按压缩语音约 16KB/s 估算保守下限。
		sizeFloor := 0.0
		if len(reqBody) > 0 {
			sizeFloor = float64(len(reqBody)) / 16000.0
		}
		clientSecs := 0.0
		if gjson.ValidBytes(reqBody) {
			if v := gjson.GetBytes(reqBody, "duration_seconds"); v.Exists() && v.Type == gjson.Number {
				clientSecs = v.Float()
			}
		}
		if secs <= 0 {
			secs = elapsed.Seconds()
		}
		if secs <= 0 {
			secs = clientSecs
		}
		if secs <= 0 {
			secs = sizeFloor
		}
		// 客户端时长明显低于请求体或实际耗时下限时，采用更大的下限防止少计费。
		if clientSecs > 0 && secs == clientSecs {
			floor := sizeFloor
			if elapsed.Seconds() > floor {
				floor = elapsed.Seconds()
			}
			if floor > 0 && clientSecs < floor*0.5 {
				secs = floor
			}
		}
		if secs <= 0 {
			return nil
		}
		return &AudioUsage{Mode: "stt", DurationOrUnits: secs / 3600.0}
	case "realtime":
		mins := elapsed.Minutes()
		if mins <= 0 {
			return nil
		}
		return &AudioUsage{Mode: "realtime", DurationOrUnits: mins}
	default:
		return nil
	}
}
