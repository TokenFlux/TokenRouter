// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

func NewTurnstileVerifier() service.TurnstileVerifier { return provider.NewTurnstileVerifier() }
