//go:build integration

// 调度 Redis 契约使用隔离容器；沿用原逐测试键前缀与清理方式。
package rediscache

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	redisclient "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

var integrationRedis *redisclient.Client
var redisNamespaceSeq uint64

func TestMain(m *testing.M) {
	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:8.4-alpine")
	if err != nil {
		log.Printf("启动隔离 Redis 失败: %v", err)
		os.Exit(1)
	}
	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		log.Print(err)
		os.Exit(1)
	}
	port, err := container.MappedPort(ctx, "6379/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		log.Print(err)
		os.Exit(1)
	}
	integrationRedis = redisclient.NewClient(&redisclient.Options{Addr: host + ":" + port.Port()})
	code := m.Run()
	_ = integrationRedis.Close()
	_ = container.Terminate(ctx)
	os.Exit(code)
}
func testRedis(t *testing.T) *redisclient.Client {
	t.Helper()

	prefix := fmt.Sprintf(
		"it:%s:%d:%d:",
		sanitizeRedisNamespace(t.Name()),
		time.Now().UnixNano(),
		atomic.AddUint64(&redisNamespaceSeq, 1),
	)

	opts := *integrationRedis.Options()
	rdb := redisclient.NewClient(&opts)
	rdb.AddHook(prefixHook{prefix: prefix})

	t.Cleanup(func() {
		ctx := context.Background()

		var cursor uint64
		for {
			keys, nextCursor, err := integrationRedis.Scan(ctx, cursor, prefix+"*", 500).Result()
			require.NoError(t, err, "scan redis keys for cleanup")
			if len(keys) > 0 {
				require.NoError(t, integrationRedis.Unlink(ctx, keys...).Err(), "unlink redis keys for cleanup")
			}

			cursor = nextCursor
			if cursor == 0 {
				break
			}
		}

		_ = rdb.Close()
	})

	return rdb
}

func assertTTLWithin(t *testing.T, ttl time.Duration, min, max time.Duration) {
	t.Helper()
	require.GreaterOrEqual(t, ttl, min, "ttl should be >= min")
	require.LessOrEqual(t, ttl, max, "ttl should be <= max")
}

func sanitizeRedisNamespace(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}

type prefixHook struct {
	prefix string
}

func (h prefixHook) DialHook(next redisclient.DialHook) redisclient.DialHook { return next }

func (h prefixHook) ProcessHook(next redisclient.ProcessHook) redisclient.ProcessHook {
	return func(ctx context.Context, cmd redisclient.Cmder) error {
		h.prefixCmd(cmd)
		return next(ctx, cmd)
	}
}

func (h prefixHook) ProcessPipelineHook(next redisclient.ProcessPipelineHook) redisclient.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redisclient.Cmder) error {
		for _, cmd := range cmds {
			h.prefixCmd(cmd)
		}
		return next(ctx, cmds)
	}
}

func (h prefixHook) prefixCmd(cmd redisclient.Cmder) {
	args := cmd.Args()
	if len(args) < 2 {
		return
	}

	prefixOne := func(i int) {
		if i < 0 || i >= len(args) {
			return
		}

		switch v := args[i].(type) {
		case string:
			if v != "" && !strings.HasPrefix(v, h.prefix) {
				args[i] = h.prefix + v
			}
		case []byte:
			s := string(v)
			if s != "" && !strings.HasPrefix(s, h.prefix) {
				args[i] = []byte(h.prefix + s)
			}
		}
	}

	switch strings.ToLower(cmd.Name()) {
	// GETDEL 与 SET 必须使用相同测试命名空间，才能验证真实的一次性会话消费。
	case "get", "getdel", "set", "setnx", "setex", "psetex", "incr", "decr", "incrby", "expire", "pexpire", "ttl", "pttl",
		"hgetall", "hget", "hset", "hdel", "hincrbyfloat", "exists",
		"zadd", "zcard", "zrange", "zrangebyscore", "zrem", "zremrangebyscore", "zrevrange", "zrevrangebyscore", "zscore":
		prefixOne(1)
	case "mget":
		for i := 1; i < len(args); i++ {
			prefixOne(i)
		}
	case "del", "unlink":
		for i := 1; i < len(args); i++ {
			prefixOne(i)
		}
	case "eval", "evalsha", "eval_ro", "evalsha_ro":
		if len(args) < 3 {
			return
		}
		numKeys, err := strconv.Atoi(fmt.Sprint(args[2]))
		if err != nil || numKeys <= 0 {
			return
		}
		for i := 0; i < numKeys && 3+i < len(args); i++ {
			prefixOne(3 + i)
		}
	case "scan":
		for i := 2; i+1 < len(args); i++ {
			if strings.EqualFold(fmt.Sprint(args[i]), "match") {
				prefixOne(i + 1)
				break
			}
		}
	}
}

// IntegrationRedisSuite provides a base suite for Redis integration tests.
// Embedding suites should call SetupTest to initialize ctx and rdb.
type IntegrationRedisSuite struct {
	suite.Suite
	ctx context.Context
	rdb *redisclient.Client
}

// SetupTest initializes ctx and rdb for each test method.
func (s *IntegrationRedisSuite) SetupTest() {
	s.ctx = context.Background()
	s.rdb = testRedis(s.T())
}

// RequireNoError is a convenience method wrapping require.NoError with s.T().
func (s *IntegrationRedisSuite) RequireNoError(err error, msgAndArgs ...any) {
	s.T().Helper()
	require.NoError(s.T(), err, msgAndArgs...)
}

// AssertTTLWithin asserts that ttl is within [min, max].
func (s *IntegrationRedisSuite) AssertTTLWithin(ttl, min, max time.Duration) {
	s.T().Helper()
	assertTTLWithin(s.T(), ttl, min, max)
}
