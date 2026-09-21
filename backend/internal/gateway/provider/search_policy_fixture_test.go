//go:build unit

package provider_test

import (
	"context"
	"encoding/json"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/search"
)

// searchSettingRows 只返回当前测试保存的原 JSON，不解释启用策略。
type searchSettingRows struct {
	search.ConfigRepository
	data string
}

func (r searchSettingRows) GetValue(context.Context, string) (string, error) { return r.data, nil }
func newSearchSettingsFixture(enabled bool, registry *search.Registry) *search.ConfigService {
	value := &search.WebSearchEmulationConfig{Enabled: enabled, Providers: []search.WebSearchProviderConfig{{Type: "brave", APIKey: "sk-test"}}}
	data, _ := json.Marshal(value)
	return search.NewConfigService(searchSettingRows{data: string(data)}, nil, nil, registry)
}

type searchChannelRows struct {
	routing.ChannelRepository
	channels []routing.Channel
}

func (r searchChannelRows) ListAll(context.Context) ([]routing.Channel, error) {
	return r.channels, nil
}
func (r searchChannelRows) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{}, nil
}
func newChannelServiceWithCache(groupID int64, ch *routing.Channel) *routing.ChannelService {
	value := ch.Clone()
	value.GroupIDs = []int64{groupID}
	return routing.NewChannelService(searchChannelRows{channels: []routing.Channel{*value}}, nil, routing.ChannelOptions{Now: time.Now})
}
