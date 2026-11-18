package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// Model prices store time-based multipliers as JSON alongside nullable price
// columns. SQL mocks verify the selected column shape and write arguments,
// while pure cases cover literal search escaping and PostgreSQL unique errors,
// including a typed nil error pointer.

// configPricingTimeColumns fixes the mocked row order used to decode
// time-pricing entries alongside their nullable price and multiplier columns.
var configPricingTimeColumns = []string{
	"id", "pricing_config_id", "models", "billing_mode", "price_multiplier", "fast_mode_multiplier", "fast_multiplier", "flex_multiplier", "max_reasoning_effort_multiplier",
	"input_price", "output_price", "cache_write_price", "cache_write_1h_price", "cache_write_30m_price", "cache_write_12h_price", "cache_read_price", "image_input_price", "image_output_price",
	"per_request_price", "time_pricing", "reference_model", "created_at", "updated_at",
}

// TestEscapeLike supplies literal backslashes, SQL wildcard characters, and
// ordinary text. Each case checks the exact escaped string, including empty
// input and adjacent special characters.
func TestEscapeLike(t *testing.T) {
	tests := []struct {
		name  string // displayed subtest name
		input string // literal search input
		want  string // expected escaped text
	}{
		{
			name:  "no special chars",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "backslash",
			input: `a\b`,
			want:  `a\\b`,
		},
		{
			name:  "percent",
			input: "50%",
			want:  `50\%`,
		},
		{
			name:  "underscore",
			input: "a_b",
			want:  `a\_b`,
		},
		{
			name:  "all special chars",
			input: `a\b%c_d`,
			want:  `a\\b\%c\_d`,
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "consecutive special chars",
			input: "%_%",
			want:  `\%\_\%`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, escapeLike(tt.input))
		})
	}
}

// TestIsUniqueViolation supplies direct and wrapped PostgreSQL errors alongside
// nil and unrelated errors. It requires only code 23505 to match and a typed
// nil pointer to return false.
func TestIsUniqueViolation(t *testing.T) {
	tests := []struct {
		name string // displayed subtest name
		err  error  // candidate database error
		want bool   // expected unique-error classification
	}{
		{
			name: "unique violation code 23505",
			err:  &pq.Error{Code: "23505"},
			want: true,
		},
		{
			name: "different pq error code",
			err:  &pq.Error{Code: "23503"},
			want: false,
		},
		{
			name: "non-pq error",
			err:  errors.New("some generic error"),
			want: false,
		},
		{
			name: "typed nil pq.Error",
			err: func() error {
				var pqErr *pq.Error
				return pqErr
			}(),
			want: false,
		},
		{
			name: "bare nil",
			err:  nil,
			want: false,
		},
		{
			name: "wrapped pq error with 23505",
			err:  fmt.Errorf("wrapped: %w", &pq.Error{Code: "23505"}),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUniqueViolation(tt.err))
		})
	}
}

// TestConfigPricingTimeRoundTrip reads a mocked price row containing one daily
// multiplier period. It checks decoded timezone and multiplier values and that
// the interval lookup follows the model-price query.
func TestConfigPricingTimeRoundTrip(t *testing.T) {
	repo, mock := newConfigPricingTimeRepo(t)
	created := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)SELECT .*per_request_price, time_pricing, reference_model, created_at, updated_at.*FROM pricing_config_model_pricing.*pricing_config_id = \$1`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(configPricingTimeColumns).AddRow(
			int64(11), int64(7), `["gpt-5"]`, routing.BillingModeToken, nil, nil, nil, nil, nil,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, `{"timezone":"Asia/Shanghai","periods":[{"start_time":"09:00","end_time":"12:00","multiplier":2}]}`,
			"", created, created,
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

// TestConfigPricingTimeCreateWritesJSON creates a price with a timezone and
// daily multiplier while leaving billing mode empty. SQL expectations require
// the token default and the exact time-pricing JSON argument.
func TestConfigPricingTimeCreateWritesJSON(t *testing.T) {
	repo, mock := newConfigPricingTimeRepo(t)
	pricing := &routing.ModelPricingEntry{
		PricingConfigID: 7,

		Models: []string{"gpt-5"},
		TimePricing: &routing.TimePricingConfig{
			Timezone: "Asia/Shanghai",
			Periods:  []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}},
		},
	}
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO pricing_config_model_pricing (pricing_config_id, models, billing_mode, price_multiplier, fast_mode_multiplier, fast_multiplier, flex_multiplier, max_reasoning_effort_multiplier, input_price, output_price, cache_write_price, cache_write_1h_price, cache_write_30m_price, cache_write_12h_price, cache_read_price, image_input_price, image_output_price, per_request_price, time_pricing, reference_model)")).
		WithArgs(int64(7), []byte(`["gpt-5"]`), routing.BillingModeToken, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, `{"timezone":"Asia/Shanghai","periods":[{"start_time":"09:00","end_time":"12:00","multiplier":2}]}`, "").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(11), time.Time{}, time.Time{}))

	require.NoError(t, repo.CreateModelPricing(context.Background(), pricing))
	require.NoError(t, mock.ExpectationsWereMet())
}

// newConfigPricingTimeRepo returns a store backed by an isolated sqlmock
// database and its expectation recorder. The test owns expectations; registered
// cleanup closes the database when that test finishes.
func newConfigPricingTimeRepo(t *testing.T) (*PricingConfigStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &PricingConfigStore{db: db}, mock
}
