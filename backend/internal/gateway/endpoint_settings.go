package gateway

import "context"

// GetGrokDefaultBaseURLMode 保留旧预算、默认 CLI 和单键读取时点。
func (s *RuntimeSettings) GetGrokDefaultBaseURLMode(ctx context.Context) string {
	if s == nil || s.settingRepo == nil {
		return "cli"
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayForwardingDBTimeout)
	defer cancel()
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyGrokDefaultBaseURLMode)
	if err != nil {
		return "cli"
	}
	return NormalizeGrokDefaultBaseURLMode(raw)
}
