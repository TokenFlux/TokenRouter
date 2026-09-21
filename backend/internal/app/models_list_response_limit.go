package app

import "github.com/TokenFlux/TokenRouter/internal/config"

// resolveModelsListReadLimit 投影模型目录读取上限，缺省与非正值沿用已有默认值。
func resolveModelsListReadLimit(cfg *config.Config) int64 {
	if cfg != nil && cfg.Gateway.ModelsListReadMaxBytes > 0 {
		return cfg.Gateway.ModelsListReadMaxBytes
	}
	return config.DefaultModelsListReadMaxBytes
}
