// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	domain "github.com/TokenFlux/TokenRouter/internal/domain"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	qoder "github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// legacyAccountModelDefaults 只投影旧平台目录；S09 改绑来源，不复制映射或状态。
func legacyAccountModelDefaults() account.ModelMappingDefaults {
	return account.ModelMappingDefaults{Antigravity: func() map[string]string { return domain.DefaultAntigravityModelMapping }, GoogleOne: geminicli.GoogleOneModelMapping, AntigravityAgentModel: domain.AntigravityGemini31ProAgentModel}
}

// legacyAccountModelRules 保留 Qoder 站点与平台目录来源，纯规则只取得判断结果。
func legacyAccountModelRules(value *Account) account.ModelPlatformRules {
	return account.ModelPlatformRules{NormalizeQoder: normalizeQoderModelForWhitelist, OpenAIOAuthServable: isOpenAIOAuthServableModel, QoderCompatible: func(model string) bool {
		site, err := qoderSiteForAccount(value)
		return err == nil && qoder.ModelCompatibleWithSite(site, model)
	}}
}
