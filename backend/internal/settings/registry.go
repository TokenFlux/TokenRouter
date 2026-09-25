package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// Fields 保留扁平 HTTP 文档的字段存在性，JSON null 与缺省不混同。
type Fields map[string]json.RawMessage

// Participant 在装配时固定所有权与准备能力；准备阶段不能提交数据库或发布运行状态。
type Participant struct {
	Module  string
	Fields  []string
	Keys    []string
	Prepare func(context.Context, Fields, map[string]string) (PreparedChange, error)
}

// Registry 只保存静态参与者及其顺序，没有运行时注册或替换入口。
type Registry struct{ participants []Participant }

// NewRegistry 在构造期间拒绝重复字段、键或模块，避免运行更新互相覆盖。
func NewRegistry(participants ...Participant) (*Registry, error) {
	fields, keys, modules := map[string]string{}, map[string]string{}, map[string]bool{}
	result := &Registry{participants: make([]Participant, len(participants))}
	for i, p := range participants {
		if p.Module == "" || p.Prepare == nil || modules[p.Module] {
			return nil, fmt.Errorf("invalid or duplicate settings participant: %q", p.Module)
		}
		modules[p.Module] = true
		for _, pair := range []struct {
			values []string
			owners map[string]string
			kind   string
		}{{p.Fields, fields, "field"}, {p.Keys, keys, "key"}} {
			for _, value := range pair.values {
				if prior, exists := pair.owners[value]; value == "" || exists {
					return nil, fmt.Errorf("duplicate or empty settings %s %q: %s/%s", pair.kind, value, prior, p.Module)
				}
				pair.owners[value] = p.Module
			}
		}
		p.Fields = slices.Clone(p.Fields)
		p.Keys = slices.Clone(p.Keys)
		result.participants[i] = p
	}
	return result, nil
}

// Prepare 按注册顺序准备各模块；只给出所属输入和旧值，返回值也必须属于声明的键。
func (r *Registry) Prepare(ctx context.Context, input Fields, current map[string]string) ([]PreparedChange, error) {
	changes := make([]PreparedChange, 0, len(r.participants))
	for _, p := range r.participants {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fields := Fields{}
		values := map[string]string{}
		allowed := map[string]bool{}
		for _, field := range p.Fields {
			if value, ok := input[field]; ok {
				fields[field] = slices.Clone(value)
			}
		}
		for _, key := range p.Keys {
			allowed[key] = true
			if value, ok := current[key]; ok {
				values[key] = value
			}
		}
		change, err := p.Prepare(ctx, fields, values)
		if err != nil {
			return nil, err
		}
		if change.Module != "" && change.Module != p.Module {
			return nil, fmt.Errorf("settings participant %s returned owner %s", p.Module, change.Module)
		}
		change.Module = p.Module
		for key := range change.Values {
			if !allowed[key] {
				return nil, fmt.Errorf("settings participant %s returned unowned key %s", p.Module, key)
			}
		}
		change.Values = maps.Clone(change.Values)
		if len(change.Values) > 0 || change.Apply != nil {
			changes = append(changes, change)
		}
	}
	return changes, nil
}

// OwnedKeys 返回独立的静态键清单，供装配检查键所有权。
func (r *Registry) OwnedKeys() []string {
	var keys []string
	for _, p := range r.participants {
		keys = append(keys, p.Keys...)
	}
	return keys
}
