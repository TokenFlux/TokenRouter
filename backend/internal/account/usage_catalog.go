// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import "github.com/TokenFlux/TokenRouter/internal/upstream/usageview"

const UpstreamUsageAdapterSub2API = usageview.UpstreamUsageAdapterSub2API
const UpstreamUsageAdapterNewAPI = usageview.UpstreamUsageAdapterNewAPI
const UpstreamUsageAdapterZivv = usageview.UpstreamUsageAdapterZivv
const UpstreamUsageAdapterKimiCoding = usageview.UpstreamUsageAdapterKimiCoding
const UpstreamUsageAdapterZhipuCoding = usageview.UpstreamUsageAdapterZhipuCoding
const UpstreamUsageAdapterKimiBalance = usageview.UpstreamUsageAdapterKimiBalance
const UpstreamUsageAdapterDeepseekBalance = usageview.UpstreamUsageAdapterDeepseekBalance
const NewAPIUserAccessTokenCredentialKey = "new_api_user_access_token"
const NewAPIUserIDCredentialKey = "new_api_user_id"
const UpstreamUsageDefaultAdapter = UpstreamUsageAdapterSub2API

// UsageAdapterSpec 声明稳定的目录属性，不持有具体网络实现。
type UsageAdapterSpec struct {
	Name, Label string
	Automatic   bool
}

var usageAdapterCatalog = []UsageAdapterSpec{
	{Name: UpstreamUsageAdapterSub2API, Label: "Sub2API / TokenRouter", Automatic: false},
	{Name: UpstreamUsageAdapterNewAPI, Label: "New API", Automatic: false},
	{Name: UpstreamUsageAdapterZivv, Label: "Zivv", Automatic: false},
	{Name: UpstreamUsageAdapterKimiCoding, Label: "Kimi Coding Plan", Automatic: true},
	{Name: UpstreamUsageAdapterZhipuCoding, Label: "Zhipu Coding Plan", Automatic: true},
	{Name: UpstreamUsageAdapterKimiBalance, Label: "Kimi Balance", Automatic: true},
	{Name: UpstreamUsageAdapterDeepseekBalance, Label: "DeepSeek Balance", Automatic: true},
}

// UpstreamUsageAdapterCatalog 返回独立列表，调用方不能改变配置校验目录。
func UpstreamUsageAdapterCatalog() []UsageAdapterSpec {
	return append([]UsageAdapterSpec(nil), usageAdapterCatalog...)
}

// UpstreamUsageAdapterOptions 返回稳定排序的内置适配器列表。
func UpstreamUsageAdapterOptions() []UpstreamUsageAdapterOption {
	options := make([]UpstreamUsageAdapterOption, 0, len(usageAdapterCatalog))
	for _, registration := range usageAdapterCatalog {
		if registration.Automatic {
			continue
		}
		options = append(options, UpstreamUsageAdapterOption{Name: registration.Name, Label: registration.Label})
	}
	return options
}
