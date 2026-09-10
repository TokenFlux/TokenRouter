package redis

import (
	"context"
	"time"

	servertiming "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"

	"github.com/redis/go-redis/v9"
)

type TimingHook struct{}

func (TimingHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (TimingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if !servertiming.Active(ctx) {
			return next(ctx, cmd)
		}
		startedAt := time.Now()
		err := next(ctx, cmd)
		servertiming.Record(ctx, servertiming.MetricRedis, startedAt, time.Now(), 1)
		return err
	}
}

func (TimingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if !servertiming.Active(ctx) {
			return next(ctx, cmds)
		}
		startedAt := time.Now()
		err := next(ctx, cmds)
		servertiming.Record(ctx, servertiming.MetricRedis, startedAt, time.Now(), len(cmds))
		return err
	}
}
