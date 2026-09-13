// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	context "context"
	sha256 "crypto/sha256"
	errors "errors"
	fmt "fmt"
	protocol "github.com/TokenFlux/TokenRouter/internal/protocol"
	policy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	maps "maps"
	strconv "strconv"
	strings "strings"
	time "time"
)

const (
	MaxGroupNameRunes            = 100
	DuplicateGroupInactiveStatus = "inactive"
)

func DuplicateGroupOperationID(sourceID int64, actorScope, operationKey string) string {
	operationKey = strings.TrimSpace(operationKey)
	if operationKey == "" {
		return ""
	}
	actorScope = strings.TrimSpace(actorScope)
	if actorScope == "" {
		actorScope = "admin:0"
	}
	payload := "admin.groups.duplicate\x00" + actorScope + "\x00" + strconv.FormatInt(sourceID, 10) + "\x00" + operationKey
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", digest)
}

func DuplicateGroupName(sourceName string, copyNumber int) string {
	if copyNumber < 1 {
		copyNumber = 1
	}
	suffix := " (Copy)"
	if copyNumber > 1 {
		suffix = fmt.Sprintf(" (Copy %d)", copyNumber)
	}
	baseRunes := []rune(strings.TrimSpace(sourceName))
	maxBaseRunes := MaxGroupNameRunes - len([]rune(suffix))
	if maxBaseRunes < 0 {
		maxBaseRunes = 0
	}
	if len(baseRunes) > maxBaseRunes {
		baseRunes = baseRunes[:maxBaseRunes]
	}
	return string(baseRunes) + suffix
}

func CloneGroupValuePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func CloneGroupModelRouting(value map[string][]int64) map[string][]int64 {
	if value == nil {
		return nil
	}
	cloned := make(map[string][]int64, len(value))
	for model, accountIDs := range value {
		cloned[model] = append([]int64(nil), accountIDs...)
	}
	return cloned
}

func CloneGroupMessagesDispatchModelConfig(value OpenAIMessagesDispatchModelConfig) OpenAIMessagesDispatchModelConfig {
	cloned := value
	if value.ExactModelMappings != nil {
		cloned.ExactModelMappings = make(map[string]string, len(value.ExactModelMappings))
		for requestedModel, mappedModel := range value.ExactModelMappings {
			cloned.ExactModelMappings[requestedModel] = mappedModel
		}
	}
	return cloned
}

func CloneGroupForDuplicate(source *Group, operationID string) *Group {
	return &Group{
		Name:                            DuplicateGroupName(source.Name, 1),
		Description:                     source.Description,
		Platform:                        source.Platform,
		SchedulerType:                   source.SchedulerType,
		AdvancedSchedulerOverrides:      policy.CloneGroupAdvancedSchedulerOverrides(source.AdvancedSchedulerOverrides),
		DisplayBrand:                    source.DisplayBrand,
		RateMultiplier:                  source.RateMultiplier,
		LongContextPricingEnabled:       source.LongContextPricingEnabled,
		ModelPricing:                    CloneGroupModelPricing(source.ModelPricing),
		PeakRateEnabled:                 source.PeakRateEnabled,
		PeakStart:                       source.PeakStart,
		PeakEnd:                         source.PeakEnd,
		PeakRateMultiplier:              source.PeakRateMultiplier,
		IsExclusive:                     source.IsExclusive,
		Status:                          DuplicateGroupInactiveStatus,
		DuplicateOperationID:            operationID,
		SessionIsolationEnabled:         source.SessionIsolationEnabled,
		AllowImageGeneration:            source.AllowImageGeneration,
		AllowBatchImageGeneration:       source.AllowBatchImageGeneration,
		BatchImageDiscountMultiplier:    source.BatchImageDiscountMultiplier,
		BatchImageHoldMultiplier:        source.BatchImageHoldMultiplier,
		WebSearchPricePerCall:           CloneGroupValuePointer(source.WebSearchPricePerCall),
		SearchPricePer1k:                CloneGroupValuePointer(source.SearchPricePer1k),
		AudioRealtimePricePerMin:        CloneGroupValuePointer(source.AudioRealtimePricePerMin),
		AudioTTSPricePerMillionChars:    CloneGroupValuePointer(source.AudioTTSPricePerMillionChars),
		AudioSTTPricePerHour:            CloneGroupValuePointer(source.AudioSTTPricePerHour),
		ClaudeCodeOnly:                  source.ClaudeCodeOnly,
		FallbackGroupID:                 CloneGroupValuePointer(source.FallbackGroupID),
		FallbackGroupIDOnInvalidRequest: CloneGroupValuePointer(source.FallbackGroupIDOnInvalidRequest),
		UnavailableFallbackGroupID:      CloneGroupValuePointer(source.UnavailableFallbackGroupID),
		ModelRouting:                    CloneGroupModelRouting(source.ModelRouting),
		ModelRoutingEnabled:             source.ModelRoutingEnabled,
		MCPXMLInject:                    source.MCPXMLInject,
		SupportedModelScopes:            append([]string(nil), source.SupportedModelScopes...),
		SortOrder:                       source.SortOrder,
		AllowedProtocols:                CloneGroupClientProtocols(source.AllowedProtocols),
		ProtocolFallbacks:               maps.Clone(source.ProtocolFallbacks),
		ResponsesImagePolicy:            source.ResponsesImagePolicy,
		AllowMessagesDispatch:           source.Platform == PlatformOpenAI && source.AllowsClientProtocol(protocol.ProtocolAnthropicMessages),
		AllowLive:                       source.AllowLive,
		ForceOpenAIFast:                 source.ForceOpenAIFast,
		OpenAIFastPolicy:                source.EffectiveOpenAIFastPolicy(),
		FreeOpenAIFast:                  source.FreeOpenAIFast,
		RequireOAuthOnly:                source.RequireOAuthOnly,
		RequirePrivacySet:               source.RequirePrivacySet,
		DefaultMappedModel:              source.DefaultMappedModel,
		MessagesDispatchModelConfig:     CloneGroupMessagesDispatchModelConfig(source.MessagesDispatchModelConfig),
		ModelsListConfig: GroupModelsListConfig{
			Enabled: source.ModelsListConfig.Enabled,
			Models:  append([]string(nil), source.ModelsListConfig.Models...),
		},
		AvailabilityProbeConfig:     source.AvailabilityProbeConfig,
		RPMLimit:                    source.RPMLimit,
		MaxReasoningEffort:          source.MaxReasoningEffort,
		MaxReasoningEffortOverLimit: source.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:     append([]ReasoningEffortMapping(nil), source.ReasoningEffortMappings...),
	}
}

