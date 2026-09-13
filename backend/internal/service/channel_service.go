// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	pricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	provider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	gjson "github.com/tidwall/gjson"
	sjson "github.com/tidwall/sjson"
	"log/slog"
	sync "sync"
	time "time"
)

type ChannelRepository = routing.ChannelRepository
type CreateChannelInput = routing.CreateChannelInput
type UpdateChannelInput = routing.UpdateChannelInput

var ErrChannelNotFound = routing.ErrChannelNotFound
var ErrChannelExists = routing.ErrChannelExists
var ErrGroupAlreadyInChannel = routing.ErrGroupAlreadyInChannel

// ChannelService 的缓存与算法唯一位于 routing；零值兼容通过一次初始化完成。
type ChannelService struct {
	*routing.ChannelService
	initialize sync.Once
}

func NewChannelService(repo ChannelRepository, invalidator APIKeyAuthCacheInvalidator) *ChannelService {
	return WrapChannelService(routing.NewChannelService(repo, invalidator, routing.ChannelOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: provider.LoadPricingLocation}))
}
func WrapChannelService(core *routing.ChannelService) *ChannelService {
	return &ChannelService{ChannelService: core}
}
func (s *ChannelService) core() *routing.ChannelService {
	s.initialize.Do(func() {
		if s.ChannelService == nil {
			s.ChannelService = routing.NewChannelService(nil, nil, routing.ChannelOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: provider.LoadPricingLocation})
		}
	})
	return s.ChannelService
}

type ChannelMappingResult routing.ChannelMappingResult

func (r ChannelMappingResult) BuildModelMappingChain(reqModel, upstreamModel string) string {
	return routing.ChannelMappingResult(r).BuildModelMappingChain(reqModel, upstreamModel)
}

// WithAPIKeyModelRedirect 把 Key 级映射追踪附加到渠道结果，并登记响应恢复阶段。
func (r ChannelMappingResult) WithAPIKeyModelRedirect(ctx context.Context, requestedModel string) ChannelMappingResult {
	trace, ok := APIKeyModelRedirectTraceFromContext(ctx)
	if !ok {
		return r
	}
	r.ClientModel = trace.ClientModel
	r.APIKeyRedirected = true
	RegisterAPIKeyModelRedirectStage(ctx, requestedModel)
	RegisterAPIKeyModelRedirectStage(ctx, r.MappedModel)
	return r
}

func (r ChannelMappingResult) ToUsageFields(reqModel, upstreamModel string) ChannelUsageFields {
	return routing.ChannelMappingResult(r).ToUsageFields(reqModel, upstreamModel)
}

func (s *ChannelService) InvalidateCache() {
	s.core().InvalidateCache()
}

func (s *ChannelService) GetChannelForGroup(ctx context.Context, groupID int64) (*Channel, error) {
	return s.core().GetChannelForGroup(ctx, groupID)
}

func (s *ChannelService) GetGroupPlatform(ctx context.Context, groupID int64) string {
	return s.core().GetGroupPlatform(ctx, groupID)
}

func (s *ChannelService) GetChannelModelPricing(ctx context.Context, groupID int64, model string) *ChannelModelPricing {
	return s.core().GetChannelModelPricing(ctx, groupID, model)
}

func (s *ChannelService) GetEffectiveChannelModelPricing(ctx context.Context, groupID int64, model string) *ChannelModelPricing {
	return s.core().GetEffectiveChannelModelPricing(ctx, groupID, model)
}

func (s *ChannelService) ResolveChannelMapping(ctx context.Context, groupID int64, model string) ChannelMappingResult {
	return ChannelMappingResult(s.core().ResolveChannelMapping(ctx, groupID, model))
}

func (s *ChannelService) IsModelRestricted(ctx context.Context, groupID int64, model string) bool {
	return s.core().IsModelRestricted(ctx, groupID, model)
}

func (s *ChannelService) ResolveChannelMappingAndRestrict(ctx context.Context, groupID *int64, model string) (ChannelMappingResult, bool) {
	v, ok := s.core().ResolveChannelMappingAndRestrict(ctx, groupID, model)
	return ChannelMappingResult(v), ok
}

// ReplaceModelInBody 替换请求体 JSON 中的 model 字段。
func ReplaceModelInBody(body []byte, newModel string) []byte {
	if len(body) == 0 {
		return body
	}
	if current := gjson.GetBytes(body, "model"); current.Exists() && current.String() == newModel {
		return body
	}
	newBody, err := sjson.SetBytes(body, "model", newModel)
	if err != nil {
		return body
	}
	return newBody
}

// RemovePreviousResponseIDFromBody 删除请求体中的 previous_response_id，用于会话失配时改用完整 input 重建上下文。
func RemovePreviousResponseIDFromBody(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	if !gjson.GetBytes(body, "previous_response_id").Exists() {
		return body
	}
	newBody, err := sjson.DeleteBytes(body, "previous_response_id")
	if err != nil {
		return body
	}
	return newBody
}

func (s *ChannelService) Create(ctx context.Context, input *CreateChannelInput) (*Channel, error) {
	return s.core().Create(ctx, input)
}

func (s *ChannelService) GetByID(ctx context.Context, id int64) (*Channel, error) {
	return s.core().GetByID(ctx, id)
}

func (s *ChannelService) Update(ctx context.Context, id int64, input *UpdateChannelInput) (*Channel, error) {
	return s.core().Update(ctx, id, input)
}

func (s *ChannelService) Delete(ctx context.Context, id int64) error {
	return s.core().Delete(ctx, id)
}

func (s *ChannelService) List(ctx context.Context, params pagination.PaginationParams, status, search string) ([]Channel, *pagination.PaginationResult, error) {
	return s.core().List(ctx, params, status, search)
}

func normalizeChannelPricingModelName(model string) string {
	return pricing.NormalizeChannelPricingModelName(model)
}
