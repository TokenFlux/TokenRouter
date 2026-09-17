package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/creative"
)

type CreativeModelSetting = creative.CreativeModelSetting

type CreativeModelCandidate = creative.CreativeModelCandidate

func (s *CreativePublicService) NormalizeCreativeModelSettingsForSave(ctx context.Context, input []CreativeModelSetting) ([]CreativeModelSetting, error) {
	return s.nativePublic().NormalizeCreativeModelSettingsForSave(ctx, input)
}

func NormalizeCreativeModelSettings(input []CreativeModelSetting) ([]CreativeModelSetting, error) {
	return creative.NormalizeCreativeModelSettings(input)
}

// parseCreativeModelSettings 解析持久化设置；任何异常都按空白名单处理，避免误放行模型。
func parseCreativeModelSettings(raw string) []CreativeModelSetting {
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

func marshalCreativeModelSettings(input []CreativeModelSetting) (string, []CreativeModelSetting, error) {
	return creative.MarshalCreativeModelSettings(input)
}
