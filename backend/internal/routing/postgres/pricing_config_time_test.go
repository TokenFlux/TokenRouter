//go:build unit

package postgres

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

var configPricingTimeColumns = []string{
	"id", "pricing_config_id", "platform", "models", "billing_mode", "price_multiplier", "fast_mode_multiplier", "fast_multiplier", "flex_multiplier", "max_reasoning_effort_multiplier",
	"input_price", "output_price", "cache_write_price", "cache_write_1h_price", "cache_read_price", "image_input_price", "image_output_price",
	"per_request_price", "time_pricing", "created_at", "updated_at",
}

func newConfigPricingTimeRepo(t *testing.T) (*PricingConfigStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &PricingConfigStore{db: db}, mock
}

func TestConfigPricingTimeRoundTrip(t *testing.T) {
	repo, mock := newConfigPricingTimeRepo(t)
	created := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)SELECT .*per_request_price, time_pricing, created_at, updated_at.*FROM pricing_config_model_pricing.*pricing_config_id = \$1`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(configPricingTimeColumns).AddRow(
			int64(11), int64(7), "openai", `["gpt-5"]`, routing.BillingModeToken, nil, nil, nil, nil, nil,
			nil, nil, nil, nil, nil, nil, nil, nil, `{"timezone":"Asia/Shanghai","periods":[{"start_time":"09:00","end_time":"12:00","multiplier":2}]}`,
			created, created,
		))
	mock.ExpectQuery(`SELECT id, pricing_id, min_tokens, max_tokens, tier_label`).
		WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	pricing, err := repo.ListModelPricing(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, pricing, 1)
	require.NotNil(t, pricing[0].TimePricing)
	require.Equal(t, "Asia/Shanghai", pricing[0].TimePricing.Timezone)
	require.Equal(t, 2.0, pricing[0].TimePricing.Periods[0].Multiplier)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConfigPricingTimeCreateWritesJSON(t *testing.T) {
	repo, mock := newConfigPricingTimeRepo(t)
	pricing := &routing.ModelPricingEntry{
		PricingConfigID: 7,
		Platform:        "openai",
		Models:          []string{"gpt-5"},
		TimePricing: &routing.TimePricingConfig{
			Timezone: "Asia/Shanghai",
			Periods:  []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}},
		},
	}
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO pricing_config_model_pricing (pricing_config_id, platform, models, billing_mode, price_multiplier, fast_mode_multiplier, fast_multiplier, flex_multiplier, max_reasoning_effort_multiplier, input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price, image_input_price, image_output_price, per_request_price, time_pricing)")).
		WithArgs(int64(7), "openai", []byte(`["gpt-5"]`), routing.BillingModeToken, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, `{"timezone":"Asia/Shanghai","periods":[{"start_time":"09:00","end_time":"12:00","multiplier":2}]}`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(11), time.Time{}, time.Time{}))

	require.NoError(t, repo.CreateModelPricing(context.Background(), pricing))
	require.NoError(t, mock.ExpectationsWereMet())
}
