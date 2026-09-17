//go:build unit

package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// 仅保留已有 unit 断言需要的旧入口，退出 S16。
const openAIImagesOAuthUnavailableDefaultCooldownMinutes = accountcore.OpenAIImagesOAuthUnavailableDefaultCooldownMinutes

const openAIImagesOAuthUnavailableMaxCooldownMinutes = accountcore.OpenAIImagesOAuthUnavailableMaxCooldownMinutes
