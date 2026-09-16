package bedrock

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// handleBedrockStreamingResponse 处理 Bedrock InvokeModelWithResponseStream 的 EventStream 响应
// Bedrock 返回 AWS EventStream 二进制格式，每个事件的 payload 中 chunk.bytes 是 base64 编码的
// Claude SSE 事件 JSON。本方法解码后转换为标准 SSE 格式写入客户端。
func StreamResponse(
	ctx context.Context,
	resp *http.Response,
	c *upstream.OutputContext,
	options StreamOptions,
	startTime time.Time,
	model string,
) (*StreamResult, error) {
	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	if v := resp.Header.Get("x-amzn-requestid"); v != "" {
		c.Header("x-request-id", v)
	}

	usage := &upstream.TokenUsage{}
	var firstTokenMs *int
	clientDisconnected := false

	// Bedrock EventStream 使用 application/vnd.amazon.eventstream 二进制格式。
	// 每个帧结构：total_length(4) + headers_length(4) + prelude_crc(4) + headers + payload + message_crc(4)
	// 但更实用的方式是使用行扫描找 JSON chunks，因为 Bedrock 的响应在二进制帧中。
	// 我们使用 EventStream decoder 来正确解析。
	decoder := NewBedrockEventStreamDecoder(resp.Body)

	type decodeEvent struct {
		payload []byte
		err     error
	}
	events := make(chan decodeEvent, 16)
	done := make(chan struct{})
	sendEvent := func(ev decodeEvent) bool {
		select {
		case events <- ev:
			return true
		case <-done:
			return false
		}
	}
	var lastReadAt atomic.Int64
	lastReadAt.Store(time.Now().UnixNano())

	go func() {
		defer close(events)
		for {
			payload, err := decoder.Decode()
			if err != nil {
				if err == io.EOF {
					return
				}
				_ = sendEvent(decodeEvent{err: err})
				return
			}
			lastReadAt.Store(time.Now().UnixNano())
			if !sendEvent(decodeEvent{payload: payload}) {
				return
			}
		}
	}()
	defer close(done)

	streamInterval := options.Interval
	var intervalTicker *time.Ticker
	if streamInterval > 0 {
		intervalTicker = time.NewTicker(streamInterval)
		defer intervalTicker.Stop()
	}
	var intervalCh <-chan time.Time
	if intervalTicker != nil {
		intervalCh = intervalTicker.C
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if !clientDisconnected {
					flusher.Flush()
				}
				return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: clientDisconnected}, nil
			}
			if ev.err != nil {
				if clientDisconnected {
					return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: true}, nil
				}
				if errors.Is(ev.err, context.Canceled) || errors.Is(ev.err, context.DeadlineExceeded) {
					return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: true}, nil
				}
				return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs}, fmt.Errorf("bedrock stream read error: %w", ev.err)
			}

			// payload 是 JSON，提取 chunk.bytes（base64 编码的 Claude SSE 事件数据）
			sseData := ExtractBedrockChunkData(ev.payload)
			if sseData == nil {
				continue
			}

			if firstTokenMs == nil {
				ms := int(time.Since(startTime).Milliseconds())
				firstTokenMs = &ms
			}

			// 转换 Bedrock 特有的 amazon-bedrock-invocationMetrics 为标准 Anthropic usage 格式
			// 同时移除该字段避免透传给客户端
			sseData = TransformBedrockInvocationMetrics(sseData)

			observation := anthropic.ObserveEvent(string(sseData))
			if options.Observe != nil {
				options.Observe(observation)
			}
			c.NextEvent(observation.Semantic, observation.Terminal)
			// 解析 SSE 事件数据提取 usage
			anthropic.ParseSSEUsagePassthrough(string(sseData), usage)

			// 确定 SSE event type
			eventType := gjson.GetBytes(sseData, "type").String()

			// 写入标准 SSE 格式
			if !clientDisconnected {
				var writeErr error
				if eventType != "" {
					_, writeErr = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, sseData)
				} else {
					_, writeErr = fmt.Fprintf(w, "data: %s\n\n", sseData)
				}
				if writeErr != nil {
					clientDisconnected = true
					logger.LegacyPrintf("service.gateway", "[Bedrock] Client disconnected during streaming, continue draining for usage: account=%d", options.AccountID)
				} else {
					flusher.Flush()
				}
			}

		case <-intervalCh:
			lastRead := time.Unix(0, lastReadAt.Load())
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if clientDisconnected {
				return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: true}, nil
			}
			logger.LegacyPrintf("service.gateway", "[Bedrock] Stream data interval timeout: account=%d model=%s interval=%s", options.AccountID, model, streamInterval)
			if options.OnTimeout != nil {
				options.OnTimeout(ctx, model)
			}
			return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs}, fmt.Errorf("stream data interval timeout")
		}
	}
}

