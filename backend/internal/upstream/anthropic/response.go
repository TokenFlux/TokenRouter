// 本文件保持 Anthropic 两种非流返回路径的独立语义，HTTP 写入由 OutputSink 完成。
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type ResponseOptions struct {
	StreamOptions
	ReadBody            func(io.Reader) ([]byte, error)
	InvalidJSON         func(context.Context, *http.Response, []byte, error) error
	PreserveContentType bool
	ForceCacheBilling   bool
}

func NonStreamResponse(ctx context.Context, resp *http.Response, c *upstream.OutputContext, options ResponseOptions, originalModel, mappedModel string) (*upstream.TokenUsage, error) {
	// 更新5h窗口状态
	if options.UpdateWindow != nil {
		options.UpdateWindow(ctx, resp.Header)
	}

	body, err := options.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}

	// 解析usage
	var response struct {
		Usage upstream.TokenUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return nil, options.InvalidJSON(ctx, resp, body, err)
		}
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if options.Observe != nil {
		options.Observe(wire.ObserveMessage(string(body)))
	}
	// 解析嵌套的 cache_creation 对象中的 5m/1h 明细
	cc5m := gjson.GetBytes(body, "usage.cache_creation.ephemeral_5m_input_tokens")
	cc1h := gjson.GetBytes(body, "usage.cache_creation.ephemeral_1h_input_tokens")
	if cc5m.Exists() || cc1h.Exists() {
		response.Usage.CacheCreation5mTokens = int(cc5m.Int())
		response.Usage.CacheCreation1hTokens = int(cc1h.Int())
	}

	// 兼容 Kimi cached_tokens → cache_read_input_tokens
	if response.Usage.CacheReadInputTokens == 0 {
		cachedTokens := gjson.GetBytes(body, "usage.cached_tokens").Int()
		if cachedTokens > 0 {
			response.Usage.CacheReadInputTokens = int(cachedTokens)
			if newBody, err := sjson.SetBytes(body, "usage.cache_read_input_tokens", cachedTokens); err == nil {
				body = newBody
			}
		}
	}

	// Cache TTL Override: 重写 non-streaming 响应中的 cache_creation 分类。
	// 账号级设置优先；全局 1h 请求注入开启时，默认把 usage 计费归回 5m。
	if overrideTarget, ok := options.override(ctx); ok {
		if ApplyCacheTTLOverride(&response.Usage, overrideTarget) {
			// 同步更新 body JSON 中的嵌套 cache_creation 对象
			if newBody, err := sjson.SetBytes(body, "usage.cache_creation.ephemeral_5m_input_tokens", response.Usage.CacheCreation5mTokens); err == nil {
				body = newBody
			}
			if newBody, err := sjson.SetBytes(body, "usage.cache_creation.ephemeral_1h_input_tokens", response.Usage.CacheCreation1hTokens); err == nil {
				body = newBody
			}
		}
	}

	// 如果有模型映射，替换响应中的model字段
	if originalModel != mappedModel {
		body = ReplaceModelInResponseBody(body, mappedModel, originalModel)
	}

	if options.WriteHeaders != nil {
		options.WriteHeaders(c.Writer.Header(), resp.Header)
	}

	contentType := "application/json"
	if options.PreserveContentType {
		if upstreamType := resp.Header.Get("Content-Type"); upstreamType != "" {
			contentType = upstreamType
		}
	}

	body = RestoreToolNamesInBytes(body, options.ToolNames)

	// 写入响应
	observation := wire.ObserveMessage(string(body))
	c.NextEvent(observation.Semantic, true)
	c.Data(resp.StatusCode, contentType, body)

	return &response.Usage, nil
}
func NonStreamResponsePassthrough(
	ctx context.Context,
	resp *http.Response,
	c *upstream.OutputContext,
	options ResponseOptions,
) (*upstream.TokenUsage, error) {
	if options.UpdateWindow != nil {
		options.UpdateWindow(ctx, resp.Header)
	}

	body, err := options.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		var raw json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, options.InvalidJSON(ctx, resp, body, err)
		}
	}

	if options.Observe != nil {
		options.Observe(wire.ObserveMessage(string(body)))
	}
	usage := wire.ParseClaudeUsageFromResponseBody(body)
	if options.ForceCacheBilling && usage.InputTokens > 0 {
		body, err = ClassifyResponseInputAsCacheRead(body, usage)
		if err != nil {
			return nil, err
		}
	}

	if options.WriteHeaders != nil {
		options.WriteHeaders(c.Writer.Header(), resp.Header)
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	body = RestoreToolNamesInBytes(body, options.ToolNames)
	observation := wire.ObserveMessage(string(body))
	c.NextEvent(observation.Semantic, true)
	c.Data(resp.StatusCode, contentType, body)
	return usage, nil
}

// replaceModelInResponseBody 替换响应体中的model字段
// 使用 gjson/sjson 精确替换，避免全量 JSON 反序列化
func ReplaceModelInResponseBody(body []byte, fromModel, toModel string) []byte {
	if m := gjson.GetBytes(body, "model"); m.Exists() && m.Str == fromModel {
		newBody, err := sjson.SetBytes(body, "model", toModel)
		if err != nil {
			return body
		}
		return newBody
	}
	return body
}

// classifyAnthropicResponseInputAsCacheRead 将故障转移后的输入 token 归类为缓存读取。
func ClassifyResponseInputAsCacheRead(body []byte, usage *upstream.TokenUsage) ([]byte, error) {
	classified, err := sjson.SetBytes(body, "usage.input_tokens", 0)
	if err != nil {
		return nil, fmt.Errorf("classify forced cache billing input tokens: %w", err)
	}
	classified, err = sjson.SetBytes(classified, "usage.cache_read_input_tokens", usage.CacheReadInputTokens+usage.InputTokens)
	if err != nil {
		return nil, fmt.Errorf("classify forced cache billing cache read tokens: %w", err)
	}
	return classified, nil
}
