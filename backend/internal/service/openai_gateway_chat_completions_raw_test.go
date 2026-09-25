//go:build unit

package service

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
)

func rawChatCompletionsTestConfig() *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: true,
			},
		},
	}
}
