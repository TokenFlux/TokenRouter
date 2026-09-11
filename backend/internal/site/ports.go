package site

import (
	"context"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

var (
	ErrAnnouncementNilInput        = infraerrors.BadRequest("ANNOUNCEMENT_INPUT_REQUIRED", "announcement input is required")
	ErrAnnouncementInvalidTitle    = infraerrors.BadRequest("ANNOUNCEMENT_TITLE_INVALID", "announcement title is invalid")
	ErrAnnouncementContentRequired = infraerrors.BadRequest(
		"ANNOUNCEMENT_CONTENT_REQUIRED",
		"announcement content is required",
	)
	ErrAnnouncementInvalidStatus     = infraerrors.BadRequest("ANNOUNCEMENT_STATUS_INVALID", "announcement status is invalid")
	ErrAnnouncementInvalidNotifyMode = infraerrors.BadRequest(
		"ANNOUNCEMENT_NOTIFY_MODE_INVALID",
		"announcement notify_mode is invalid",
	)
	ErrAnnouncementInvalidSchedule = infraerrors.BadRequest(
		"ANNOUNCEMENT_TIME_RANGE_INVALID",
		"starts_at must be before ends_at",
	)
)

type AnnouncementListFilters struct {
	Status string
	Search string
}

type AnnouncementRepository interface {
	Create(ctx context.Context, a *Announcement) error
	GetByID(ctx context.Context, id int64) (*Announcement, error)
	Update(ctx context.Context, a *Announcement) error
	Delete(ctx context.Context, id int64) error
	// ArchiveExpired 将已超过结束时间的展示中公告批量归档。
	ArchiveExpired(ctx context.Context, now time.Time) (int64, error)

	List(ctx context.Context, params pagination.PaginationParams, filters AnnouncementListFilters) ([]Announcement, *pagination.PaginationResult, error)
	ListActive(ctx context.Context, now time.Time) ([]Announcement, error)
}

type AnnouncementReadRepository interface {
	MarkRead(ctx context.Context, announcementID, userID int64, readAt time.Time) error
	GetReadMapByUser(ctx context.Context, userID int64, announcementIDs []int64) (map[int64]time.Time, error)
	GetReadMapByUsers(ctx context.Context, announcementID int64, userIDs []int64) (map[int64]time.Time, error)
	CountByAnnouncementID(ctx context.Context, announcementID int64) (int64, error)
}

// UserSnapshot 仅投影公告展示及资格所需的用户字段。
type UserSnapshot struct {
	ID       int64
	Email    string
	Username string
	Balance  float64
}
type UserListFilters struct{ Search string }
type SubscriptionSnapshot struct{ PlanID int64 }

// UserReader 和 SubscriptionReader 由 app 绑定旧能力，核心不认识旧实体。
type UserReader interface {
	GetByID(context.Context, int64) (*UserSnapshot, error)
	ListWithFilters(context.Context, pagination.PaginationParams, UserListFilters) ([]UserSnapshot, *pagination.PaginationResult, error)
}
type SubscriptionReader interface {
	ListActiveByUserID(context.Context, int64) ([]SubscriptionSnapshot, error)
}
