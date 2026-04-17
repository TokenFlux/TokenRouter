package service

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

type RedeemCode struct {
	ID        int64
	Code      string
	Type      string
	Value     float64
	Status    string
	MaxUses   int
	UsedCount int
	ExpiresAt *time.Time
	UsedBy    *int64
	UsedAt    *time.Time
	Notes     string
	CreatedAt time.Time

	GroupID      *int64
	ValidityDays int

	User         *User
	Group        *Group
	UsageRecords []RedeemCodeUsage
}

type RedeemCodeUsage struct {
	ID           int64
	RedeemCodeID int64
	UserID       int64
	UsedAt       time.Time

	RedeemCode *RedeemCode
	User       *User
}

func (r *RedeemCode) maxUsesOrDefault() int {
	if r == nil || r.MaxUses < 0 {
		return 1
	}
	return r.MaxUses
}

func (r *RedeemCode) HasUnlimitedUses() bool {
	return r != nil && r.maxUsesOrDefault() == 0
}

func (r *RedeemCode) HasBeenUsed() bool {
	return r != nil && r.UsedCount > 0
}

func (r *RedeemCode) HasRemainingUses() bool {
	if r == nil {
		return false
	}
	if r.HasUnlimitedUses() {
		return true
	}
	return r.UsedCount < r.maxUsesOrDefault()
}

func (r *RedeemCode) IsExhausted() bool {
	return r != nil && !r.HasUnlimitedUses() && !r.HasRemainingUses()
}

func (r *RedeemCode) IsNaturallyExpired() bool {
	if r == nil || r.Type == RedeemTypeInvitation || r.ExpiresAt == nil {
		return false
	}
	return !time.Now().Before(*r.ExpiresAt)
}

func (r *RedeemCode) IsExpired() bool {
	if r == nil {
		return false
	}
	return r.Status == StatusExpired || r.IsNaturallyExpired()
}

func (r *RedeemCode) PersistedStatus() string {
	switch {
	case r == nil:
		return StatusUnused
	case r.Status == StatusExpired:
		return StatusExpired
	case r.IsExhausted():
		return StatusUsed
	case r.HasBeenUsed():
		return StatusActive
	default:
		return StatusUnused
	}
}

func (r *RedeemCode) EffectiveStatus() string {
	if r == nil {
		return StatusUnused
	}
	if r.IsExpired() {
		return StatusExpired
	}
	return r.PersistedStatus()
}

func (r *RedeemCode) IsUsed() bool {
	return r.EffectiveStatus() == StatusUsed
}

func (r *RedeemCode) CanUse() bool {
	switch r.EffectiveStatus() {
	case StatusUnused, StatusActive:
		return true
	default:
		return false
	}
}

func (r *RedeemCode) CanDelete() bool {
	return r != nil && r.UsedCount == 0
}

func GenerateRedeemCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