// ExtractBedrockChunkData 从 Bedrock EventStream payload 中提取 Claude SSE 事件数据
// Bedrock payload 格式：{"bytes":"<base64-encoded-json>"}
func ExtractBedrockChunkData(payload []byte) []byte {
	b64 := gjson.GetBytes(payload, "bytes").String()
	if b64 == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil
	}
	return decoded
}

// TransformBedrockInvocationMetrics 将 Bedrock 特有的 amazon-bedrock-invocationMetrics
// 转换为标准 Anthropic usage 格式，并从 SSE 数据中移除该字段。
//
// Bedrock Invoke 返回的 message_delta 事件可能包含：
//
//	{"type":"message_delta","delta":{...},"amazon-bedrock-invocationMetrics":{"inputTokenCount":150,"outputTokenCount":42}}
//
// 转换为：
//
//	{"type":"message_delta","delta":{...},"usage":{"input_tokens":150,"output_tokens":42}}
func TransformBedrockInvocationMetrics(data []byte) []byte {
	metrics := gjson.GetBytes(data, "amazon-bedrock-invocationMetrics")
	if !metrics.Exists() || !metrics.IsObject() {
		return data
	}

	// 移除 Bedrock 特有字段
	data, _ = sjson.DeleteBytes(data, "amazon-bedrock-invocationMetrics")

	// 如果已有标准 usage 字段，不覆盖
	if gjson.GetBytes(data, "usage").Exists() {
		return data
	}

	// 转换 camelCase → snake_case 写入 usage
	inputTokens := metrics.Get("inputTokenCount")
	outputTokens := metrics.Get("outputTokenCount")
	if inputTokens.Exists() {
		data, _ = sjson.SetBytes(data, "usage.input_tokens", inputTokens.Int())
	}
	if outputTokens.Exists() {
		data, _ = sjson.SetBytes(data, "usage.output_tokens", outputTokens.Int())
	}

	return data
}

// BedrockEventStreamDecoder 解码 AWS EventStream 二进制帧
// EventStream 帧格式：
//
//	[total_byte_length: 4 bytes]
//	[headers_byte_length: 4 bytes]
//	[prelude_crc: 4 bytes]
//	[headers: variable]
//	[payload: variable]
//	[message_crc: 4 bytes]
type BedrockEventStreamDecoder struct {
	reader *bufio.Reader
}

func NewBedrockEventStreamDecoder(r io.Reader) *BedrockEventStreamDecoder {
	return &BedrockEventStreamDecoder{
		reader: bufio.NewReaderSize(r, 64*1024),
	}
}

