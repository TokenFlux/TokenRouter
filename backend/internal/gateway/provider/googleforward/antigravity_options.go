package googleforward

import (
	"context"
	"io"
	"net/http"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

func (s *Antigravity) upstreamErrorBodyReadLimit() int64 {
	limit := int64(512 << 10)
	if s != nil && s.Options.Configured && s.Options.LogErrorBody && s.Options.LogErrorBodyMaxBytes > int(limit) {
		limit = int64(s.Options.LogErrorBodyMaxBytes)
	}
	return limit
}

func (s *Antigravity) readUpstreamErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, s.upstreamErrorBodyReadLimit()))
	return body
}

// getLogConfig 获取上游错误日志配置
// 返回是否记录日志体和最大字节数
func (s *Antigravity) getLogConfig() (logBody bool, maxBytes int) {
	maxBytes = 2048 // 默认值
	if !s.Options.Configured {
		return false, maxBytes
	}
	cfg := s.Options
	if cfg.LogErrorBodyMaxBytes > 0 {
		maxBytes = cfg.LogErrorBodyMaxBytes
	}
	return cfg.LogErrorBody, maxBytes
}

// getUpstreamErrorDetail 获取上游错误详情（用于日志记录）
func (s *Antigravity) getUpstreamErrorDetail(body []byte) string {
	logBody, maxBytes := s.getLogConfig()
	if !logBody {
		return ""
	}
	return logredact.TruncateUTF8(string(body), maxBytes)
}

// getMappedModel 获取映射后的模型名
// 完全依赖映射配置：账户映射（通配符）→ 默认映射兜底
func (s *Antigravity) getMappedModel(account *gatewayprovider.ExecutionAccount, requestedModel string) string {
	return accountprovider.MapAntigravityModel(gatewayprovider.ExecutionRecord(account), requestedModel)
}

func resolveAntigravityProjectID(account *gatewayprovider.ExecutionAccount) (string, error) {
	return accountcore.ResolveAntigravityProjectID(gatewayprovider.ExecutionRecord(account), antigravity.ErrProjectIDRequired)
}

func (s *Antigravity) getClaudeTransformOptions(ctx context.Context) antigravity.TransformOptions {
	opts := antigravity.DefaultTransformOptions()
	if s.Gateway == nil {
		return opts
	}
	opts.EnableIdentityPatch = s.Gateway.IsIdentityPatchEnabled(ctx)
	opts.IdentityPatch = s.Gateway.GetIdentityPatchPrompt(ctx)
	return opts
}
