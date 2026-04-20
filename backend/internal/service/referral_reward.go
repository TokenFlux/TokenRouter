package service

import (
	"context"
	"fmt"
	"time"
)

type referralRewardGrantResult struct {
	inviteeUserID int64
	inviterUserID int64
	amount        float64
}

func createReferralRewardRedeemRecord(ctx context.Context, redeemRepo RedeemCodeRepository, userID int64, amount float64) error {
	code, err := GenerateRedeemCode()
	if err != nil {
		return fmt.Errorf("generate referral reward redeem code: %w", err)
	}

	usedAt := time.Now()
	record := &RedeemCode{
		Code:      code,
		Type:      RedeemTypeReferralReward,
		Value:     amount,
		Status:    StatusUsed,
		MaxUses:   1,
		UsedCount: 1,
		UsedBy:    &userID,
		UsedAt:    &usedAt,
	}

	if err := redeemRepo.Create(ctx, record); err != nil {
		return fmt.Errorf("create referral reward redeem code: %w", err)
	}
	if err := redeemRepo.CreateUsage(ctx, &RedeemCodeUsage{
		RedeemCodeID: record.ID,
		UserID:       userID,
		UsedAt:       usedAt,
	}); err != nil {
		return fmt.Errorf("create referral reward usage: %w", err)
	}
	return nil
}
