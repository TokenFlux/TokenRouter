// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	"bytes"
	"encoding/json"
	"time"
)

type RedeemCode struct {
	ID        int64      `json:"id"`
	Code      string     `json:"code"`
	Type      string     `json:"type"`
	Value     float64    `json:"value"`
	Status    string     `json:"status"`
	MaxUses   int        `json:"max_uses"`
	UsedCount int        `json:"used_count"`
	ExpiresAt *time.Time `json:"expires_at"`
	UsedBy    *int64     `json:"used_by"` // 最后一次成功兑换的用户
	UsedAt    *time.Time `json:"used_at"` // 最后一次成功兑换的时间
	CreatedAt time.Time  `json:"created_at"`

	PlanID *int64 `json:"plan_id"`

	// Notes is only populated for admin_balance/admin_concurrency types
	// so users can see why they were charged or credited
	Notes *string `json:"notes,omitempty"`

	User *UserSummaryResponse `json:"user,omitempty"`
}

// AdminRedeemCode 是管理员接口使用的 redeem code DTO（包含 notes 等字段）。
// 注意：普通用户接口不得返回 notes 等内部信息。
type AdminRedeemCode struct {
	RedeemCode

	Notes string `json:"notes"`
}

// NullableTimeField 用于区分 JSON 字段缺失、传入 null 和传入具体时间。
type NullableTimeField struct {
	Set   bool
	Value *time.Time
}

func (f *NullableTimeField) UnmarshalJSON(data []byte) error {
	f.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		f.Value = nil
		return nil
	}
	var value time.Time
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	f.Value = &value
	return nil
}

type BatchUpdateRedeemCodeFields struct {
	Status    *string           `json:"status,omitempty"`
	ExpiresAt NullableTimeField `json:"expires_at,omitempty"`
	Notes     *string           `json:"notes,omitempty"`

	Type    *string  `json:"type,omitempty"`
	Value   *float64 `json:"value,omitempty"`
	MaxUses *int     `json:"max_uses,omitempty"`
	PlanID  *int64   `json:"plan_id,omitempty"`
}

type BatchUpdateRedeemCodesRequest struct {
	IDs    []int64                     `json:"ids" binding:"required,min=1"`
	Fields BatchUpdateRedeemCodeFields `json:"fields" binding:"required"`
}
