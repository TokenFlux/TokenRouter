// 图片回填不带账号实体，只接收技术参数与已判定的开关。
// @project-doc docs/interfaces/openai_upstream.md#images_url_backfill
package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress/urlpolicy"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const openAIImageURLDownloadTimeout = 60 * time.Second

type ImageBackfillOptions struct {
	Enabled        bool
	Stream         bool
	ResponseFormat string
	ValidateURL    func(string) (string, error)
	Do             func(*http.Request) (*http.Response, error)
	Failure        func(int)
}

func (options ImageBackfillOptions) ReportFailure(index int) {
	if options.Failure != nil {
		options.Failure(index)
	}
}
func (options ImageBackfillOptions) Backfill(ctx context.Context, body []byte) []byte {
	if !options.Enabled || !gjson.ValidBytes(body) || (options.Stream || strings.EqualFold(strings.TrimSpace(options.ResponseFormat), "url")) {
		return body
	}
	items := gjson.GetBytes(body, "data")
	if !items.IsArray() {
		return body
	}
	for index, item := range items.Array() {
		if !item.IsObject() || strings.TrimSpace(item.Get("b64_json").String()) != "" {
			continue
		}
		rawURL := strings.TrimSpace(item.Get("url").String())
		if rawURL == "" {
			continue
		}
		encoded, err := options.FetchBase64(ctx, rawURL)
		if err != nil {
			// 下载错误可能含带签名的 URL，日志只记录阶段与条目，不记录原始地址。
			options.ReportFailure(index)
			continue
		}
		updated, err := sjson.SetBytes(body, fmt.Sprintf("data.%d.b64_json", index), encoded)
		if err != nil {
			// 响应写回失败时同样保留原始图片项。
			options.ReportFailure(index)
			continue
		}
		body = updated
	}
	return body
}

// fetchOpenAIImageURLBase64 下载图片 URL 并返回标准 base64。
// 目的地和每次重定向都必须是公网主机，内容类型只信字节嗅探结果。
func (options ImageBackfillOptions) FetchBase64(ctx context.Context, rawURL string) (string, error) {
	if strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		if encoded := NormalizeOpenAIImageBase64(rawURL); encoded != "" {
			if len(encoded) > base64.StdEncoding.EncodedLen(int(OpenAIImageMaxDownloadBytes)) {
				return "", errors.New("data url image exceeds size limit")
			}
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err == nil && int64(len(data)) <= OpenAIImageMaxDownloadBytes && IsBackfillImageContent(data) {
				return encoded, nil
			}
		}
		return "", errors.New("data url payload is not valid base64")
	}
	if options.Do == nil {
		return "", errors.New("http upstream is not configured")
	}
	// URL 校验器为 base URL 规范化路径；下载必须保留签名 URL 的原始转义和尾斜杠。
	downloadURL := strings.TrimSpace(rawURL)
	if _, err := options.ValidateURL(downloadURL); err != nil {
		return "", fmt.Errorf("invalid image url: %w", err)
	}
	if err := RejectPrivateImageHost(downloadURL); err != nil {
		return "", err
	}
	downloadCtx, cancel := context.WithTimeout(upstream.WithHTTPUpstreamPublicHostsOnly(ctx), openAIImageURLDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("build image download request: %w", err)
	}
	req.Header.Set("Accept", "image/*,*/*;q=0.8")
	resp, err := options.Do(req)
	if err != nil {
		return "", fmt.Errorf("download image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("download image: unexpected status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, OpenAIImageMaxDownloadBytes+1))
	if err != nil {
		return "", fmt.Errorf("read image body: %w", err)
	}
	if int64(len(data)) > OpenAIImageMaxDownloadBytes {
		return "", fmt.Errorf("downloaded image exceeds %d bytes", OpenAIImageMaxDownloadBytes)
	}
	if !IsBackfillImageContent(data) {
		return "", errors.New("download image: content is not an allowed image format")
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// RejectPrivateImageHost 拒绝 URL 中直接写出的内网、回环和未指定地址。
func RejectPrivateImageHost(downloadURL string) error {
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		return fmt.Errorf("invalid image url: %w", err)
	}
	if host := parsed.Hostname(); urlpolicy.IsBlockedHost(host) {
		return fmt.Errorf("image url host is not allowed: %s", host)
	}
	return nil
}

var openAIImageBackfillContentTypes = map[string]struct{}{
	"image/png": {}, "image/jpeg": {}, "image/webp": {}, "image/gif": {},
}

// IsBackfillImageContent 只允许常见位图格式，避免把 SVG/HTML 等内容写入 b64_json。
func IsBackfillImageContent(data []byte) bool {
	_, ok := openAIImageBackfillContentTypes[strings.ToLower(http.DetectContentType(data))]
	return ok
}
