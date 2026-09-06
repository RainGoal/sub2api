package repository

import (
	"context"
	"strconv"
	"time"

	limitmiddleware "github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

type videoAssetUploadLimiter struct {
	redis   *redis.Client
	limiter *limitmiddleware.RateLimiter
}

func NewVideoAssetUploadLimiter(redisClient *redis.Client) service.VideoAssetUploadLimiter {
	return &videoAssetUploadLimiter{redis: redisClient, limiter: limitmiddleware.NewRateLimiter(redisClient)}
}

func (l *videoAssetUploadLimiter) Allow(ctx context.Context, userID int64, maxPerMinute int) (bool, time.Duration, error) {
	if l == nil || l.redis == nil {
		return false, 0, redis.Nil
	}
	result, err := l.limiter.Allow(ctx, "video-assets:user:"+strconv.FormatInt(userID, 10), maxPerMinute, time.Minute)
	return result.Allowed, result.RetryAfter, err
}

var videoAssetByteBudget = redis.NewScript(`
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
local size = tonumber(ARGV[1])
if current + size > tonumber(ARGV[2]) then return 0 end
redis.call('INCRBY', KEYS[1], size)
redis.call('EXPIRE', KEYS[1], 172800)
return 1
`)

func (l *videoAssetUploadLimiter) ReserveBytes(ctx context.Context, userID, size, dailyLimit int64) (bool, error) {
	if l == nil || l.redis == nil {
		return false, redis.Nil
	}
	key := "video-assets:bytes:" + time.Now().UTC().Format("2006-01-02") + ":" + strconv.FormatInt(userID, 10)
	allowed, err := videoAssetByteBudget.Run(ctx, l.redis, []string{key}, size, dailyLimit).Int()
	return allowed == 1, err
}
