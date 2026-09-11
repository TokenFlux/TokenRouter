package service

import (
	"github.com/TokenFlux/TokenRouter/internal/site"
	"time"
)

// 以下别名仅兼容尚未迁出的消费者，S15/S16 删除。
type AnnouncementTargeting = site.AnnouncementTargeting
type AnnouncementConditionGroup = site.AnnouncementConditionGroup
type AnnouncementCondition = site.AnnouncementCondition
type Announcement = site.Announcement
type AnnouncementListFilters = site.AnnouncementListFilters
type AnnouncementRepository = site.AnnouncementRepository
type AnnouncementReadRepository = site.AnnouncementReadRepository
type AnnouncementService = site.AnnouncementService
type CreateAnnouncementInput = site.CreateAnnouncementInput
type UpdateAnnouncementInput = site.UpdateAnnouncementInput
type UserAnnouncement = site.UserAnnouncement
type AnnouncementUserReadStatus = site.AnnouncementUserReadStatus
type AnnouncementExpiryService = site.AnnouncementExpiryService

const AnnouncementStatusDraft = site.AnnouncementStatusDraft
const AnnouncementStatusActive = site.AnnouncementStatusActive
const AnnouncementStatusArchived = site.AnnouncementStatusArchived
const AnnouncementNotifyModeSilent = site.AnnouncementNotifyModeSilent
const AnnouncementNotifyModePopup = site.AnnouncementNotifyModePopup
const AnnouncementConditionTypeSubscription = site.AnnouncementConditionTypeSubscription
const AnnouncementConditionTypeBalance = site.AnnouncementConditionTypeBalance
const AnnouncementOperatorIn = site.AnnouncementOperatorIn
const AnnouncementOperatorGT = site.AnnouncementOperatorGT
const AnnouncementOperatorGTE = site.AnnouncementOperatorGTE
const AnnouncementOperatorLT = site.AnnouncementOperatorLT
const AnnouncementOperatorLTE = site.AnnouncementOperatorLTE
const AnnouncementOperatorEQ = site.AnnouncementOperatorEQ

var ErrAnnouncementNotFound = site.ErrAnnouncementNotFound
var ErrAnnouncementInvalidTarget = site.ErrAnnouncementInvalidTarget
var ErrAnnouncementNilInput = site.ErrAnnouncementNilInput
var ErrAnnouncementInvalidTitle = site.ErrAnnouncementInvalidTitle
var ErrAnnouncementContentRequired = site.ErrAnnouncementContentRequired
var ErrAnnouncementInvalidStatus = site.ErrAnnouncementInvalidStatus
var ErrAnnouncementInvalidNotifyMode = site.ErrAnnouncementInvalidNotifyMode
var ErrAnnouncementInvalidSchedule = site.ErrAnnouncementInvalidSchedule

func NewAnnouncementExpiryService(repo AnnouncementRepository, interval time.Duration) *AnnouncementExpiryService {
	return site.NewAnnouncementExpiryService(repo, interval)
}
