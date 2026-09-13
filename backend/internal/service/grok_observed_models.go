package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	"github.com/tidwall/gjson"
)

const (
	grokObservedModelsExtraKey = "grok_observed_models"
	grokObservedModelsTTL      = 6 * time.Hour
	grokObservedModelsTimeout  = 15 * time.Second
)

type grokObservedModelsSnapshot struct {
	Models    []string `json:"models"`
	FetchedAt string   `json:"fetched_at"`
	Source    string   `json:"source,omitempty"`
}

// scheduleGrokObservedModelsSync 尽力拉取 Grok OAuth 账号的上游 /v1/models 并把 ID 保存到 Extra；
// 调用方应在授权或探测成功后异步触发，不能长时间阻塞请求路径。
func (s *GrokQuotaService) scheduleGrokObservedModelsSync(account *Account) {
	if s == nil || account == nil || !account.IsGrokOAuth() || s.accountRepo == nil {
		return
	}
	// 后台任务持有独立记录，避免请求方修改嵌套凭据和 Extra。
	acc := AccountFromRecord(AccountRecordView(account))
	s.probeRuntime.Schedule("models:"+strconv.FormatInt(acc.ID, 10), grokObservedModelsTimeout, func(ctx context.Context) {
		if err := s.syncGrokObservedModels(ctx, acc); err != nil {
			slog.Debug("grok_observed_models_sync_failed", "account_id", acc.ID, "error", err)
		}
	})
}

func (s *GrokQuotaService) syncGrokObservedModels(ctx context.Context, account *Account) error {
	if s == nil || account == nil {
		return nil
	}
	// 快照仍新鲜时跳过同步。
	if snap := parseGrokObservedModels(account.Extra); snap != nil {
		if t, err := time.Parse(time.RFC3339, snap.FetchedAt); err == nil && time.Since(t) < grokObservedModelsTTL {
			return nil
		}
	}
	token := strings.TrimSpace(account.GetGrokAccessToken())
	if token == "" && s.tokenProvider != nil {
		// 尽力预热 token，避免强制刷新风暴。
		if at, err := s.tokenProvider.GetAccessToken(ctx, account); err == nil {
			token = strings.TrimSpace(at)
		}
	}
	if token == "" {
		return nil
	}
	baseURL := strings.TrimSpace(account.GetGrokBaseURL())
	if s.settingService != nil {
		baseURL = strings.TrimSpace(s.settingService.ResolveGrokBaseURL(ctx, account))
	}
	if baseURL == "" {
		baseURL = xai.DefaultCLIBaseURL
	}
	validator, err := grokBaseURLValidator(account, s.cfg)
	if err != nil {
		return err
	}
	validatedBaseURL, err := validator(baseURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildOpenAIModelsURL(validatedBaseURL), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", defaultGrokUpstreamUserAgent())
	if account.IsGrokOAuth() && isGrokCLIProxyTarget(req.URL.String()) {
		applyGrokCLIHeaders(req.Header)
		if userID := strings.TrimSpace(account.GetCredential("sub")); userID != "" {
			req.Header.Set("X-UserID", userID)
		}
		if email := strings.TrimSpace(account.GetCredential("email")); email != "" {
			req.Header.Set("X-Email", email)
		}
	}
	account.ApplyHeaderOverrides(req.Header)
	if account.IsGrokOAuth() {
		// CLI 身份头必须在账号覆写之后写入，避免配置意外破坏上游协议身份。
		xai.ApplyCLIProxyHeaders(req)
	}

	proxyURL := ""
	if s.proxyRepo != nil && account.ProxyID != nil {
		if p, err := s.proxyRepo.GetByID(ctx, *account.ProxyID); err == nil && p != nil {
			proxyURL = p.URL()
		}
	}
	if s.httpUpstream == nil {
		return nil
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return nil
	}
	ids := extractGrokModelIDsFromModelsBody(body)
	if len(ids) == 0 {
		return nil
	}
	snap := grokObservedModelsSnapshot{
		Models:    ids,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Source:    "upstream_v1_models",
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
		grokObservedModelsExtraKey: asMap,
	})
}

func extractGrokModelIDsFromModelsBody(body []byte) []string {
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

func parseGrokObservedModels(extra map[string]any) *grokObservedModelsSnapshot {
	if extra == nil {
		return nil
	}
	raw, ok := extra[grokObservedModelsExtraKey]
	if !ok || raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var snap grokObservedModelsSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil
	}
	if len(snap.Models) == 0 {
		return nil
	}
	return &snap
}
