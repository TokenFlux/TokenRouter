// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
)

// ModelChain 分开保存客户端、Key 后请求、渠道与账号模型；不对已解析的一跳映射递归。
// 最终供应商规范化与响应恢复仍由旧执行层完成，并按原时机传给用量记录。
type ModelChain struct {
	ClientModel, RequestedModel, ChannelModel, AccountMappedModel string
	APIKeyRedirected, ChannelMapped                               bool
	ChannelID                                                     int64
	BillingModelSource                                            string
}

// Mapping 只投影原渠道输出，不增加查价、限制检查或读取。
func (p RoutePlan) Mapping() ChannelMappingResult {
	return ChannelMappingResult{MappedModel: p.models.ChannelModel, ChannelID: p.models.ChannelID, Mapped: p.models.ChannelMapped, BillingModelSource: p.models.BillingModelSource, ClientModel: p.models.ClientModel, APIKeyRedirected: p.models.APIKeyRedirected}
}
func (p RoutePlan) Models() ModelChain { return p.models }

// ResolveModel 按原匹配时机接收当次账号规则快照，返回独立尝试结果。
func (p CandidatePlan) ResolveModel(snapshot account.AccountSnapshot, requested string) (CandidatePlan, bool) {
	mapped, matched := snapshot.ModelPolicy.Resolve(requested)
	p.Models.AccountMappedModel = mapped
	return p, matched
}
