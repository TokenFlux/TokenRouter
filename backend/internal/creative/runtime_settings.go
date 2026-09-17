package creative

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
)

// RuntimeSettingsStore 只读取创作模块的运行设置。
type RuntimeSettingsStore interface {
	GetValue(context.Context, string) (string, error)
}

// RuntimeSettings 保留原即时读取与缺省规则，不增加缓存或后台任务。
type RuntimeSettings struct {
	settingRepo RuntimeSettingsStore
	notFound    error
}

func NewRuntimeSettings(repo RuntimeSettingsStore, notFound error) *RuntimeSettings {
	return &RuntimeSettings{settingRepo: repo, notFound: notFound}
}

func ParseCreativeModelSettings(raw string) []CreativeModelSetting {
	if strings.TrimSpace(raw) == "" {
		return []CreativeModelSetting{}
	}
	var input []CreativeModelSetting
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		slog.Warn("invalid persisted creative model settings", "error", err)
		return []CreativeModelSetting{}
	}
	normalized, err := NormalizeCreativeModelSettings(input)
	if err != nil {
		slog.Warn("invalid persisted creative model settings", "error", err)
		return []CreativeModelSetting{}
	}
	return normalized
}

func (s *RuntimeSettings) IsCreativeEnabled(ctx context.Context) bool {
	if s == nil || s.settingRepo == nil {
		return true
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyCreativeEnabled)
	if err != nil {
		return true
	}
	return value != "false"
}

func (s *RuntimeSettings) GetCreativeModelSettings(ctx context.Context) []CreativeModelSetting {
	if s == nil || s.settingRepo == nil {
		return []CreativeModelSetting{}
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyCreativeModelSettings)
	if err != nil {
		if !errors.Is(err, s.notFound) {
			slog.Warn("failed to read creative model settings", "error", err)
		}
		return []CreativeModelSetting{}
	}
	return ParseCreativeModelSettings(raw)
}

// ParseCreativeWorkerCount 复用现有缺省和上下限。
func ParseCreativeWorkerCount(value string) int {
	count, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || count <= 0 {
		return DefaultCreativeWorkerCount
	}
	return count
}
