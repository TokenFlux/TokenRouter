package handler

import (
	"context"
	slog "log/slog"
	time "time"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/service"
)

// 剩余转发测试的构造夹具；不再用于模型目录 HTTP。
type gatewayExecutionAccountRows struct {
	gatewayprovider.ExecutionAccountStore

	byGroup map[int64][]gatewayprovider.ExecutionAccount
}

type gatewayExecutionChannelRows struct {
	routing.ChannelRepository

	channels       []routing.Channel
	groupPlatforms map[int64]string
}

func (s *gatewayExecutionChannelRows) ListAll(ctx context.Context) ([]routing.Channel, error) {
	channels := make([]routing.Channel, len(s.channels))
	copy(channels, s.channels)
	return channels, nil
}

func (s *gatewayExecutionChannelRows) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
	platforms := make(map[int64]string, len(groupIDs))
	for _, groupID := range groupIDs {
		if platform, ok := s.groupPlatforms[groupID]; ok {
			platforms[groupID] = platform
		}
	}
	return platforms, nil
}

func (s *gatewayExecutionAccountRows) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]gatewayprovider.ExecutionAccount, error) {
	accounts, ok := s.byGroup[groupID]
	if !ok {
		return nil, nil
	}
	out := make([]gatewayprovider.ExecutionAccount, len(accounts))
	copy(out, accounts)
	return out, nil
}

func newGatewayExecutionHandlerForTest(repo gatewayprovider.ExecutionAccountStore) *GatewayHandler {
	return newGatewayExecutionHandlerWithChannelForTest(repo, nil)
}

func newGatewayExecutionHandlerWithChannelForTest(repo gatewayprovider.ExecutionAccountStore, channelService *routing.ChannelService) *GatewayHandler {
	return &GatewayHandler{
		gatewayService: service.NewGatewayService(
			repo,
			nil, nil, nil, nil, nil, nil, nil,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, channelService, nil, responseHeaderFilterForTest(nil),
		),
	}
}

func newGatewayExecutionChannelServiceForTest(groupID int64, platform string, channel routing.Channel) *routing.ChannelService {
	channel.GroupIDs = []int64{groupID}
	repo := &gatewayExecutionChannelRows{
		channels:       []routing.Channel{channel},
		groupPlatforms: map[int64]string{groupID: platform},
	}
	return routing.NewChannelService(repo, nil, routing.ChannelOptions{Warn: slog.Warn,
		Now: time.Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	},
	)
}
