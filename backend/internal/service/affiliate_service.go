// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	native "github.com/TokenFlux/TokenRouter/internal/promotion"
)

var ErrAffiliateProfileNotFound = native.ErrAffiliateProfileNotFound
var ErrAffiliateCodeInvalid = native.ErrAffiliateCodeInvalid
var ErrAffiliateCodeTaken = native.ErrAffiliateCodeTaken
var ErrAffiliateAlreadyBound = native.ErrAffiliateAlreadyBound
var ErrAffiliateQuotaEmpty = native.ErrAffiliateQuotaEmpty

const AffiliateCodeMinLength = native.AffiliateCodeMinLength
const AffiliateCodeMaxLength = native.AffiliateCodeMaxLength

type AffiliateSummary = native.AffiliateSummary
type AffiliateInvitee = native.AffiliateInvitee
type AffiliateDetail = native.AffiliateDetail
type AffiliateRepository = native.AffiliateRepository
type AffiliateAdminFilter = native.AffiliateAdminFilter
type AffiliateAdminEntry = native.AffiliateAdminEntry
type AffiliateRecordFilter = native.AffiliateRecordFilter
type AffiliateInviteRecord = native.AffiliateInviteRecord
type AffiliateRebateRecord = native.AffiliateRebateRecord
type AffiliateTransferRecord = native.AffiliateTransferRecord
type AffiliateUserOverview = native.AffiliateUserOverview
type AffiliateService = native.AffiliateService

// NewAffiliateService 只投影旧装配参数，不持有第二份规则或缓存。
func NewAffiliateService(repo AffiliateRepository, settings *SettingService, auth APIKeyAuthCacheInvalidator, balances *BillingCacheService) *AffiliateService {
	var settingsPort native.SettingsReader
	if settings != nil {
		settingsPort = settings
	}
	var balancePort native.BalanceCache
	if balances != nil {
		balancePort = balances
	}
	return native.NewAffiliateService(repo, settingsPort, auth, balancePort, native.Runtime{Warn: func(id int64, err error) {
		logger.LegacyPrintf("service.affiliate", "[Affiliate] Failed to invalidate billing cache for user %d: %v", id, err)
	}})
}
