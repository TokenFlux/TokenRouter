package redis

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	servertiming "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"

	"github.com/redis/go-redis/v9"
)

func TestServerTimingRedisHookRecordsCommands(t *testing.T) {
	collector := servertiming.New(time.Now())
	ctx := servertiming.WithCollector(context.Background(), collector)
	hook := TimingHook{}

	process := hook.ProcessHook(func(context.Context, redis.Cmder) error {
		time.Sleep(time.Millisecond)
		return errors.New("redis failure")
	})
	if err := process(ctx, redis.NewStringCmd(ctx, "get", "sensitive-key")); err == nil {
		t.Fatal("ProcessHook did not return the underlying error")
	}

	pipeline := hook.ProcessPipelineHook(func(context.Context, []redis.Cmder) error {
		time.Sleep(time.Millisecond)
		return nil
	})
	commands := []redis.Cmder{
		redis.NewStringCmd(ctx, "get", "first-secret"),
		redis.NewStringCmd(ctx, "get", "second-secret"),
		redis.NewStatusCmd(ctx, "set", "third-secret", "value"),
	}
	if err := pipeline(ctx, commands); err != nil {
		t.Fatal(err)
	}

	header := collector.HeaderValue(time.Now(), "bypass")
	if !strings.Contains(header, `commands=4`) {
		t.Fatalf("header %q does not report one command and a three-command pipeline", header)
	}
	if strings.Contains(header, "secret") || strings.Contains(header, "get") {
		t.Fatalf("Redis command details leaked into header: %q", header)
	}
}

func TestServerTimingRedisHookSkipsInactiveContext(t *testing.T) {
	called := false
	hook := TimingHook{}
	process := hook.ProcessHook(func(context.Context, redis.Cmder) error {
		called = true
		return nil
	})
	ctx := context.Background()
	if err := process(ctx, redis.NewStringCmd(ctx, "ping")); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("inactive Redis command did not reach the next hook")
	}
}
