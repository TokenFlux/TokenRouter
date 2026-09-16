// 审核存储由 moderation/postgres 唯一实现；旧构造仅保留兼容。
package repository

import (
	"database/sql"

	identitypg "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/moderation/postgres"
)

func NewContentModerationRepository(db *sql.DB) moderation.ContentModerationRepository {
	return postgres.NewContentModerationRepository(db, func(tx *sql.Tx) postgres.UserStatusTx { return identitypg.NewRiskStatusParticipant(tx) })
}
