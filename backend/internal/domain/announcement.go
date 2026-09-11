// 公告实体与 targeting 的唯一实现已迁入 site；Ent 生成引用在 S16 清理。
package domain

import "github.com/TokenFlux/TokenRouter/internal/site"

type AnnouncementTargeting = site.AnnouncementTargeting
type AnnouncementConditionGroup = site.AnnouncementConditionGroup
type AnnouncementCondition = site.AnnouncementCondition
type Announcement = site.Announcement

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
