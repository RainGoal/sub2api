//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const explicitVideoPricingJSON = `{"version":1,"unit":"per_video","quantity_policy":"output_seconds","max_billable_seconds":20}`

const explicitVideoIntervalsJSON = `[{"id":31,"pricing_id":11,"tier_label":"1080p","per_request_price":0.25,"sort_order":2}]`

func TestChannelVideoPricingMetadataRoundTrip(t *testing.T) {
	repo, mock := newChannelModelPricingTimePricingRepo(t)
	price := 0.4
	pricing := &service.ChannelModelPricing{ID: 11, ChannelID: 7, Platform: service.PlatformSeedance,
		Models: []string{"custom-video"}, BillingMode: service.BillingModeVideo, PerRequestPrice: &price,
		VideoPricingVersion: 1, VideoBillingUnit: service.VideoBillingPerVideo,
		VideoQuantityPolicy: service.VideoQuantityOutputSeconds, VideoMaxBillableSeconds: 20}
	mock.ExpectQuery(`INSERT INTO channel_model_pricing .*video_pricing_config`).
		WithArgs(int64(7), "seedance", []byte(`["custom-video"]`), service.BillingModeVideo,
			nil, nil, nil, nil, nil, nil, nil, "{}", nil, nil, price, nil, explicitVideoPricingJSON).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(11), time.Time{}, time.Time{}))
	require.NoError(t, repo.CreateModelPricing(context.Background(), pricing))
	mock.ExpectQuery(`(?s)SELECT .*FROM channel_model_pricing WHERE channel_id = \$1`).WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(channelModelPricingTimePricingColumns).AddRow(
			int64(11), int64(7), "seedance", `["custom-video"]`, service.BillingModeVideo,
			nil, nil, nil, nil, nil, nil, nil, "{}", nil, nil, price, nil, time.Time{}, time.Time{}, explicitVideoPricingJSON, `[]`))
	loaded, err := repo.ListModelPricing(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	require.Equal(t, pricing.PerRequestPrice, loaded[0].PerRequestPrice)
	require.Equal(t, pricing.VideoPricingVersion, loaded[0].VideoPricingVersion)
	require.Equal(t, pricing.VideoBillingUnit, loaded[0].VideoBillingUnit)
	require.Equal(t, pricing.VideoQuantityPolicy, loaded[0].VideoQuantityPolicy)
	require.Equal(t, pricing.VideoMaxBillableSeconds, loaded[0].VideoMaxBillableSeconds)
	mock.ExpectExec(`UPDATE channel_model_pricing.*video_pricing_config = \$17`).
		WithArgs([]byte(`["custom-video"]`), service.BillingModeVideo,
			nil, nil, nil, nil, nil, nil, nil, "{}", nil, nil, price, nil, "seedance", int64(11), explicitVideoPricingJSON).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateModelPricing(context.Background(), pricing))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChannelVideoPricingMetadataNullAndMalformed(t *testing.T) {
	for _, data := range [][]byte{nil, []byte(`null`), []byte(`{"version":`)} {
		pricing := service.ChannelModelPricing{ID: 11, Platform: service.PlatformSeedance}
		err := unmarshalVideoPricingConfig(&pricing, data)
		if len(data) == 0 || string(data) == "null" {
			require.NoError(t, err)
			require.Zero(t, pricing.VideoPricingVersion)
			require.Empty(t, pricing.VideoBillingUnit)
		} else {
			require.ErrorContains(t, err, "unmarshal video pricing")
		}
	}
}

func TestChannelVideoPricingIntervalsSnapshotValidation(t *testing.T) {
	for _, data := range []string{"", "null", " null ", `{}`, `[{"per_request_price":"bad"}]`} {
		pricing := service.ChannelModelPricing{}
		require.Error(t, unmarshalVideoPricingIntervals(&pricing, []byte(data)), data)
	}
	pricing := service.ChannelModelPricing{}
	require.NoError(t, unmarshalVideoPricingIntervals(&pricing, []byte(`[]`)))
	require.Empty(t, pricing.Intervals)
	require.NoError(t, unmarshalVideoPricingIntervals(&pricing, []byte(`[{"id":31,"pricing_id":11,"tier_label":"1080p","per_request_price":0.25,"created_at":"2026-10-17T02:03:04+00:00","updated_at":"2026-10-17T02:03:04+00:00"}]`)))
	require.Equal(t, int64(31), pricing.Intervals[0].ID)
	require.Equal(t, int64(11), pricing.Intervals[0].PricingID)
	require.True(t, time.Date(2026, 10, 17, 2, 3, 4, 0, time.UTC).Equal(pricing.Intervals[0].CreatedAt))
}

