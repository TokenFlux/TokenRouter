// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

type OpenAIMessagesDispatchModelConfig = accessview.OpenAIMessagesDispatchModelConfig

type GroupModelsListConfig = accessview.GroupModelsListConfig

type GroupAvailabilityProbeConfig = accessview.GroupAvailabilityProbeConfig

type GroupAdvancedSchedulerOverrides = policy.GroupAdvancedSchedulerOverrides

type GroupSchedulerType = accessview.GroupSchedulerType

const (
	// GroupSchedulerTypeBasic 保持当前默认调度路径。
	GroupSchedulerTypeBasic GroupSchedulerType = "basic"
	// GroupSchedulerTypeAdvanced 使用通用高级调度器。
	GroupSchedulerTypeAdvanced GroupSchedulerType = "advanced"
)

// NormalizeGroupSchedulerType 归一化并校验调度器类型。
func NormalizeGroupSchedulerType(value string) (GroupSchedulerType, error) {
	normalized := GroupSchedulerType(strings.ToLower(strings.TrimSpace(value)))
	switch normalized {
	case "", GroupSchedulerTypeBasic:
		return GroupSchedulerTypeBasic, nil
	case GroupSchedulerTypeAdvanced:
		return GroupSchedulerTypeAdvanced, nil
	default:
		return "", fmt.Errorf("scheduler_type must be basic or advanced")
	}
}

// Group 在共享值契约上拥有分组规则，账号只读取 accessview 的值投影。
type GroupRoutingPolicy = accessview.GroupRoutingPolicy

type Group accessview.GroupConfig

// UsesAdvancedScheduler 返回分组是否启用通用高级调度器。
func (g *Group) UsesAdvancedScheduler() bool {
	return g != nil && g.SchedulerType == GroupSchedulerTypeAdvanced
}

func (g *Group) IsActive() bool {
	return g.Status == StatusActive
}

// IsGroupContextValid reports whether a group from context has the fields required for routing decisions.
func IsGroupContextValid(group *Group) bool {
	if group == nil {
		return false
	}
	if group.ID <= 0 {
		return false
	}
	if !group.Hydrated {
		return false
	}
	if group.Platform == "" || group.Status == "" {
		return false
	}
	return true
}

// GetRoutingAccountIDs 根据请求模型获取路由账号 ID 列表
// 返回匹配的优先账号 ID 列表，如果没有匹配规则则返回 nil
func (g *Group) GetRoutingAccountIDs(requestedModel string) []int64 {
	if !g.ModelRoutingEnabled || len(g.ModelRouting) == 0 || requestedModel == "" {
		return nil
	}

	// 1. 精确匹配优先
	if accountIDs, ok := g.ModelRouting[requestedModel]; ok && len(accountIDs) > 0 {
		return accountIDs
	}

	// 2. 通配符匹配（前缀匹配）
	for pattern, accountIDs := range g.ModelRouting {
		if MatchModelPattern(pattern, requestedModel) && len(accountIDs) > 0 {
			return accountIDs
		}
	}

	return nil
}

// MatchModelPattern 检查模型是否匹配模式
// 支持 * 通配符，如 "claude-opus-*" 匹配 "claude-opus-4-20250514"
func MatchModelPattern(pattern, model string) bool {
	if pattern == model {
		return true
	}

	// 处理 * 通配符（仅支持末尾通配符）
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(model, prefix)
	}

	return false
}

