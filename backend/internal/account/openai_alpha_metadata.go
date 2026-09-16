// PAT 搜索元数据属于账号授权，供应商校验仍通过既有原生客户端端口执行。
package account

import (
	"context"
	"fmt"
	"maps"
	"strings"
)

// OpenAIAlphaMetadataPorts 保留旧调用方先更新请求副本、再持久化的顺序。
type OpenAIAlphaMetadataPorts struct {
	Apply   func(map[string]any)
	Persist func(context.Context, map[string]any) error
}

// EnsureAlphaSearchMetadata 只补全缺失的 PAT 元数据，不改变原已有元数据或其它认证模式。
func (s *OpenAIAuthorization) EnsureAlphaSearchMetadata(ctx context.Context, record *Record, token, proxyURL string, ports OpenAIAlphaMetadataPorts) error {
	if record == nil || !record.IsOpenAIPersonalAccessToken() || strings.TrimSpace(record.GetChatGPTAccountID()) != "" {
		return nil
	}
	ctx, done, err := s.activity.begin(ctx, ErrProbeStopped)
	if err != nil {
		return err
	}
	defer done()
	info, err := s.Options.ValidatePAT(ctx, token, proxyURL)
	if err != nil {
		return fmt.Errorf("validate Codex PAT metadata for alpha/search: %w", err)
	}
	credentials := maps.Clone(record.Credentials)
	if credentials == nil {
		credentials = make(map[string]any)
	}
	for key, value := range BuildOpenAIAccountCredentials(info) {
		credentials[key] = value
	}
	credentials = NormalizeOpenAIPersonalAccessTokenCredentials(record, info, credentials)
	record.Credentials = maps.Clone(credentials)
	ports.Apply(maps.Clone(credentials))
	if ports.Persist != nil {
		if err := ports.Persist(ctx, credentials); err != nil {
			return fmt.Errorf("persist Codex PAT metadata for alpha/search: %w", err)
		}
	}
	return nil
}
