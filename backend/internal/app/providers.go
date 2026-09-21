package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/app/bootstrap"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/redis/go-redis/v9"
)

func provideEnt(ctx context.Context, cfg *config.Config, manager *lifecycle.Manager) (*ent.Client, error) {
	client, _, err := bootstrap.InitEnt(ctx, cfg)
	if err != nil {
		return nil, err
	}
	manager.Register(lifecycle.Hook{Name: "Ent", StartOrder: -100, StopOrder: 910, Stop: func(context.Context) error { return client.Close() }})
	return client, nil
}
func provideRedis(cfg *config.Config, manager *lifecycle.Manager) *redis.Client {
	client := bootstrap.InitRedis(cfg)
	manager.Register(lifecycle.Hook{Name: "Redis", StartOrder: -90, StopOrder: 900, Stop: func(context.Context) error { return client.Close() }})
	return client
}
