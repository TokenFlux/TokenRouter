// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/redeemcode"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// RegistrationInvitations 使用调用方选定的连接，不开启、提交或回滚事务。
type RegistrationInvitations struct{ client *dbent.Client }

func NewRegistrationInvitations(client *dbent.Client) *RegistrationInvitations {
	return &RegistrationInvitations{client}
}
func RegistrationInvitationsInTx(tx *dbent.Tx) *RegistrationInvitations {
	return &RegistrationInvitations{tx.Client()}
}
func (r *RegistrationInvitations) Load(ctx context.Context, invitationCode string) (*billing.RedeemCode, error) {
	client := r.client
	entity, err := client.RedeemCode.Query().Where(redeemcode.CodeEQ(invitationCode)).Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, billing.ErrRedeemCodeNotFound
		}
		return nil, err
	}
	return &billing.RedeemCode{
		ID:        entity.ID,
		Code:      entity.Code,
		Type:      entity.Type,
		Value:     entity.Value,
		Status:    entity.Status,
		MaxUses:   entity.MaxUses,
		UsedCount: entity.UsedCount,
		ExpiresAt: entity.ExpiresAt,
		UsedBy:    entity.UsedBy,
		UsedAt:    entity.UsedAt,
		Notes:     registrationInvitationNotes(entity.Notes),
		CreatedAt: entity.CreatedAt,
		PlanID:    entity.PlanID,
	}, nil
}
func (r *RegistrationInvitations) Consume(ctx context.Context, invitationID, userID int64) error {
	client := r.client
	affected, err := client.RedeemCode.Update().
		Where(
			redeemcode.IDEQ(invitationID),
			redeemcode.StatusEQ(billing.StatusUnused),
			redeemcode.Or(redeemcode.ExpiresAtIsNil(), redeemcode.ExpiresAtGT(time.Now().UTC())),
		).
		SetStatus(billing.StatusUsed).
		SetUsedCount(1).
		SetUsedBy(userID).
		SetUsedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return billing.ErrRedeemCodeUsed
	}
	return nil
}

// RestoreSnapshot 维持原注册补偿字段集和原始存储错误；不影响调用方的事务所有权。
func (r *RegistrationInvitations) RestoreSnapshot(ctx context.Context, code *billing.RedeemCode) error {
	if code == nil {
		return nil
	}
	_, err := redeemUpdateBuilder(r.client, code).Save(ctx)
	return err
}
func registrationInvitationNotes(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
