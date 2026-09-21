package service

import "github.com/TokenFlux/TokenRouter/internal/routing"

// CapturedAccountCandidatePlan 只复制本次选择已固化的候选，不查询、重算或补造缺失计划。
func CapturedAccountCandidatePlan(account *Account) (routing.CandidatePlan, bool) {
	if account == nil {
		return routing.CandidatePlan{}, false
	}
	return account.attemptRoute.Candidate()
}