// Decode 读取下一个 EventStream 帧并返回 chunk 类型事件的 payload
func (d *BedrockEventStreamDecoder) Decode() ([]byte, error) {
	for {
		// 读取 prelude: total_length(4) + headers_length(4) + prelude_crc(4) = 12 bytes
		prelude := make([]byte, 12)
		if _, err := io.ReadFull(d.reader, prelude); err != nil {
			return nil, err
		}

		// 验证 prelude CRC（AWS EventStream 使用标准 CRC32 / IEEE）
		preludeCRC := BedrockReadUint32(prelude[8:12])
		if crc32.Checksum(prelude[0:8], Crc32IEEETable) != preludeCRC {
			return nil, fmt.Errorf("eventstream prelude CRC mismatch")
		}

		totalLength := BedrockReadUint32(prelude[0:4])
		headersLength := BedrockReadUint32(prelude[4:8])

		if totalLength < 16 { // minimum: 12 prelude + 4 message_crc
			return nil, fmt.Errorf("invalid eventstream frame: total_length=%d", totalLength)
		}

		// 读取 headers + payload + message_crc
		remaining := int(totalLength) - 12
		if remaining <= 0 {
			continue
		}
		data := make([]byte, remaining)
		if _, err := io.ReadFull(d.reader, data); err != nil {
			return nil, err
		}

		// 验证 message CRC（覆盖 prelude + headers + payload）
		messageCRC := BedrockReadUint32(data[len(data)-4:])
		h := crc32.New(Crc32IEEETable)
		_, _ = h.Write(prelude)
		_, _ = h.Write(data[:len(data)-4])
		if h.Sum32() != messageCRC {
			return nil, fmt.Errorf("eventstream message CRC mismatch")
		}

		// 解析 headers
		headers := data[:headersLength]
		payload := data[headersLength : len(data)-4] // 去掉 message_crc

		// 从 headers 中提取 :event-type
		eventType := ExtractEventStreamHeaderValue(headers, ":event-type")

		// 只处理 chunk 事件
		if eventType == "chunk" {
			// payload 是完整的 JSON，包含 bytes 字段
			return payload, nil
		}

		// 检查异常事件
		exceptionType := ExtractEventStreamHeaderValue(headers, ":exception-type")
		if exceptionType != "" {
			return nil, fmt.Errorf("bedrock exception: %s: %s", exceptionType, string(payload))
		}

		messageType := ExtractEventStreamHeaderValue(headers, ":message-type")
		if messageType == "exception" || messageType == "error" {
			return nil, fmt.Errorf("bedrock error: %s", string(payload))
		}

		// 跳过其他事件类型（如 initial-response）
	}
}

// ExtractEventStreamHeaderValue 从 EventStream headers 二进制数据中提取指定 header 的字符串值
// EventStream header 格式：
//
//	[name_length: 1 byte][name: variable][value_type: 1 byte][value: variable]
//
// value_type = 7 表示 string 类型，前 2 bytes 为长度
func ExtractEventStreamHeaderValue(headers []byte, targetName string) string {
	pos := 0
	for pos < len(headers) {
		if pos >= len(headers) {
			break
		}
		nameLen := int(headers[pos])
		pos++
		if pos+nameLen > len(headers) {
			break
		}
		name := string(headers[pos : pos+nameLen])
		pos += nameLen

		if pos >= len(headers) {
			break
		}
		valueType := headers[pos]
		pos++

		switch valueType {
		case 7: // string
			if pos+2 > len(headers) {
				return ""
			}
			valueLen := int(BedrockReadUint16(headers[pos : pos+2]))
			pos += 2
			if pos+valueLen > len(headers) {
				return ""
			}
			value := string(headers[pos : pos+valueLen])
			pos += valueLen
			if name == targetName {
				return value
			}
		case 0: // bool true
			if name == targetName {
				return "true"
			}
		case 1: // bool false
			if name == targetName {
				return "false"
			}
		case 2: // byte
			pos++
			if name == targetName {
				return ""
			}
		case 3: // short
			pos += 2
			if name == targetName {
				return ""
			}
		case 4: // int
			pos += 4
			if name == targetName {
				return ""
			}
		case 5: // long
			pos += 8
			if name == targetName {
				return ""
			}
		case 6: // bytes
			if pos+2 > len(headers) {
				return ""
			}
			valueLen := int(BedrockReadUint16(headers[pos : pos+2]))
			pos += 2 + valueLen
		case 8: // timestamp
			pos += 8
		case 9: // uuid
			pos += 16
		default:
			return "" // 未知类型，无法继续解析
		}
	}
	return ""
}

// Crc32IEEETable is the CRC32 / IEEE table used by AWS EventStream.
var Crc32IEEETable = crc32.MakeTable(crc32.IEEE)

func BedrockReadUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func BedrockReadUint16(b []byte) uint16 {
	return uint16(b[0])<<8 | uint16(b[1])
}

// StreamOptions 只包含执行技术参数与外层观测回调，不接收账号或配置实体。
type StreamOptions struct {
	Observe   func(anthropic.Observation)
	AccountID int64
	Interval  time.Duration
	OnTimeout func(context.Context, string)
}
type StreamResult struct {
	Usage            *upstream.TokenUsage
	FirstTokenMs     *int
	ClientDisconnect bool
}