// ParseMinutes 把 "HH:MM" 解析为当日分钟数（0..1439），格式非法返回 (0,false)。
func ParseMinutes(hhmm string) (int, bool) {
	// 手工解析避免计费热路径反复走 time.Parse；接受集保持与 time.Parse("15:04", s) 一致：
	// 小时允许 1-2 位数字（0..23），分钟必须是 2 位数字（00..59）。
	colon := strings.IndexByte(hhmm, ':')
	if (colon != 1 && colon != 2) || len(hhmm)-colon-1 != 2 {
		return 0, false
	}
	hour := 0
	for i := 0; i < colon; i++ {
		digit := hhmm[i] - '0'
		if digit > 9 {
			return 0, false
		}
		hour = hour*10 + int(digit)
	}
	minuteTens, minuteOnes := hhmm[colon+1]-'0', hhmm[colon+2]-'0'
	if minuteTens > 9 || minuteOnes > 9 {
		return 0, false
	}
	minute := int(minuteTens)*10 + int(minuteOnes)
	if hour > 23 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

// PeakMultiplierAt 返回指定时刻 now 的高峰因子。
//   - 未启用 / 未配置 / 配置非法（start>=end 或格式错误） / 非高峰时段 → 返回 1.0（安全降级）
//   - 区间为左闭右开 [PeakStart, PeakEnd)，仅支持当日区间，不支持跨天（如 22:00-次日02:00）
//   - 调用方把时刻投影到显式日期对象的时区后再调用
//
// 该方法是纯函数，不读取任何外部状态，便于单测。
func (g *Group) PeakMultiplierAt(now time.Time) float64 {
	if g == nil || !g.PeakRateEnabled || g.PeakStart == "" || g.PeakEnd == "" {
		return 1.0
	}
	start, ok1 := ParseMinutes(g.PeakStart)
	end, ok2 := ParseMinutes(g.PeakEnd)
	if !ok1 || !ok2 || start >= end {
		return 1.0
	}
	t := now
	cur := t.Hour()*60 + t.Minute()
	if cur >= start && cur < end {
		return g.PeakRateMultiplier
	}
	return 1.0
}

// ValidatePeakRateConfig 是高峰倍率配置的唯一校验来源，供 handler 与 service 层共用。
// enabled=true 时要求 start/end 合法且 end>start（不支持跨天），multiplier>=0。
// multiplier=0 是允许的，表示高峰 token 请求按 0 倍计费，可用于折扣/免费策略。
// enabled=false 时放行。
func ValidatePeakRateConfig(enabled bool, start, end string, multiplier float64) error {
	if !enabled {
		return nil
	}
	if start == "" || end == "" {
		return errors.New("peak_rate_enabled 为 true 时 peak_start 与 peak_end 必填")
	}
	st, okStart := ParseMinutes(start)
	if !okStart {
		return fmt.Errorf("peak_start 格式应为 HH:MM，got %q", start)
	}
	en, okEnd := ParseMinutes(end)
	if !okEnd {
		return fmt.Errorf("peak_end 格式应为 HH:MM，got %q", end)
	}
	if st >= en {
		return errors.New("peak_end 必须大于 peak_start（不支持跨天区间，如 22:00-02:00）")
	}
	if multiplier < 0 {
		return errors.New("peak_rate_multiplier 不能为负")
	}
	return nil
}

// NormalizePeakRateConfig 归一化最终落库的高峰倍率配置，供 CreateGroup 与 UpdateGroup 共用。
// 启用时保持原值并交给 ValidatePeakRateConfig 严格校验；停用时保留合法窗口与非负倍率，
// 仅清理脏窗口与负倍率，便于管理员临时停用后按原配置重新启用。
func NormalizePeakRateConfig(enabled bool, start, end string, multiplier float64) (bool, string, string, float64) {
	if enabled {
		return enabled, start, end, multiplier
	}
	if _, ok := ParseMinutes(start); !ok {
		start = ""
	}
	if _, ok := ParseMinutes(end); !ok {
		end = ""
	}
	if multiplier < 0 {
		multiplier = 1.0
	}
	return false, start, end, multiplier
}

// GetSearchPricePer1k 返回分组显式配置的搜索工具每千次价格。
func (g *Group) GetSearchPricePer1k() *float64 {
	if g == nil {
		return nil
	}
	return g.SearchPricePer1k
}
