package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// --- 模型定价 ---

func (r *channelRepository) ListModelPricing(ctx context.Context, channelID int64) ([]service.ChannelModelPricing, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, channel_id, platform, models, billing_mode, input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price, fast_multiplier, flex_multiplier, reasoning_effort_multipliers, image_input_price, image_output_price, per_request_price, time_pricing, created_at, updated_at, video_pricing_config,
		 CASE WHEN platform = 'seedance' AND video_pricing_config->>'version' = '1' THEN
		   (SELECT COALESCE(json_agg(row_to_json(iv) ORDER BY iv.sort_order, iv.id), '[]'::json)
		    FROM channel_pricing_intervals iv WHERE iv.pricing_id = channel_model_pricing.id)
		 END AS video_pricing_intervals
		 FROM channel_model_pricing WHERE channel_id = $1 ORDER BY id`, channelID,
	)
	if err != nil {
		return nil, fmt.Errorf("list model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result, pricingIDs, err := scanModelPricingRows(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	if len(pricingIDs) > 0 {
		intervalMap, err := r.batchLoadIntervals(ctx, pricingIDs)
		if err != nil {
			return nil, err
		}
		for i := range result {
			if result[i].VideoPricingVersion != service.VideoPricingVersion {
				result[i].Intervals = intervalMap[result[i].ID]
			}
		}
	}

	return result, nil
}

func (r *channelRepository) CreateModelPricing(ctx context.Context, pricing *service.ChannelModelPricing) error {
	return createModelPricingExec(ctx, r.db, pricing)
}

func (r *channelRepository) UpdateModelPricing(ctx context.Context, pricing *service.ChannelModelPricing) error {
	modelsJSON, err := json.Marshal(pricing.Models)
	if err != nil {
		return fmt.Errorf("marshal models: %w", err)
	}
	reasoningEffortMultipliersJSON, err := marshalReasoningEffortMultipliers(pricing.ReasoningEffortMultipliers)
	if err != nil {
		return err
	}
	timePricingJSON, err := marshalChannelTimePricing(pricing.TimePricing)
	if err != nil {
		return err
	}
	billingMode := pricing.BillingMode
	if billingMode == "" {
		billingMode = service.BillingModeToken
	}
	query := `UPDATE channel_model_pricing
		 SET models = $1, billing_mode = $2, input_price = $3, output_price = $4, cache_write_price = $5, cache_write_1h_price = $6, cache_read_price = $7, fast_multiplier = $8, flex_multiplier = $9, reasoning_effort_multipliers = $10, image_input_price = $11, image_output_price = $12, per_request_price = $13, time_pricing = $14, platform = $15, updated_at = NOW()
		 WHERE id = $16`
	args := []any{
		modelsJSON, billingMode, pricing.InputPrice, pricing.OutputPrice, pricing.CacheWritePrice, pricing.CacheWrite1hPrice, pricing.CacheReadPrice,
		pricing.FastMultiplier, pricing.FlexMultiplier, reasoningEffortMultipliersJSON, pricing.ImageInputPrice, pricing.ImageOutputPrice, pricing.PerRequestPrice,
		timePricingJSON, pricing.Platform, pricing.ID,
	}
	if pricing.Platform == service.PlatformSeedance {
		videoConfig, err := marshalVideoPricingConfig(pricing)
		if err != nil {
			return err
		}
		query = strings.Replace(query, "updated_at = NOW()", "video_pricing_config = $17, updated_at = NOW()", 1)
		args = append(args, videoConfig)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update model pricing: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("pricing entry not found: %d", pricing.ID)
	}
	return nil
}

func (r *channelRepository) DeleteModelPricing(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM channel_model_pricing WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete model pricing: %w", err)
	}
	return nil
}

func (r *channelRepository) ReplaceModelPricing(ctx context.Context, channelID int64, pricingList []service.ChannelModelPricing) error {
	return r.runInTx(ctx, func(tx *sql.Tx) error {
		return replaceModelPricingTx(ctx, tx, channelID, pricingList)
	})
}

// --- 批量加载辅助方法 ---

// batchLoadModelPricing 批量加载多个渠道的模型定价（含区间）
func (r *channelRepository) batchLoadModelPricing(ctx context.Context, channelIDs []int64) (map[int64][]service.ChannelModelPricing, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, channel_id, platform, models, billing_mode, input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price, fast_multiplier, flex_multiplier, reasoning_effort_multipliers, image_input_price, image_output_price, per_request_price, time_pricing, created_at, updated_at, video_pricing_config,
		 CASE WHEN platform = 'seedance' AND video_pricing_config->>'version' = '1' THEN
		   (SELECT COALESCE(json_agg(row_to_json(iv) ORDER BY iv.sort_order, iv.id), '[]'::json)
		    FROM channel_pricing_intervals iv WHERE iv.pricing_id = channel_model_pricing.id)
		 END AS video_pricing_intervals
		 FROM channel_model_pricing WHERE channel_id = ANY($1) ORDER BY channel_id, id`,
		pq.Array(channelIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()

	allPricing, allPricingIDs, err := scanModelPricingRows(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// 按 channelID 分组
	pricingMap := make(map[int64][]service.ChannelModelPricing, len(channelIDs))
	for _, p := range allPricing {
		pricingMap[p.ChannelID] = append(pricingMap[p.ChannelID], p)
	}

	// 批量加载所有区间
	if len(allPricingIDs) > 0 {
		intervalMap, err := r.batchLoadIntervals(ctx, allPricingIDs)
		if err != nil {
			return nil, err
		}
		for chID := range pricingMap {
			for i := range pricingMap[chID] {
				if pricingMap[chID][i].VideoPricingVersion != service.VideoPricingVersion {
					pricingMap[chID][i].Intervals = intervalMap[pricingMap[chID][i].ID]
				}
			}
		}
	}

	return pricingMap, nil
}

// batchLoadIntervals 批量加载多个定价条目的区间
func (r *channelRepository) batchLoadIntervals(ctx context.Context, pricingIDs []int64) (map[int64][]service.PricingInterval, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, pricing_id, min_tokens, max_tokens, tier_label,
		        input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price,
		        input_multiplier, output_multiplier, cache_write_multiplier, cache_read_multiplier,
		        per_request_price, sort_order, created_at, updated_at
		 FROM channel_pricing_intervals
		 WHERE pricing_id = ANY($1) ORDER BY pricing_id, sort_order, id`,
		pq.Array(pricingIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load intervals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	intervalMap := make(map[int64][]service.PricingInterval, len(pricingIDs))
	for rows.Next() {
		var iv service.PricingInterval
		if err := rows.Scan(
			&iv.ID, &iv.PricingID, &iv.MinTokens, &iv.MaxTokens, &iv.TierLabel,
			&iv.InputPrice, &iv.OutputPrice, &iv.CacheWritePrice, &iv.CacheWrite1hPrice, &iv.CacheReadPrice,
			&iv.InputMultiplier, &iv.OutputMultiplier, &iv.CacheWriteMultiplier, &iv.CacheReadMultiplier,
			&iv.PerRequestPrice, &iv.SortOrder, &iv.CreatedAt, &iv.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan interval: %w", err)
		}
		intervalMap[iv.PricingID] = append(intervalMap[iv.PricingID], iv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate intervals: %w", err)
	}
	return intervalMap, nil
}

// --- 共享 scan 辅助 ---

// scanModelPricingRows 扫描 model pricing 行，返回结果列表和 ID 列表
func scanModelPricingRows(rows *sql.Rows) ([]service.ChannelModelPricing, []int64, error) {
	var result []service.ChannelModelPricing
	var pricingIDs []int64
	for rows.Next() {
		var p service.ChannelModelPricing
		var modelsJSON []byte
		var timePricingJSON []byte
		var reasoningEffortMultipliersJSON []byte
		var videoPricingJSON []byte
		var videoIntervalsJSON []byte
		if err := rows.Scan(
			&p.ID, &p.ChannelID, &p.Platform, &modelsJSON, &p.BillingMode,
			&p.InputPrice, &p.OutputPrice, &p.CacheWritePrice, &p.CacheWrite1hPrice, &p.CacheReadPrice,
			&p.FastMultiplier, &p.FlexMultiplier, &reasoningEffortMultipliersJSON,
			&p.ImageInputPrice, &p.ImageOutputPrice, &p.PerRequestPrice, &timePricingJSON, &p.CreatedAt, &p.UpdatedAt, &videoPricingJSON, &videoIntervalsJSON,
		); err != nil {
			return nil, nil, fmt.Errorf("scan model pricing: %w", err)
		}
		if err := json.Unmarshal(modelsJSON, &p.Models); err != nil {
			p.Models = []string{}
		}
		multipliers, err := unmarshalReasoningEffortMultipliers(reasoningEffortMultipliersJSON)
		if err != nil {
			return nil, nil, err
		}
		p.ReasoningEffortMultipliers = multipliers
		timePricing, err := unmarshalChannelTimePricing(timePricingJSON)
		if err != nil {
			return nil, nil, err
		}
		p.TimePricing = timePricing
		if err := unmarshalVideoPricingConfig(&p, videoPricingJSON); err != nil {
			return nil, nil, err
		}
		if p.VideoPricingVersion == service.VideoPricingVersion {
			if err := unmarshalVideoPricingIntervals(&p, videoIntervalsJSON); err != nil {
				return nil, nil, err
			}
		} else {
			pricingIDs = append(pricingIDs, p.ID)
		}
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate model pricing: %w", err)
	}
	return result, pricingIDs, nil
}

// --- 事务内辅助方法 ---

// dbExec 是 *sql.DB 和 *sql.Tx 共享的最小 SQL 执行接口
type dbExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func setGroupIDsTx(ctx context.Context, exec dbExec, channelID int64, groupIDs []int64) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM channel_groups WHERE channel_id = $1`, channelID); err != nil {
		return fmt.Errorf("delete old group associations: %w", err)
	}
	if len(groupIDs) == 0 {
		return nil
	}
	_, err := exec.ExecContext(ctx,
		`INSERT INTO channel_groups (channel_id, group_id)
		 SELECT $1, unnest($2::bigint[])`,
		channelID, pq.Array(groupIDs),
	)
	if err != nil {
		return fmt.Errorf("insert group associations: %w", err)
	}
	return nil
}

func createModelPricingExec(ctx context.Context, exec dbExec, pricing *service.ChannelModelPricing) error {
	modelsJSON, err := json.Marshal(pricing.Models)
	if err != nil {
		return fmt.Errorf("marshal models: %w", err)
	}
	reasoningEffortMultipliersJSON, err := marshalReasoningEffortMultipliers(pricing.ReasoningEffortMultipliers)
	if err != nil {
		return err
	}
	timePricingJSON, err := marshalChannelTimePricing(pricing.TimePricing)
	if err != nil {
		return err
	}
	billingMode := pricing.BillingMode
	if billingMode == "" {
		billingMode = service.BillingModeToken
	}
	platform := pricing.Platform
	if platform == "" {
		platform = "anthropic"
	}
	query := `INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price, fast_multiplier, flex_multiplier, reasoning_effort_multipliers, image_input_price, image_output_price, per_request_price, time_pricing)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16) RETURNING id, created_at, updated_at`
	args := []any{
		pricing.ChannelID, platform, modelsJSON, billingMode,
		pricing.InputPrice, pricing.OutputPrice, pricing.CacheWritePrice, pricing.CacheWrite1hPrice, pricing.CacheReadPrice,
		pricing.FastMultiplier, pricing.FlexMultiplier, reasoningEffortMultipliersJSON, pricing.ImageInputPrice, pricing.ImageOutputPrice,
		pricing.PerRequestPrice, timePricingJSON,
	}
	if platform == service.PlatformSeedance && pricing.VideoPricingVersion != 0 {
		videoConfig, err := marshalVideoPricingConfig(pricing)
		if err != nil {
			return err
		}
		query = strings.Replace(query, "time_pricing)", "time_pricing, video_pricing_config)", 1)
		query = strings.Replace(query, "$15, $16)", "$15, $16, $17)", 1)
		args = append(args, videoConfig)
	}
	err = exec.QueryRowContext(ctx, query, args...).Scan(&pricing.ID, &pricing.CreatedAt, &pricing.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert model pricing: %w", err)
	}

	for i := range pricing.Intervals {
		pricing.Intervals[i].PricingID = pricing.ID
		if err := createIntervalExec(ctx, exec, &pricing.Intervals[i]); err != nil {
			return err
		}
	}

	return nil
}

func marshalReasoningEffortMultipliers(multipliers map[string]float64) (string, error) {
	if len(multipliers) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(multipliers)
	if err != nil {
		return "", fmt.Errorf("marshal reasoning effort multipliers: %w", err)
	}
	return string(data), nil
}

func unmarshalReasoningEffortMultipliers(data []byte) (map[string]float64, error) {
	multipliers := make(map[string]float64)
	if len(data) == 0 {
		return multipliers, nil
	}
	if err := json.Unmarshal(data, &multipliers); err != nil {
		return nil, fmt.Errorf("unmarshal reasoning effort multipliers: %w", err)
	}
	if multipliers == nil {
		multipliers = make(map[string]float64)
	}
	return multipliers, nil
}

func marshalChannelTimePricing(config *service.ChannelTimePricing) (any, error) {
	if config == nil || len(config.Periods) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal time pricing: %w", err)
	}
	return string(data), nil
}

