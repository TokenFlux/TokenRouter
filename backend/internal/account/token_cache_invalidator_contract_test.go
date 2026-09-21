//go:build unit

package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type geminiTokenCacheStub struct {
	deletedKeys []string
	deleteErr   error
}

func (s *geminiTokenCacheStub) GetAccessToken(ctx context.Context, cacheKey string) (string, error) {
	return "", nil
}

func (s *geminiTokenCacheStub) SetAccessToken(ctx context.Context, cacheKey string, token string, ttl time.Duration) error {
	return nil
}

func (s *geminiTokenCacheStub) DeleteAccessToken(ctx context.Context, cacheKey string) error {
	s.deletedKeys = append(s.deletedKeys, cacheKey)
	return s.deleteErr
}

func (s *geminiTokenCacheStub) AcquireRefreshLock(ctx context.Context, cacheKey string, ttl time.Duration) (bool, error) {
	return true, nil
}

func (s *geminiTokenCacheStub) ReleaseRefreshLock(ctx context.Context, cacheKey string) error {
	return nil
}

func TestCompositeTokenCacheInvalidator_Gemini(t *testing.T) {
	cache := &geminiTokenCacheStub{}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(cache, nil, nil)
	account := &accountcore.Record{
		ID:       10,
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"project_id": "project-x",
		},
	}

	err := invalidator.InvalidateToken(context.Background(), account)
	require.NoError(t, err)
	// 新行为：同时删除基于 project_id 和 account_id 的缓存键
	// 这是为了处理：首次获取 token 时可能没有 project_id，之后自动检测到后会使用新 key
	require.Equal(t, []string{"gemini:project-x", "gemini:account:10"}, cache.deletedKeys)
}

func TestCompositeTokenCacheInvalidator_GeminiWithoutProjectID(t *testing.T) {
	cache := &geminiTokenCacheStub{}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(cache, nil, nil)
	account := &accountcore.Record{
		ID:       10,
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "gemini-token",
		},
	}

	err := invalidator.InvalidateToken(context.Background(), account)
	require.NoError(t, err)
	// 没有 project_id 时，两个 key 相同，去重后只删除一个
	require.Equal(t, []string{"gemini:account:10"}, cache.deletedKeys)
}

func TestCompositeTokenCacheInvalidator_Antigravity(t *testing.T) {
	cache := &geminiTokenCacheStub{}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(cache, nil, nil)
	account := &accountcore.Record{
		ID:       99,
		Platform: capability.PlatformAntigravity,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"project_id": "ag-project",
		},
	}

	err := invalidator.InvalidateToken(context.Background(), account)
	require.NoError(t, err)
	// 新行为：同时删除基于 project_id 和 account_id 的缓存键
	require.Equal(t, []string{"ag:ag-project", "ag:account:99"}, cache.deletedKeys)
}

func TestCompositeTokenCacheInvalidator_AntigravityWithoutProjectID(t *testing.T) {
	cache := &geminiTokenCacheStub{}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(cache, nil, nil)
	account := &accountcore.Record{
		ID:       99,
		Platform: capability.PlatformAntigravity,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "ag-token",
		},
	}

	err := invalidator.InvalidateToken(context.Background(), account)
	require.NoError(t, err)
	// 没有 project_id 时，两个 key 相同，去重后只删除一个
	require.Equal(t, []string{"ag:account:99"}, cache.deletedKeys)
}

func TestCompositeTokenCacheInvalidator_OpenAI(t *testing.T) {
	cache := &geminiTokenCacheStub{}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(cache, nil, nil)
	account := &accountcore.Record{
		ID:       500,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "openai-token",
		},
	}

	err := invalidator.InvalidateToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"openai:account:500"}, cache.deletedKeys)
}

func TestCompositeTokenCacheInvalidator_Claude(t *testing.T) {
	cache := &geminiTokenCacheStub{}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(cache, nil, nil)
	account := &accountcore.Record{
		ID:       600,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "claude-token",
		},
	}

	err := invalidator.InvalidateToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"claude:account:600"}, cache.deletedKeys)
}

func TestCompositeTokenCacheInvalidator_SkipNonOAuth(t *testing.T) {
	cache := &geminiTokenCacheStub{}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(cache, nil, nil)

	tests := []struct {
		name    string
		account *accountcore.Record
	}{
		{
			name: "gemini_api_key",
			account: &accountcore.Record{
				ID:       1,
				Platform: capability.PlatformGemini,
				Type:     capability.AccountTypeAPIKey,
			},
		},
		{
			name: "openai_api_key",
			account: &accountcore.Record{
				ID:       2,
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeAPIKey,
			},
		},
		{
			name: "claude_api_key",
			account: &accountcore.Record{
				ID:       3,
				Platform: capability.PlatformAnthropic,
				Type:     capability.AccountTypeAPIKey,
			},
		},
		{
			name: "claude_setup_token",
			account: &accountcore.Record{
				ID:       4,
				Platform: capability.PlatformAnthropic,
				Type:     capability.AccountTypeSetupToken,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache.deletedKeys = nil
			err := invalidator.InvalidateToken(context.Background(), tt.account)
			require.NoError(t, err)
			require.Empty(t, cache.deletedKeys)
		})
	}
}

