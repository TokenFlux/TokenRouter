package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/stretchr/testify/require"
)

type snapshotGroupBindingReader struct {
	routing.GroupRepository
	group *routing.Group
	calls int
}

func (r *snapshotGroupBindingReader) GetByID(context.Context, int64) (*routing.Group, error) {
	r.calls++
	return r.group, nil
}

// 各执行入口在请求未携带分组时仍读取一次原分组来源，不丢失管理覆盖。
func TestSnapshotNativeBindingPreservesGroupOverrides(t *testing.T) {
	for _, platform := range []string{"gateway", "gemini", "openai"} {
		t.Run(platform, func(t *testing.T) {
			topK := 17
			group := &routing.Group{ID: 71, SchedulerType: routing.GroupSchedulerTypeAdvanced,
				AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{LBTopK: &topK}}
			reader := &snapshotGroupBindingReader{group: group}
			snapshot := scheduler.NewSnapshotService(nil, nil, nil, nil, nil, scheduler.SnapshotBindings{})
			var read func(context.Context, *int64) policy.EffectiveSettings
			switch platform {
			case "gateway":
				gateway := withSchedulerParametersForTest(&GatewayService{groupRepo: reader, schedulerSnapshot: snapshot})
				read = gateway.advancedSchedulerEffectiveSettingsForRequest
			case "gemini":
				gateway := withSchedulerParametersForTest(&GeminiMessagesCompatService{groupRepo: reader, schedulerSnapshot: snapshot})
				read = gateway.advancedSchedulerEffectiveSettingsForRequest
			case "openai":
				gateway := withSchedulerParametersForTest(&OpenAIGatewayService{schedulerSnapshot: snapshot})
				gateway.BindSchedulingGroups(reader.GetByID)
				read = gateway.advancedSchedulerEffectiveSettingsForRequest
			}
			settings := read(context.Background(), &group.ID)
			require.Equal(t, topK, settings.TopK)
			require.Equal(t, 1, reader.calls)
		})
	}
}
