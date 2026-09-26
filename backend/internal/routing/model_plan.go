// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
)

// ModelChain 分开保存客户端、Key 重定向后、分组映射后与账号映射后的模型；不对已解析的一跳映射递归。
// 执行层完成供应商名称规范化和响应恢复，用量记录读取本次模型链。
type ModelChain struct {
	ClientModel, RequestedModel, GroupMappedModel, AccountMappedModel string
	APIKeyRedirected, GroupMapped                                     bool
	RestrictionModelSource                                            string
	RestrictModels                                                    bool
	PricingConfigID                                                   int64
	BillingModelSource                                                string
}

// Mapping 投影本次映射、独立白名单阶段及计费元数据，不重新读取配置。
func (p RoutePlan) Mapping() GroupMappingResult {
	return GroupMappingResult{
		MappedModel:            p.models.GroupMappedModel,
		PricingConfigID:        p.models.PricingConfigID,
		Mapped:                 p.models.GroupMapped,
		BillingModelSource:     p.models.BillingModelSource,
		ClientModel:            p.models.ClientModel,
		APIKeyRedirected:       p.models.APIKeyRedirected,
		RestrictModels:         p.models.RestrictModels,
		RestrictionModelSource: p.models.RestrictionModelSource,
	}
}
func (p RoutePlan) Models() ModelChain { return p.models }

// ResolveModel 按原匹配时机接收当次账号规则快照，返回独立尝试结果。
func (p CandidatePlan) ResolveModel(snapshot account.AccountSnapshot, requested string) (CandidatePlan, bool) {
	mapped, matched := snapshot.ModelPolicy.Resolve(requested)
	p.Models.AccountMappedModel = mapped
	return p, matched
}