func TestChannelVideoPricingReadsPriceAndUnitTogether(t *testing.T) {
	for _, operation := range []string{"list", "batch"} {
		t.Run(operation, func(t *testing.T) {
			repo, mock := newChannelModelPricingTimePricingRepo(t)
			rows := sqlmock.NewRows(channelModelPricingTimePricingColumns)
			for i, config := range []string{
				`{"version":1,"unit":"per_second","quantity_policy":"output_seconds","max_billable_seconds":20}`,
				explicitVideoPricingJSON,
			} {
				rows.AddRow(int64(11+i), int64(7), "seedance", `["custom-video"]`, service.BillingModeVideo,
					nil, nil, nil, nil, nil, nil, nil, "{}", nil, nil, []float64{0.1, 0.7}[i], nil,
					time.Time{}, time.Time{}, config, explicitVideoIntervalsJSON)
			}
			rows.AddRow(int64(13), int64(7), "seedance", `["legacy-video"]`, service.BillingModeVideo,
				nil, nil, nil, nil, nil, nil, nil, "{}", nil, nil, 0.05, nil, time.Time{}, time.Time{}, nil, nil)
			query := mock.ExpectQuery(`(?s)SELECT .*per_request_price.*video_pricing_config.*json_agg.*iv.pricing_id = channel_model_pricing.id.*FROM channel_model_pricing`)
			if operation == "list" {
				query.WithArgs(int64(7))
			} else {
				query.WithArgs(pq.Array([]int64{7}))
			}
			query.WillReturnRows(rows)
			// A replacement may commit before the legacy batch; versioned intervals
			// must remain in the original snapshot even when this query is empty.
			mock.ExpectQuery(`SELECT id, pricing_id, min_tokens, max_tokens, tier_label`).
				WithArgs(pq.Array([]int64{13})).WillReturnRows(sqlmock.NewRows([]string{"id"}))
			var loaded []service.ChannelModelPricing
			if operation == "list" {
				var err error
				loaded, err = repo.ListModelPricing(context.Background(), 7)
				require.NoError(t, err)
			} else {
				byChannel, err := repo.batchLoadModelPricing(context.Background(), []int64{7})
				require.NoError(t, err)
				loaded = byChannel[7]
			}
			require.Len(t, loaded, 3)
			assertVideoPricingAmountAndUnitPairs(t, loaded[:2])
			require.Zero(t, loaded[2].VideoPricingVersion)
			require.Empty(t, loaded[2].Intervals)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func assertVideoPricingAmountAndUnitPairs(t *testing.T, loaded []service.ChannelModelPricing) {
	t.Helper()
	require.Len(t, loaded, 2)
	for i, unit := range []service.VideoBillingUnit{service.VideoBillingPerSecond, service.VideoBillingPerVideo} {
		require.NotNil(t, loaded[i].PerRequestPrice)
		require.Equal(t, []float64{0.1, 0.7}[i], *loaded[i].PerRequestPrice)
		require.Equal(t, unit, loaded[i].VideoBillingUnit)
		require.Equal(t, 1, loaded[i].VideoPricingVersion)
		require.Len(t, loaded[i].Intervals, 1)
		require.Equal(t, "1080p", loaded[i].Intervals[0].TierLabel)
		require.Equal(t, 0.25, *loaded[i].Intervals[0].PerRequestPrice)
	}
}

func TestAccountStatsVideoPricingReadsPriceAndUnitTogether(t *testing.T) {
	repo, mock := newChannelModelPricingTimePricingRepo(t)
	rows := sqlmock.NewRows([]string{
		"id", "rule_id", "platform", "models", "billing_mode", "input_price", "output_price",
		"cache_write_price", "cache_write_1h_price", "cache_read_price", "reasoning_effort_multipliers",
		"image_output_price", "per_request_price", "created_at", "updated_at", "video_pricing_config", "video_pricing_intervals",
	})
	for i, config := range []string{
		`{"version":1,"unit":"per_second","quantity_policy":"output_seconds","max_billable_seconds":20}`,
		explicitVideoPricingJSON,
	} {
		rows.AddRow(int64(11+i), int64(9), "seedance", `["custom-video"]`, service.BillingModeVideo,
			nil, nil, nil, nil, nil, "{}", nil, []float64{0.1, 0.7}[i], time.Time{}, time.Time{}, config, explicitVideoIntervalsJSON)
	}
	rows.AddRow(int64(13), int64(9), "openai", `["gpt-5"]`, service.BillingModeToken,
		nil, nil, nil, nil, nil, "{}", nil, nil, time.Time{}, time.Time{}, nil, nil)
	mock.ExpectQuery(`(?s)SELECT .*per_request_price.*video_pricing_config.*json_agg.*iv.pricing_id = channel_account_stats_model_pricing.id.*FROM channel_account_stats_model_pricing`).
		WithArgs(pq.Array([]int64{9})).WillReturnRows(rows)
	mock.ExpectQuery(`(?s)SELECT .*FROM channel_account_stats_pricing_intervals`).
		WithArgs(pq.Array([]int64{13})).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	byRule, err := repo.batchLoadAccountStatsModelPricing(context.Background(), []int64{9})
	require.NoError(t, err)
	require.Len(t, byRule[9], 3)
	assertVideoPricingAmountAndUnitPairs(t, byRule[9][:2])
	require.Zero(t, byRule[9][2].VideoPricingVersion)
	require.NoError(t, mock.ExpectationsWereMet())
}