// CloneGroupClientProtocols 返回独立且非 nil 的协议集合副本。
func CloneGroupClientProtocols(protocols []protocol.ProtocolID) []protocol.ProtocolID {
	return append([]protocol.ProtocolID{}, protocols...)
}

// RecoverDuplicateGroup 只读查找同一操作者、源分组和幂等键已提交的副本。
func (s *GroupAdmin) RecoverDuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*Group, error) {
	operationID := DuplicateGroupOperationID(id, actorScope, operationKey)
	if operationID == "" {
		return nil, nil
	}
	if s.groupDuplicateRepo == nil {
		return nil, errors.New("group duplicate repository is not configured")
	}
	group, err := s.groupDuplicateRepo.FindByDuplicateOperationID(ctx, operationID)
	if err != nil {
		return nil, fmt.Errorf("find duplicate group operation: %w", err)
	}
	if group == nil {
		return nil, nil
	}
	hydrated, err := s.groupRepo.GetByID(ctx, group.ID)
	if err != nil {
		return nil, fmt.Errorf("load recovered duplicate group: %w", err)
	}
	return hydrated, nil
}

// DuplicateGroup 创建停用状态的配置副本并保留账号优先级。
// 仓储会原子提交分组、绑定和 outbox 事件，绑定失败时不会留下孤立分组。
func (s *GroupAdmin) DuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*Group, error) {
	existing, err := s.RecoverDuplicateGroup(ctx, id, actorScope, operationKey)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	source, err := s.groupRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.groupDuplicateRepo == nil {
		return nil, errors.New("group duplicate repository is not configured")
	}

	duplicate := CloneGroupForDuplicate(source, DuplicateGroupOperationID(id, actorScope, operationKey))
	SanitizeGroupReasoningEffortPolicy(duplicate)
	for copyNumber := 1; ; copyNumber++ {
		duplicate.Name = DuplicateGroupName(source.Name, copyNumber)
		duplicate.ID = 0
		duplicate.CreatedAt = time.Time{}
		duplicate.UpdatedAt = time.Time{}
		if err := s.groupDuplicateRepo.CreateFromSource(ctx, duplicate, source.ID); err == nil {
			hydrated, loadErr := s.groupRepo.GetByID(ctx, duplicate.ID)
			if loadErr != nil {
				return nil, fmt.Errorf("load duplicate group: %w", loadErr)
			}
			return hydrated, nil
		} else if !errors.Is(err, ErrGroupExists) {
			return nil, fmt.Errorf("create duplicate group: %w", err)
		}

		// 唯一冲突可能来自自动生成的名称，也可能来自 operation ID；先尝试恢复，
		// 若没有对应操作记录，再继续生成下一个副本名称。
		recovered, recoverErr := s.RecoverDuplicateGroup(ctx, id, actorScope, operationKey)
		if recoverErr != nil {
			return nil, recoverErr
		}
		if recovered != nil {
			return recovered, nil
		}
	}
}

// CloneGroupModelPricing 保持复制分组的模型、区间、分时配置与源分组互相独立。
func CloneGroupModelPricing(pricing []ChannelModelPricing) []ChannelModelPricing {
	if pricing == nil {
		return nil
	}
	cloned := make([]ChannelModelPricing, len(pricing))
	for i := range pricing {
		cloned[i] = pricing[i].Clone()
	}
	return cloned
}
