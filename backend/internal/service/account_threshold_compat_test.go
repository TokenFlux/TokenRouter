//go:build unit

// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	time "time"
)

type accountSchedulingThresholdCandidate struct {
	window, scope string
	usedPercent   float64
	until         *time.Time
}

func cnProviderThresholdCandidates(value *Account, provider string) []*accountSchedulingThresholdCandidate {
	v := account.CNProviderThresholdCandidates(AccountRecordView(value), provider)
	if v == nil {
		return nil
	}
	out := make([]*accountSchedulingThresholdCandidate, 0, len(v))
	for _, x := range v {
		if x == nil {
			out = append(out, nil)
		} else {
			out = append(out, &accountSchedulingThresholdCandidate{window: x.Window, scope: x.Scope, usedPercent: x.UsedPercent, until: x.Until})
		}
	}
	return out
}
