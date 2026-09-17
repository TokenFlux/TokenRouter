package config

import "github.com/TokenFlux/TokenRouter/internal/identity/authconfig"

// 旧启动校验保持同一错误身份，规则由身份配置叶子拥有。
var ErrDingTalkV1AppTypeMismatch = authconfig.ErrDingTalkV1AppTypeMismatch
var ErrDingTalkV4InvalidAppKind = authconfig.ErrDingTalkV4InvalidAppKind

func ValidateDingTalkConfig(value DingTalkConnectConfig) error {
	return authconfig.ValidateDingTalkConfig(value)
}
