package provider

import (
	"context"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

const (
	ollamaCloudUsageMaxSessionBytes = 16 * 1024
	ollamaCloudUsageConcurrency     = 4
	ollamaCloudUsageLeaderLockKey   = "ollama:cloud:usage:leader"
)

// 测试只持有所需账号行，查询返回独立副本以模拟原存储边界。
type ollamaUsageRows struct {
	mu       sync.Mutex
	accounts map[int64]*account.Record
}

func (r *ollamaUsageRows) GetByID(_ context.Context, id int64) (*account.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.accounts[id]
	if v == nil {
		return nil, account.ErrAccountNotFound
	}
	copy := cloneOllamaUsageTestAccount(*v)
	return &copy, nil
}

// 设置替身仅提供原生读取器使用的两个键值操作。
type ollamaUsageSettings struct {
	settings.Repository
	mu     sync.Mutex
	values map[string]string
}

func (r *ollamaUsageSettings) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.values[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return v, nil
}

func (r *ollamaUsageSettings) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

type ollamaUsageTransport interface {
	Do(*http.Request, string, int64, int) (*http.Response, error)
}

// 可调时钟与锁替身只注入原生选项，夹具不实现查询、缓存或生命周期算法。
type ollamaUsageContract struct {
	*account.OllamaCloudUsageService
	now       func() time.Time
	lockCache account.CNMonitorLeader
}

func newOllamaUsageContract(repo account.OllamaAccountReader, transport ollamaUsageTransport, source account.RuntimeSettingsStore, cipher account.OllamaSessionCipher, fixedKey bool) *ollamaUsageContract {
	s := &ollamaUsageContract{now: time.Now}
	options := account.OllamaUsageOptions{
		EncryptionKeyConfigured: fixedKey,
		Now:                     func() time.Time { return s.now() },
		Jitter:                  rand.Int64N,
		InstanceID:              "ollama-contract",
		Lease: func(ctx context.Context, key, owner string, ttl time.Duration) (func(), bool) {
			return account.AcquireSingletonLease(ctx, s.lockCache, nil, key, owner, ttl)
		},
	}
	if transport != nil {
		options.Fetch = OllamaUsageFetcher(transport.Do)
	}
	var config account.OllamaUsageSettingsStore
	if source != nil {
		config = account.NewRuntimeSettings(source, settings.ErrSettingNotFound)
	}
	s.OllamaCloudUsageService = account.NewOllamaCloudUsageService(repo, config, cipher, options)
	return s
}

// 内存 leader 只模拟同一 key 的所有者比较，真实 Redis 行为另有集成测试。
type ollamaUsageLeader struct {
	mu     sync.Mutex
	owners map[string]string
}

func (l *ollamaUsageLeader) TryAcquireLeaderLock(_ context.Context, key, owner string, _ time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owners == nil {
		l.owners = make(map[string]string)
	}
	if _, ok := l.owners[key]; ok {
		return false, nil
	}
	l.owners[key] = owner
	return true, nil
}

func (l *ollamaUsageLeader) ReleaseLeaderLock(_ context.Context, key, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owners[key] == owner {
		delete(l.owners, key)
	}
	return nil
}
