package identity

import (
	"context"
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// AuthSourceParticipantFields 只投影已合并的配置；使用原存储编码保留金额和旧值容错边界。
func AuthSourceParticipantFields(value *AuthSourceDefaultSettings) settings.Fields {
	result := settings.Fields{}
	if value == nil {
		return result
	}
	for key, encoded := range encodeAuthSourceSettings(value) {
		// 编码字符串不会把旧数据库的非规范数值变成新的 JSON 数字校验。
		raw, _ := json.Marshal(encoded)
		result[key] = raw
	}
	return result
}

// prepareAuthSourceFields 只准备来源配置，不重复执行系统设置或发放资金。
func (g *GrantSettings) prepareAuthSourceFields(ctx context.Context, input settings.Fields) (map[string]string, error) {
	values := map[string]string{}
	for _, key := range AuthSourceSettingKeys() {
		raw, ok := input[key]
		if !ok {
			continue
		}
		var stored string
		if err := json.Unmarshal(raw, &stored); err != nil {
			stored = string(raw)
		}
		values[key] = stored
	}
	if len(values) == 0 {
		return nil, nil
	}
	prepared, err := g.PrepareAuthSourceDefaults(ctx, parseAuthSourceSettings(values))
	if err != nil {
		return nil, err
	}
	for key := range prepared {
		if _, present := values[key]; !present {
			delete(prepared, key)
		}
	}
	return prepared, nil
}
