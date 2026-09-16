// 模型列表只解析当前账号实际可见的 ID，不替换模型默认目录或缓存资格。
package grok

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

type ModelsRequest struct {
	URL, Token, UserID, Email string
	OAuth                     bool
	ApplyOverrides            func(http.Header)
	Do                        func(*http.Request) (*http.Response, error)
}

func FetchObservedModels(ctx context.Context, o ModelsRequest) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", DefaultGrokUpstreamUserAgent())
	if o.OAuth && (MediaCodec{}).IsGrokCLIProxyTarget(req.URL.String()) {
		ApplyCLIHeaders(req.Header)
		if v := strings.TrimSpace(o.UserID); v != "" {
			req.Header.Set("X-UserID", v)
		}
		if v := strings.TrimSpace(o.Email); v != "" {
			req.Header.Set("X-Email", v)
		}
	}
	if o.ApplyOverrides != nil {
		o.ApplyOverrides(req.Header)
	}
	if o.OAuth {
		ApplyCLIProxyHeaders(req)
	}
	resp, err := o.Do(req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, nil
	}
	return ExtractModelIDs(body), nil
}
func ExtractModelIDs(body []byte) []string {
	data := gjson.GetBytes(body, "data")
	if !data.IsArray() {
		// 兼容部分网关直接返回数组。
		data = gjson.ParseBytes(body)
	}
	seen := make(map[string]struct{})
	var out []string
	data.ForEach(func(_, v gjson.Result) bool {
		id := strings.TrimSpace(v.Get("id").String())
		if id == "" {
			id = strings.TrimSpace(v.String())
		}
		if id == "" {
			return true
		}
		if _, ok := seen[id]; ok {
			return true
		}
		seen[id] = struct{}{}
		out = append(out, id)
		return true
	})
	return out
}