func unmarshalChannelTimePricing(data []byte) (*service.ChannelTimePricing, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var config service.ChannelTimePricing
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("unmarshal time pricing: %w", err)
	}
	return &config, nil
}

type videoPricingConfig struct {
	Version            int                         `json:"version"`
	Unit               service.VideoBillingUnit    `json:"unit"`
	QuantityPolicy     service.VideoQuantityPolicy `json:"quantity_policy"`
	MaxBillableSeconds float64                     `json:"max_billable_seconds"`
}

func marshalVideoPricingConfig(pricing *service.ChannelModelPricing) (any, error) {
	if pricing.VideoPricingVersion == 0 {
		return nil, nil
	}
	config := videoPricingConfig{Version: pricing.VideoPricingVersion, Unit: pricing.VideoBillingUnit,
		QuantityPolicy: pricing.VideoQuantityPolicy, MaxBillableSeconds: pricing.VideoMaxBillableSeconds}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal video pricing: %w", err)
	}
	return string(data), nil
}

// Read the unit and amount from one row version so concurrent price updates
// cannot combine an old amount with a newly selected billing unit.
func unmarshalVideoPricingConfig(pricing *service.ChannelModelPricing, data []byte) error {
	if pricing.Platform != service.PlatformSeedance || len(data) == 0 {
		return nil
	}
	var config videoPricingConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("unmarshal video pricing: %w", err)
	}
	pricing.VideoPricingVersion, pricing.VideoBillingUnit = config.Version, config.Unit
	pricing.VideoQuantityPolicy, pricing.VideoMaxBillableSeconds = config.QuantityPolicy, config.MaxBillableSeconds
	return nil
}

