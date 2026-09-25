// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"strings"
)

func NormalizeMessagesDispatchConfig(cfg OpenAIMessagesDispatchModelConfig, normalize func(string) string) OpenAIMessagesDispatchModelConfig {
	out := OpenAIMessagesDispatchModelConfig{
		OpusMappedModel:   normalize(cfg.OpusMappedModel),
		SonnetMappedModel: normalize(cfg.SonnetMappedModel),
		HaikuMappedModel:  normalize(cfg.HaikuMappedModel),
	}

	if len(cfg.ExactModelMappings) > 0 {
		out.ExactModelMappings = make(map[string]string, len(cfg.ExactModelMappings))
		for requestedModel, mappedModel := range cfg.ExactModelMappings {
			requestedModel = strings.TrimSpace(requestedModel)
			mappedModel = normalize(mappedModel)
			if requestedModel == "" || mappedModel == "" {
				continue
			}
			out.ExactModelMappings[requestedModel] = mappedModel
		}
		if len(out.ExactModelMappings) == 0 {
			out.ExactModelMappings = nil
		}
	}

	return out
}
