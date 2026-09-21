package searchtools

import "github.com/TokenFlux/TokenRouter/internal/routing/capability"

const (
	ModeDefault  = "default"
	ModeEnabled  = "enabled"
	ModeDisabled = "disabled"
)

// AccountPolicy 只携带工具启用裁决需要的账号配置，不包含凭据或运行资源。
type AccountPolicy struct {
	ID       int64
	Platform string
	Type     string
	Extra    map[string]any
}

// ModeSelection 将历史布尔配置的诊断交给外层，纯裁决不安装日志后端。
type ModeSelection struct {
	Mode       string
	LegacyBool *bool
}

func AccountMode(value *AccountPolicy) ModeSelection {
	result := ModeSelection{Mode: ModeDefault}
	if value == nil || value.Platform != capability.PlatformAnthropic || value.Type != capability.AccountTypeAPIKey || value.Extra == nil {
		return result
	}
	raw := value.Extra[FeatureKey]
	if legacy, ok := raw.(bool); ok {
		result.LegacyBool = &legacy
		if legacy {
			result.Mode = ModeEnabled
		}
		return result
	}
	mode, ok := raw.(string)
	if !ok {
		return result
	}
	switch mode {
	case ModeEnabled, ModeDisabled:
		result.Mode = mode
	}
	return result
}
