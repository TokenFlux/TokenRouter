//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

// 删列迁移可以重放，不修改模型价卡和独立于分组配置的历史任务快照。
func TestRemoveGroupMediaPricingMigration(t *testing.T) {
	tx, err := integrationDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	// 用事务内临时表隔离迁移验证，不修改集成库已有业务表。
	_, err = tx.Exec(`CREATE TEMP TABLE groups (id bigint, model_pricing jsonb, rate_multiplier numeric, image_price_1k numeric, image_price_2k numeric, image_price_4k numeric, video_price_480p numeric, video_price_720p numeric, video_price_1080p numeric, video_model_prices jsonb, image_rate_independent boolean, image_rate_multiplier numeric, video_rate_independent boolean, video_rate_multiplier numeric)`)
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO groups (id,model_pricing,rate_multiplier,image_price_1k,video_model_prices,image_rate_multiplier) VALUES (1,'[{"models":["gpt-image-2"],"billing_mode":"image","per_request_price":0}]',1.5,0.4,'{"grok-imagine-video":{"480p":0.2}}',0.5)`)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("272_remove_group_media_pricing.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = tx.Exec(string(migration))
		require.NoError(t, err)
	}
	var model, mode string
	var price, rate float64
	err = tx.QueryRow(`SELECT model_pricing->0->'models'->>0,model_pricing->0->>'billing_mode',(model_pricing->0->>'per_request_price')::numeric,rate_multiplier FROM groups WHERE id=1`).Scan(&model, &mode, &price, &rate)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-2", model)
	require.Equal(t, "image", mode)
	require.Zero(t, price)
	require.Equal(t, 1.5, rate)
	var columns int
	err = tx.QueryRow(`SELECT count(*) FROM pg_attribute WHERE attrelid='pg_temp.groups'::regclass AND attnum>0 AND NOT attisdropped`).Scan(&columns)
	require.NoError(t, err)
	require.Equal(t, 3, columns)
}

// 媒体价卡在创建、回填和更新后保留计费模式、分辨率、零价以及批量策略。
func (s *GroupRepoSuite) TestMediaCardsRoundTrip() {
	zero, price := 0.0, 0.15
	group := &service.Group{Name: "media-card-roundtrip", Platform: service.PlatformGrok, Status: service.StatusActive, RateMultiplier: 1.5, AllowImageGeneration: true, BatchImageDiscountMultiplier: 0.5, BatchImageHoldMultiplier: 0.6,
		ModelPricing: []service.ChannelModelPricing{
			{Models: []string{"grok-imagine-image"}, Platform: service.PlatformGrok, BillingMode: service.BillingModeImage, PerRequestPrice: &zero},
			{Models: []string{"grok-imagine-video"}, Platform: service.PlatformGrok, BillingMode: service.BillingModeVideo, PerRequestPrice: &price, Intervals: []service.PricingInterval{{TierLabel: "720p", PerRequestPrice: &zero}}},
		},
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))
	got, err := s.repo.GetByIDLite(s.ctx, group.ID)
	s.Require().NoError(err)
	s.Require().Equal(group.ModelPricing, got.ModelPricing)
	got.ModelPricing[1].Intervals[0].PerRequestPrice = &price
	s.Require().NoError(s.repo.Update(s.ctx, got))
	updated, err := s.repo.GetByID(s.ctx, group.ID)
	s.Require().NoError(err)
	s.Require().Equal(got.ModelPricing, updated.ModelPricing)
	s.Require().Equal(0.5, updated.BatchImageDiscountMultiplier)
	s.Require().Equal(0.6, updated.BatchImageHoldMultiplier)
	s.Require().True(updated.AllowImageGeneration)
}
