//go:build integration

package account_test

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// 真实 JSONB 合并验证逐账号协议补丁与凭据轮换在同一 SQL 中提交。
func (s *AccountRepoSuite) TestUnifiedProtocolBulkUpdate() {
	first := &account.Record{Name: "protocol-one", Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey, Status: billing.StatusActive, Credentials: map[string]any{"api_key": "before", "upstream_protocols": []string{"anthropic_messages"}}, Extra: map[string]any{"keep": true}}
	second := &account.Record{Name: "protocol-two", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: billing.StatusActive, Credentials: map[string]any{"api_key": "before", "upstream_protocols": []string{"openai_responses"}}, Extra: map[string]any{"keep": true}}
	s.Require().NoError(s.repo.Create(s.ctx, first))
	s.Require().NoError(s.repo.Create(s.ctx, second))
	count, err := s.repo.BulkUpdate(s.ctx, []int64{first.ID, second.ID}, account.AccountBulkUpdate{Credentials: map[string]any{"api_key": "after"}, ProtocolUpdates: map[int64]map[string]any{first.ID: {"upstream_protocols": []string{"openai_chat_completions"}, "api_base_urls": map[string]any{"chat_completions": "https://relay.example"}}, second.ID: {"upstream_protocols": []string{"openai_embeddings"}}}})
	s.Require().NoError(err)
	s.Require().Equal(int64(2), count)
	got, err := s.repo.GetByID(s.ctx, first.ID)
	s.Require().NoError(err)
	s.Require().Equal("after", got.GetCredential("api_key"))
	s.Require().Equal([]protocol.ProtocolID{protocol.ProtocolOpenAIChatCompletions}, got.UpstreamProtocols())
	got, err = s.repo.GetByID(s.ctx, second.ID)
	s.Require().NoError(err)
	s.Require().Equal([]protocol.ProtocolID{"openai_embeddings"}, got.UpstreamProtocols())
	s.Require().Equal(true, got.Extra["keep"])
	_, err = s.repo.BulkUpdate(s.ctx, []int64{first.ID}, account.AccountBulkUpdate{ProtocolUpdates: map[int64]map[string]any{first.ID: {"upstream_protocols": []string{}}}, Extra: map[string]any{"openai_text_route_mode": "force_responses"}})
	s.Require().NoError(err)
	got, err = s.repo.GetByID(s.ctx, first.ID)
	s.Require().NoError(err)
	s.Require().Empty(got.UpstreamProtocols())
	s.Require().NotContains(got.Extra, "openai_text_route_mode")
}