// Versioned video prices must include their resolution prices in the same SQL
// snapshot as the default price and unit, even when the result is an empty list.
func unmarshalVideoPricingIntervals(pricing *service.ChannelModelPricing, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("video pricing intervals snapshot is missing")
	}
	if err := json.Unmarshal(data, &pricing.Intervals); err != nil {
		return fmt.Errorf("unmarshal video pricing intervals: %w", err)
	}
	if pricing.Intervals == nil {
		return fmt.Errorf("video pricing intervals snapshot is missing")
	}
	return nil
}

func createIntervalExec(ctx context.Context, exec dbExec, iv *service.PricingInterval) error {
	return exec.QueryRowContext(ctx,
		`INSERT INTO channel_pricing_intervals
		 (pricing_id, min_tokens, max_tokens, tier_label, input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price, input_multiplier, output_multiplier, cache_write_multiplier, cache_read_multiplier, per_request_price, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) RETURNING id, created_at, updated_at`,
		iv.PricingID, iv.MinTokens, iv.MaxTokens, iv.TierLabel,
		iv.InputPrice, iv.OutputPrice, iv.CacheWritePrice, iv.CacheWrite1hPrice, iv.CacheReadPrice,
		iv.InputMultiplier, iv.OutputMultiplier, iv.CacheWriteMultiplier, iv.CacheReadMultiplier,
		iv.PerRequestPrice, iv.SortOrder,
	).Scan(&iv.ID, &iv.CreatedAt, &iv.UpdatedAt)
}

func replaceModelPricingTx(ctx context.Context, exec dbExec, channelID int64, pricingList []service.ChannelModelPricing) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM channel_model_pricing WHERE channel_id = $1`, channelID); err != nil {
		return fmt.Errorf("delete old model pricing: %w", err)
	}
	for i := range pricingList {
		pricingList[i].ChannelID = channelID
		if err := createModelPricingExec(ctx, exec, &pricingList[i]); err != nil {
			return fmt.Errorf("insert model pricing: %w", err)
		}
	}
	return nil
}

// isUniqueViolation 检查 pq 唯一约束违反错误
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr != nil {
		return pqErr.Code == "23505"
	}
	return false
}

// escapeLike 转义 LIKE/ILIKE 模式中的特殊字符
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