func TestCompositeTokenCacheInvalidator_SkipUnsupportedPlatform(t *testing.T) {
	cache := &geminiTokenCacheStub{}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(cache, nil, nil)
	account := &accountcore.Record{
		ID:       100,
		Platform: "unknown-platform",
		Type:     capability.AccountTypeOAuth,
	}

	err := invalidator.InvalidateToken(context.Background(), account)
	require.NoError(t, err)
	require.Empty(t, cache.deletedKeys)
}

func TestCompositeTokenCacheInvalidator_NilCache(t *testing.T) {
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(nil, nil, nil)
	account := &accountcore.Record{
		ID:       2,
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
	}

	err := invalidator.InvalidateToken(context.Background(), account)
	require.NoError(t, err)
}

func TestCompositeTokenCacheInvalidator_NilAccount(t *testing.T) {
	cache := &geminiTokenCacheStub{}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(cache, nil, nil)

	err := invalidator.InvalidateToken(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, cache.deletedKeys)
}

func TestCompositeTokenCacheInvalidator_NilInvalidator(t *testing.T) {
	var invalidator *accountcore.CompositeTokenCacheInvalidator
	account := &accountcore.Record{
		ID:       5,
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
	}

	err := invalidator.InvalidateToken(context.Background(), account)
	require.NoError(t, err)
}

func TestCompositeTokenCacheInvalidator_DeleteError(t *testing.T) {
	expectedErr := errors.New("redis connection failed")
	cache := &geminiTokenCacheStub{deleteErr: expectedErr}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(cache, nil, nil)

	tests := []struct {
		name    string
		account *accountcore.Record
	}{
		{
			name: "openai_delete_error",
			account: &accountcore.Record{
				ID:       700,
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeOAuth,
			},
		},
		{
			name: "claude_delete_error",
			account: &accountcore.Record{
				ID:       800,
				Platform: capability.PlatformAnthropic,
				Type:     capability.AccountTypeOAuth,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 新行为：删除失败只记录日志，不返回错误
			// 这是因为缓存失效失败不应影响主业务流程
			err := invalidator.InvalidateToken(context.Background(), tt.account)
			require.NoError(t, err)
		})
	}
}

func TestCompositeTokenCacheInvalidator_AllPlatformsIntegration(t *testing.T) {
	// 测试所有平台的缓存键生成和删除
	cache := &geminiTokenCacheStub{}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(cache, nil, nil)

	accounts := []*accountcore.Record{
		{ID: 1, Platform: capability.PlatformGemini, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"project_id": "gemini-proj"}},
		{ID: 2, Platform: capability.PlatformAntigravity, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"project_id": "ag-proj"}},
		{ID: 3, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth},
		{ID: 4, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth},
	}

	// 新行为：Gemini 和 Antigravity 会同时删除基于 project_id 和 account_id 的键
	expectedKeys := []string{
		"gemini:gemini-proj",
		"gemini:account:1",
		"ag:ag-proj",
		"ag:account:2",
		"openai:account:3",
		"claude:account:4",
	}

	for _, acc := range accounts {
		err := invalidator.InvalidateToken(context.Background(), acc)
		require.NoError(t, err)
	}

	require.Equal(t, expectedKeys, cache.deletedKeys)
}

// ========== GetCredentialAsInt64 测试 ==========

