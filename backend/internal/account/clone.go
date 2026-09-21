package account

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// CloneValues 保留 JSON 值的实际数值类型、nil 与空集合，并隔离可变容器。
// 对非法循环输入保留其循环形状，后续 JSON 校验仍按原错误路径拒绝。
func CloneValues(values map[string]any) map[string]any {
	return cloneValues(values, &valueCopyState{})
}

type sliceIdentity struct {
	ptr    uintptr
	length int
}
type valueCopyState struct {
	maps   map[uintptr]map[string]any
	slices map[sliceIdentity][]any
}

func cloneValues(values map[string]any, state *valueCopyState) map[string]any {
	if values == nil {
		return nil
	}
	id := reflect.ValueOf(values).Pointer()
	if previous, ok := state.maps[id]; ok {
		return previous
	}
	if state.maps == nil {
		state.maps = make(map[uintptr]map[string]any)
	}
	out := make(map[string]any, len(values))
	state.maps[id] = out
	for key, value := range values {
		out[key] = cloneValue(value, state)
	}
	return out
}
func cloneValue(value any, state *valueCopyState) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneValues(v, state)
	case []any:
		if v == nil {
			return []any(nil)
		}
		id := sliceIdentity{ptr: reflect.ValueOf(v).Pointer(), length: len(v)}
		if previous, ok := state.slices[id]; ok {
			return previous
		}
		if state.slices == nil {
			state.slices = make(map[sliceIdentity][]any)
		}
		out := make([]any, len(v))
		state.slices[id] = out
		for i := range v {
			out[i] = cloneValue(v[i], state)
		}
		return out
	case map[string]string:
		return maps.Clone(v)
	case map[string]bool:
		return maps.Clone(v)
	case []protocol.ProtocolID:
		return slices.Clone(v)
	case []string:
		return slices.Clone(v)
	case []int64:
		return slices.Clone(v)
	case []int:
		return slices.Clone(v)
	case []float64:
		return slices.Clone(v)
	case []bool:
		return slices.Clone(v)
	case json.RawMessage:
		return slices.Clone(v)
	case []byte:
		return slices.Clone(v)
	default:
		return value
	}
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

// CloneRecord 复制模块、缓存与请求边界的账号图，不复制时钟或安装新的缓存状态。
func CloneRecord(record *Record) *Record { return cloneRecord(record, make(map[*Record]*Record)) }
func cloneRecord(record *Record, seen map[*Record]*Record) *Record {
	if record == nil {
		return nil
	}
	if copy, ok := seen[record]; ok {
		return copy
	}
	out := *record
	seen[record] = &out
	out.Credentials = CloneValues(record.Credentials)
	out.Extra = CloneValues(record.Extra)
	out.Notes = clonePointer(record.Notes)
	out.ProxyID = clonePointer(record.ProxyID)
	out.ProxyFallbackOriginID = clonePointer(record.ProxyFallbackOriginID)
	out.ProxyFallbackOriginName = clonePointer(record.ProxyFallbackOriginName)
	out.RateMultiplier = clonePointer(record.RateMultiplier)
	out.LoadFactor = clonePointer(record.LoadFactor)
	out.LastUsedAt = clonePointer(record.LastUsedAt)
	out.ExpiresAt = clonePointer(record.ExpiresAt)
	out.RateLimitedAt = clonePointer(record.RateLimitedAt)
	out.RateLimitResetAt = clonePointer(record.RateLimitResetAt)
	out.OverloadUntil = clonePointer(record.OverloadUntil)
	out.TempUnschedulableUntil = clonePointer(record.TempUnschedulableUntil)
	out.SessionWindowStart = clonePointer(record.SessionWindowStart)
	out.SessionWindowEnd = clonePointer(record.SessionWindowEnd)
	out.ParentAccountID = clonePointer(record.ParentAccountID)
	out.GroupIDs = slices.Clone(record.GroupIDs)
	if record.Proxy != nil {
		proxy := *record.Proxy
		proxy.ExpiresAt = clonePointer(proxy.ExpiresAt)
		proxy.BackupProxyID = clonePointer(proxy.BackupProxyID)
		out.Proxy = &proxy
	}
	if record.Groups != nil {
		out.Groups = make([]*accessview.GroupConfig, len(record.Groups))
		for i := range record.Groups {
			out.Groups[i] = accessview.CloneGroupConfig(record.Groups[i])
		}
	}
	if record.AccountGroups != nil {
		out.AccountGroups = make([]GroupMembership, len(record.AccountGroups))
		for i, link := range record.AccountGroups {
			out.AccountGroups[i] = link
			out.AccountGroups[i].Account = cloneRecord(link.Account, seen)
			out.AccountGroups[i].Group = accessview.CloneGroupConfig(link.Group)
		}
	}
	return &out
}
