package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	maxGroupNameRunes            = 100
	duplicateGroupInactiveStatus = "inactive"
)

func duplicateGroupOperationID(sourceID int64, actorScope, operationKey string) string {
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

func duplicateGroupName(sourceName string, copyNumber int) string {
	if copyNumber < 1 {
		copyNumber = 1
	}
	suffix := " (Copy)"
	if copyNumber > 1 {
		suffix = fmt.Sprintf(" (Copy %d)", copyNumber)
	}
	baseRunes := []rune(strings.TrimSpace(sourceName))
	maxBaseRunes := maxGroupNameRunes - len([]rune(suffix))
	if maxBaseRunes < 0 {
		maxBaseRunes = 0
	}
	if len(baseRunes) > maxBaseRunes {
		baseRunes = baseRunes[:maxBaseRunes]
	}
	return string(baseRunes) + suffix
}

func cloneGroupValuePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneGroupModelRouting(value map[string][]int64) map[string][]int64 {
	if value == nil {
		return nil
	}
	cloned := make(map[string][]int64, len(value))
	for model, accountIDs := range value {
		cloned[model] = append([]int64(nil), accountIDs...)
	}
	return cloned
}

func cloneGroupVideoModelPrices(value map[string]map[string]float64) map[string]map[string]float64 {
	if value == nil {
		return nil
	}
	cloned := make(map[string]map[string]float64, len(value))
	for model, prices := range value {
		clonedPrices := make(map[string]float64, len(prices))
		for resolution, price := range prices {
			clonedPrices[resolution] = price
		}
		cloned[model] = clonedPrices
	}
	return cloned
}

func cloneGroupMessagesDispatchModelConfig(value OpenAIMessagesDispatchModelConfig) OpenAIMessagesDispatchModelConfig {
	cloned := value
	if value.ExactModelMappings != nil {
		cloned.ExactModelMappings = make(map[string]string, len(value.ExactModelMappings))
		for requestedModel, mappedModel := range value.ExactModelMappings {
			cloned.ExactModelMappings[requestedModel] = mappedModel
		}
	}
	return cloned
}

func cloneGroupForDuplicate(source *Group, operationID string) *Group {
	return &Group{
		Name:                            duplicateGroupName(source.Name, 1),
		Description:                     source.Description,
		Platform:                        source.Platform,
		SchedulerType:                   source.SchedulerType,
		AdvancedSchedulerOverrides:      CloneGroupAdvancedSchedulerOverrides(source.AdvancedSchedulerOverrides),
		DisplayBrand:                    source.DisplayBrand,
		RateMultiplier:                  source.RateMultiplier,
		PeakRateEnabled:                 source.PeakRateEnabled,
		PeakStart:                       source.PeakStart,
		PeakEnd:                         source.PeakEnd,
		PeakRateMultiplier:              source.PeakRateMultiplier,
		IsExclusive:                     source.IsExclusive,
		Status:                          duplicateGroupInactiveStatus,
		DuplicateOperationID:            operationID,
		DataSharingEnabled:              source.DataSharingEnabled,
		SessionIsolationEnabled:         source.SessionIsolationEnabled,
		AllowImageGeneration:            source.AllowImageGeneration,
		AllowBatchImageGeneration:       source.AllowBatchImageGeneration,
		ImageRateIndependent:            source.ImageRateIndependent,
		ImageRateMultiplier:             source.ImageRateMultiplier,
		ImagePrice1K:                    cloneGroupValuePointer(source.ImagePrice1K),
		ImagePrice2K:                    cloneGroupValuePointer(source.ImagePrice2K),
		ImagePrice4K:                    cloneGroupValuePointer(source.ImagePrice4K),
		BatchImageDiscountMultiplier:    source.BatchImageDiscountMultiplier,
		BatchImageHoldMultiplier:        source.BatchImageHoldMultiplier,
		VideoRateIndependent:            source.VideoRateIndependent,
		VideoRateMultiplier:             source.VideoRateMultiplier,
		VideoPrice480P:                  cloneGroupValuePointer(source.VideoPrice480P),
		VideoPrice720P:                  cloneGroupValuePointer(source.VideoPrice720P),
		VideoPrice1080P:                 cloneGroupValuePointer(source.VideoPrice1080P),
		VideoModelPrices:                cloneGroupVideoModelPrices(source.VideoModelPrices),
		WebSearchPricePerCall:           cloneGroupValuePointer(source.WebSearchPricePerCall),
		SearchPricePer1k:                cloneGroupValuePointer(source.SearchPricePer1k),
		AudioRealtimePricePerMin:        cloneGroupValuePointer(source.AudioRealtimePricePerMin),
		AudioTTSPricePerMillionChars:    cloneGroupValuePointer(source.AudioTTSPricePerMillionChars),
		AudioSTTPricePerHour:            cloneGroupValuePointer(source.AudioSTTPricePerHour),
		ClaudeCodeOnly:                  source.ClaudeCodeOnly,
		FallbackGroupID:                 cloneGroupValuePointer(source.FallbackGroupID),
		FallbackGroupIDOnInvalidRequest: cloneGroupValuePointer(source.FallbackGroupIDOnInvalidRequest),
		UnavailableFallbackGroupID:      cloneGroupValuePointer(source.UnavailableFallbackGroupID),
		ModelRouting:                    cloneGroupModelRouting(source.ModelRouting),
		ModelRoutingEnabled:             source.ModelRoutingEnabled,
		MCPXMLInject:                    source.MCPXMLInject,
		SupportedModelScopes:            append([]string(nil), source.SupportedModelScopes...),
		SortOrder:                       source.SortOrder,
		AllowedClientProtocols:          cloneGroupClientProtocols(source.AllowedClientProtocols),
		AllowMessagesDispatch:           source.Platform == PlatformOpenAI && source.AllowsClientProtocol(GroupClientProtocolAnthropicMessages),
		AllowLive:                       source.AllowLive,
		RequireOAuthOnly:                source.RequireOAuthOnly,
		RequirePrivacySet:               source.RequirePrivacySet,
		DefaultMappedModel:              source.DefaultMappedModel,
		MessagesDispatchModelConfig:     cloneGroupMessagesDispatchModelConfig(source.MessagesDispatchModelConfig),
		ModelsListConfig: GroupModelsListConfig{
			Enabled: source.ModelsListConfig.Enabled,
			Models:  append([]string(nil), source.ModelsListConfig.Models...),
		},
		AvailabilityProbeConfig: source.AvailabilityProbeConfig,
		RPMLimit:                source.RPMLimit,
		MaxReasoningEffort:      source.MaxReasoningEffort,
		ReasoningEffortMappings: append([]ReasoningEffortMapping(nil), source.ReasoningEffortMappings...),
	}
}

// cloneGroupClientProtocols 返回独立且非 nil 的协议集合副本。
func cloneGroupClientProtocols(protocols []GroupClientProtocol) []GroupClientProtocol {
	return append([]GroupClientProtocol{}, protocols...)
}

// RecoverDuplicateGroup 只读查找同一操作者、源分组和幂等键已提交的副本。
func (s *adminServiceImpl) RecoverDuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*Group, error) {
	operationID := duplicateGroupOperationID(id, actorScope, operationKey)
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
func (s *adminServiceImpl) DuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*Group, error) {
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

	duplicate := cloneGroupForDuplicate(source, duplicateGroupOperationID(id, actorScope, operationKey))
	sanitizeGroupReasoningEffortPolicy(duplicate)
	for copyNumber := 1; ; copyNumber++ {
		duplicate.Name = duplicateGroupName(source.Name, copyNumber)
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