func TestAccount_GetCredentialAsInt64(t *testing.T) {
	tests := []struct {
		name        string
		credentials map[string]any
		key         string
		expected    int64
	}{
		{
			name:        "int64_value",
			credentials: map[string]any{"_token_version": int64(1737654321000)},
			key:         "_token_version",
			expected:    1737654321000,
		},
		{
			name:        "float64_value",
			credentials: map[string]any{"_token_version": float64(1737654321000)},
			key:         "_token_version",
			expected:    1737654321000,
		},
		{
			name:        "int_value",
			credentials: map[string]any{"_token_version": 12345},
			key:         "_token_version",
			expected:    12345,
		},
		{
			name:        "string_value",
			credentials: map[string]any{"_token_version": "1737654321000"},
			key:         "_token_version",
			expected:    1737654321000,
		},
		{
			name:        "string_with_spaces",
			credentials: map[string]any{"_token_version": "  1737654321000  "},
			key:         "_token_version",
			expected:    1737654321000,
		},
		{
			name:        "nil_credentials",
			credentials: nil,
			key:         "_token_version",
			expected:    0,
		},
		{
			name:        "missing_key",
			credentials: map[string]any{"other_key": 123},
			key:         "_token_version",
			expected:    0,
		},
		{
			name:        "nil_value",
			credentials: map[string]any{"_token_version": nil},
			key:         "_token_version",
			expected:    0,
		},
		{
			name:        "invalid_string",
			credentials: map[string]any{"_token_version": "not_a_number"},
			key:         "_token_version",
			expected:    0,
		},
		{
			name:        "empty_string",
			credentials: map[string]any{"_token_version": ""},
			key:         "_token_version",
			expected:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &accountcore.Record{Credentials: tt.credentials}
			result := account.GetCredentialAsInt64(tt.key)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestAccount_GetCredentialAsInt64_NilAccount(t *testing.T) {
	var account *accountcore.Record
	result := account.GetCredentialAsInt64("_token_version")
	require.Equal(t, int64(0), result)
}

// ========== CheckTokenVersion 测试 ==========

func TestCheckTokenVersion(t *testing.T) {
	tests := []struct {
		name          string
		account       *accountcore.Record
		latestAccount *accountcore.Record
		repoErr       error
		expectedStale bool
	}{
		{
			name:          "nil_account",
			account:       nil,
			latestAccount: nil,
			expectedStale: false,
		},
		{
			name: "no_version_in_account_but_db_has_version",
			account: &accountcore.Record{
				ID:          1,
				Credentials: map[string]any{},
			},
			latestAccount: &accountcore.Record{
				ID:          1,
				Credentials: map[string]any{"_token_version": int64(100)},
			},
			expectedStale: true, // 当前 account 无版本但 DB 有，说明已被异步刷新，当前已过时
		},
		{
			name: "both_no_version",
			account: &accountcore.Record{
				ID:          1,
				Credentials: map[string]any{},
			},
			latestAccount: &accountcore.Record{
				ID:          1,
				Credentials: map[string]any{},
			},
			expectedStale: false, // 两边都没有版本号，说明从未被异步刷新过，允许缓存
		},
		{
			name: "same_version",
			account: &accountcore.Record{
				ID:          1,
				Credentials: map[string]any{"_token_version": int64(100)},
			},
			latestAccount: &accountcore.Record{
				ID:          1,
				Credentials: map[string]any{"_token_version": int64(100)},
			},
			expectedStale: false,
		},
		{
			name: "current_version_newer",
			account: &accountcore.Record{
				ID:          1,
				Credentials: map[string]any{"_token_version": int64(200)},
			},
			latestAccount: &accountcore.Record{
				ID:          1,
				Credentials: map[string]any{"_token_version": int64(100)},
			},
			expectedStale: false,
		},
		{
			name: "current_version_older_stale",
			account: &accountcore.Record{
				ID:          1,
				Credentials: map[string]any{"_token_version": int64(100)},
			},
			latestAccount: &accountcore.Record{
				ID:          1,
				Credentials: map[string]any{"_token_version": int64(200)},
			},
			expectedStale: true, // 当前版本过时
		},
		{
			name: "repo_error",
			account: &accountcore.Record{
				ID:          1,
				Credentials: map[string]any{"_token_version": int64(100)},
			},
			latestAccount: nil,
			repoErr:       errors.New("db error"),
			expectedStale: false, // 查询失败，默认允许缓存
		},
		{
			name: "repo_returns_nil",
			account: &accountcore.Record{
				ID:          1,
				Credentials: map[string]any{"_token_version": int64(100)},
			},
			latestAccount: nil,
			repoErr:       nil,
			expectedStale: false, // 查询返回 nil，默认允许缓存
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 原表格直接验证所属实现，不在测试中保留第二份版本比较算法。
			repo := tokenVersionReader{value: tt.latestAccount, err: tt.repoErr}
			_, isStale := accountcore.CheckTokenVersion(context.Background(), tt.account, repo)
			require.Equal(t, tt.expectedStale, isStale)
		})
	}
}

func TestCheckTokenVersion_NilRepo(t *testing.T) {
	account := &accountcore.Record{
		ID:          1,
		Credentials: map[string]any{"_token_version": int64(100)},
	}
	_, isStale := accountcore.CheckTokenVersion(context.Background(), account, nil)
	require.False(t, isStale) // nil repo，默认允许缓存
}

// tokenVersionReader 只提供版本复核所需的单次账号查询结果。
type tokenVersionReader struct {
	value *accountcore.Record
	err   error
}

func (r tokenVersionReader) GetByID(context.Context, int64) (*accountcore.Record, error) {
	return r.value, r.err
}
