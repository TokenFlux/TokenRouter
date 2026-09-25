//go:build integration

package routing_test

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// 通过路由存储验证分组字段的保存与回读。
func (s *GroupRepoSuite) TestUnifiedProtocolRoundTrip() {
	original := &routing.Group{Name: "protocol-group", Platform: capability.PlatformOpenAI, Status: billing.StatusActive, RateMultiplier: 1, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}, ProtocolFallbacks: map[protocol.ProtocolID]protocol.ProtocolID{protocol.ProtocolAnthropicMessages: protocol.ProtocolOpenAIResponses}, ResponsesImagePolicy: "disabled"}
	s.Require().NoError(s.repo.Create(s.ctx, original))
	got, err := s.repo.GetByIDLite(s.ctx, original.ID)
	s.Require().NoError(err)
	s.Require().Equal(original.AllowedProtocols, got.AllowedProtocols)
	s.Require().Equal(original.ProtocolFallbacks, got.ProtocolFallbacks)
	s.Require().Equal("disabled", got.ResponsesImagePolicy)
	got.AllowedProtocols = []protocol.ProtocolID{}
	got.ProtocolFallbacks = map[protocol.ProtocolID]protocol.ProtocolID{}
	got.ResponsesImagePolicy = "block"
	s.Require().NoError(s.repo.Update(s.ctx, got))
	got, err = s.repo.GetByID(s.ctx, original.ID)
	s.Require().NoError(err)
	s.Require().Empty(got.AllowedProtocols)
	s.Require().Empty(got.ProtocolFallbacks)
	s.Require().Equal("block", got.ResponsesImagePolicy)
}
