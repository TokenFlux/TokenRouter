//go:build unit

// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func propagateAccountProxyToShadows(ctx context.Context, repo AccountRepository, parentID int64, proxyID *int64) error {
	return acctcore.PropagateAccountProxyToShadows(ctx, legacyAccountAdminStore{repo}, parentID, proxyID)
}

func guardCRSShadowParentInvariant(ctx context.Context, repo AccountRepository, existing *Account, newPlatform, newType string) error {
	return acctcore.GuardCRSShadowParentInvariant(ctx, legacyCRSStore{source: repo}, AccountRecordView(existing), newPlatform, newType)
}
