package provider

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

// 未绑定分组时保留请求模型，并且不访问分组或价格存储。
func TestRoutePlannerWithoutGroupDoesNotReadConfiguration(t *testing.T) {
	planner := NewRoutePlanner(routing.NewPricingConfigService(nil, nil))
	plan := planner.PlanRoute(context.Background(), nil, nil, "request-model")
	require.Equal(t, routing.GroupMappingResult{MappedModel: "request-model"}, plan.Mapping())
	require.Zero(t, plan.GroupID())
}
