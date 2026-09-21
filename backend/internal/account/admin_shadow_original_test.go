//go:build unit

package account_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

// sparkShadowRepoStub 是 AccountRepository 的内存测试桩，
// 专为 CreateShadow 单元测试设计。
// 嵌入 mockAccountRepoForGemini（由 gemini_multiplatform_test.go 提供所有 stub 方法），
// 并覆盖测试所需的核心方法。
type sparkShadowRepoStub struct {
	accountcore.AdminStore
	accountsByID map[int64]*accountcore.Record
	nextID       int64
	accounts     map[int64]*accountcore.Record
	groupsOf     map[int64][]int64 // accountID → []groupIDs
}

func newSparkShadowRepoStub() *sparkShadowRepoStub {
	return &sparkShadowRepoStub{
		nextID:       0,
		accounts:     make(map[int64]*accountcore.Record),
		groupsOf:     make(map[int64][]int64),
		accountsByID: make(map[int64]*accountcore.Record),
	}
}

func (s *sparkShadowRepoStub) Create(_ context.Context, account *accountcore.Record) error {
	s.nextID++
	account.ID = s.nextID
	cp := *accountcore.CloneRecord(account)
	s.accounts[account.ID] = &cp
	s.accountsByID[account.ID] = &cp
	return nil
}

func (s *sparkShadowRepoStub) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	acc, ok := s.accounts[id]
	if !ok {
		return nil, accountcore.ErrAccountNotFound
	}
	return accountcore.CloneRecord(acc), nil
}

func (s *sparkShadowRepoStub) ListShadowsByParent(_ context.Context, parentID int64) ([]*accountcore.Record, error) {
	var result []*accountcore.Record
	for _, acc := range s.accounts {
		if acc.ParentAccountID != nil && *acc.ParentAccountID == parentID && acc.QuotaDimension == accountcore.QuotaDimensionSpark {
			cp := *accountcore.CloneRecord(acc)
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (s *sparkShadowRepoStub) BindGroups(_ context.Context, accountID int64, groupIDs []int64) error {
	s.groupsOf[accountID] = append(s.groupsOf[accountID], groupIDs...)
	return nil
}

func (s *sparkShadowRepoStub) ListSchedulableByGroupID(_ context.Context, groupID int64) ([]accountcore.Record, error) {
	var result []accountcore.Record
	for accID, groups := range s.groupsOf {
		for _, gid := range groups {
			if gid == groupID {
				if acc, ok := s.accounts[accID]; ok {
					result = append(result, *acc)
				}
				break
			}
		}
	}
	return result, nil
}

// ── 追加 stub（AccountRepository に必要な残りのメソッド）──────────────────
func (s *sparkShadowRepoStub) ExistsByID(_ context.Context, id int64) (bool, error) {
	_, ok := s.accounts[id]
	return ok, nil
}

func (s *sparkShadowRepoStub) Update(_ context.Context, account *accountcore.Record) error {
	if _, ok := s.accounts[account.ID]; !ok {
		return accountcore.ErrAccountNotFound
	}
	cp := *accountcore.CloneRecord(account)
	s.accounts[account.ID] = &cp
	s.accountsByID[account.ID] = &cp
	return nil
}

func (s *sparkShadowRepoStub) Delete(_ context.Context, id int64) error {
	delete(s.accounts, id)
	delete(s.accountsByID, id)
	return nil
}

func (s *sparkShadowRepoStub) BatchUpdateLastUsed(_ context.Context, _ map[int64]time.Time) error {
	return nil
}

func (s *sparkShadowRepoStub) ListByGroup(_ context.Context, _ int64) ([]accountcore.Record, error) {
	return nil, nil
}

func (s *sparkShadowRepoStub) ListWithFilters(_ context.Context, _ pagination.PaginationParams, _, _, _, _ string, _ int64, _ string) ([]accountcore.Record, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

// TestCreateShadow はメインのシナリオを検証する。
//
// Test 1 — 基本生成: ParentAccountID / QuotaDimension / 默认 spark model_mapping / 无 auth token / ProxyID 継承
// Test 2 — 一母一影: 二度目の生成はエラー
func TestCreateShadow(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)

	proxyID := int64(7)
	parent := &accountcore.Record{
		Name:     "p",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
		ProxyID:  &proxyID,
		Credentials: map[string]any{
			"refresh_token":      "RT",
			"chatgpt_account_id": "org-x",
		},
	}
	require.NoError(t, repo.Create(ctx, parent))

	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "p-spark", Priority: 50})
	require.NoError(t, err)
	require.NotNil(t, shadow)
	require.Equal(t, parent.ID, *shadow.ParentAccountID)
	require.Equal(t, accountcore.QuotaDimensionSpark, shadow.QuotaDimension)
	require.Equal(t, accountprovider.DefaultSparkShadowModels(), shadow.Credentials["model_mapping"],
		"影子默认带 spark 恒等变体映射")
	require.Nil(t, shadow.Credentials["refresh_token"], "影子不得持有 auth token")
	require.Nil(t, shadow.Credentials["access_token"], "影子不得持有 auth token")
	require.Equal(t, parent.ProxyID, shadow.ProxyID)
	require.NotContains(t, shadow.Extra, "openai_long_context_billing_enabled")

	_, err = svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "dup"})
	require.Error(t, err)
}

// TestCreateShadow_BindGroups は BindGroups の後置呼び出しを検証する。
// 影子账号が指定グループに属し、ListSchedulableByGroupID で取得可能であること。
func TestCreateShadow_BindGroups(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)

	parent := &accountcore.Record{
		Name:     "parent",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"chatgpt_account_id": "org-y",
		},
	}
	require.NoError(t, repo.Create(ctx, parent))

	const testGroupID = int64(42)
	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{
		Name:     "p-spark",
		GroupIDs: []int64{testGroupID},
	})
	require.NoError(t, err)
	require.NotNil(t, shadow)
	require.Equal(t, []int64{testGroupID}, shadow.GroupIDs, "CreateShadow should backfill GroupIDs into the returned shadow")

	accounts, err := repo.ListSchedulableByGroupID(ctx, testGroupID)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, shadow.ID, accounts[0].ID)
}

// TestDeleteAccount_CascadeToShadow verifies that deleting a parent account also
// deletes its spark shadow account.
// TestCreateShadow_InheritsParentConcurrency 验证外审 F3:未指定并发时
// 影子继承母账号并发,避免 Concurrency=0 被限流器当作"无限并发"。
func TestCreateShadow_InheritsParentConcurrency(t *testing.T) {
	ctx := context.Background()

	t.Run("unspecified_inherits_parent", func(t *testing.T) {
		repo := newSparkShadowRepoStub()
		svc := newOriginalAccountEditor(repo)
		parent := &accountcore.Record{
			Name: "conc-parent", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
			Status: billing.StatusActive, Concurrency: 3,
			Credentials: map[string]any{"chatgpt_account_id": "org-c"},
		}
		require.NoError(t, repo.Create(ctx, parent))

		shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "conc-shadow"})
		require.NoError(t, err)
		require.Equal(t, 3, shadow.Concurrency, "未指定并发应继承母账号(非 0=无限)")
		require.Equal(t, 3, repo.accounts[shadow.ID].Concurrency)
	})

	t.Run("explicit_positive_kept", func(t *testing.T) {
		repo := newSparkShadowRepoStub()
		svc := newOriginalAccountEditor(repo)
		parent := &accountcore.Record{
			Name: "conc-parent2", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
			Status: billing.StatusActive, Concurrency: 3,
			Credentials: map[string]any{"chatgpt_account_id": "org-c2"},
		}
		require.NoError(t, repo.Create(ctx, parent))

		shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "conc-shadow2", Concurrency: 2})
		require.NoError(t, err)
		require.Equal(t, 2, shadow.Concurrency, "显式正并发应保留")
	})
}

// TestCreateShadow_InheritsParentPriorityWhenOmitted 验证外审第5轮 P1:未指定优先级时
// 影子继承母账号 priority,而非直写 0 抢到最高调度优先级(repo SetPriority 绕过 ent 默认 50,
// 调度比较数值越小越优先;前端一键创建只传 name 即触发该路径)。
func TestCreateShadow_InheritsParentPriorityWhenOmitted(t *testing.T) {
	ctx := context.Background()

	t.Run("unspecified_inherits_parent", func(t *testing.T) {
		repo := newSparkShadowRepoStub()
		svc := newOriginalAccountEditor(repo)
		parent := &accountcore.Record{
			Name: "prio-parent", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
			Status: billing.StatusActive, Priority: 30,
			Credentials: map[string]any{"chatgpt_account_id": "org-p"},
		}
		require.NoError(t, repo.Create(ctx, parent))

		shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "prio-shadow"})
		require.NoError(t, err)
		require.Equal(t, 30, shadow.Priority, "未指定优先级应继承母账号(而非 0=最高优先级)")
		require.Equal(t, 30, repo.accounts[shadow.ID].Priority)
	})

	t.Run("explicit_positive_kept", func(t *testing.T) {
		repo := newSparkShadowRepoStub()
		svc := newOriginalAccountEditor(repo)
		parent := &accountcore.Record{
			Name: "prio-parent2", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
			Status: billing.StatusActive, Priority: 30,
			Credentials: map[string]any{"chatgpt_account_id": "org-p2"},
		}
		require.NoError(t, repo.Create(ctx, parent))

		shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "prio-shadow2", Priority: 7})
		require.NoError(t, err)
		require.Equal(t, 7, shadow.Priority, "显式正优先级应保留")
	})
}

// TestPersistAccountCredentials_SkipsShadow 验证外审第6轮 P1:凭据写入唯一汇聚点
// persistAccountCredentials 对 spark 影子早返 no-op,任何上游路径都无法把凭据落到影子行。
func TestPersistAccountCredentials_SkipsShadow(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	parentID := int64(1)
	shadow := &accountcore.Record{
		Name: "shadow", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Credentials: map[string]any{},
		ParentAccountID: &parentID, QuotaDimension: accountcore.QuotaDimensionSpark,
	}
	require.NoError(t, repo.Create(ctx, shadow))

	_, err := accountcore.PersistCredentials(ctx, repo, shadow, map[string]any{"access_token": "LEAK", "refresh_token": "LEAK"}, nil)
	require.NoError(t, err)
	require.Empty(t, shadow.Credentials, "影子凭据不可被写入(传入对象)")
	require.Empty(t, repo.accounts[shadow.ID].Credentials, "影子凭据不可被写入(仓储)")
}

// TestResolveCredentialAccount_RejectsParentShadow 验证外审第6轮 P2 防御:畸形数据/手工 DB
// 写出的「影子→影子」链,凭据解析必须 fail-closed 而非停在无凭据的一级影子。
func TestResolveCredentialAccount_RejectsParentShadow(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()

	grandparent := &accountcore.Record{
		Name: "gp", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive,
		Credentials: map[string]any{"refresh_token": "RT"},
	}
	require.NoError(t, repo.Create(ctx, grandparent))

	parentShadow := &accountcore.Record{
		Name: "parent-shadow", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive,
		Credentials: map[string]any{}, ParentAccountID: &grandparent.ID, QuotaDimension: accountcore.QuotaDimensionSpark,
	}
	require.NoError(t, repo.Create(ctx, parentShadow))
	child := &accountcore.Record{
		Name: "child-shadow", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive,
		Credentials: map[string]any{}, ParentAccountID: &parentShadow.ID, QuotaDimension: accountcore.QuotaDimensionSpark,
	}
	require.NoError(t, repo.Create(ctx, child))

	_, err := accountcore.ResolveCredentialRecord(ctx, repo.GetByID, child)
	require.Error(t, err, "父账号本身是影子时凭据解析应拒绝(fail-closed)")
}

// TestResetAccountQuota_RejectsShadow 验证外审第7轮 P2:通用 reset-quota 对影子明确 400 拒绝
// (影子不持自有配额,语义不一致),且母账号仍可正常重置。
func TestResetAccountQuota_RejectsShadow(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)
	parent := &accountcore.Record{
		Name: "rq-parent", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive,
		Credentials: map[string]any{"chatgpt_account_id": "o"},
	}
	require.NoError(t, repo.Create(ctx, parent))
	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "rq-shadow"})
	require.NoError(t, err)

	err = svc.ResetAccountQuota(ctx, shadow.ID)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err), "影子 reset-quota 应 400")

	require.NoError(t, svc.ResetAccountQuota(ctx, parent.ID), "母账号 reset-quota 应放行")
}

// TestCreateShadow_RejectsShadowAsParent 验证外审 G6:不允许把影子当母创建二级影子。
func TestCreateShadow_RejectsShadowAsParent(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)

	parent := &accountcore.Record{
		Name: "real-parent", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Credentials: map[string]any{"chatgpt_account_id": "org-x"},
	}
	require.NoError(t, repo.Create(ctx, parent))
	firstShadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "first-shadow"})
	require.NoError(t, err)

	_, err = svc.CreateShadow(ctx, firstShadow.ID, accountcore.ShadowOptions{Name: "second-shadow"})
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err), "影子当母应返回 400")
}

// TestCreateShadow_StructuredErrors 验证外审 G3:可预期业务错误返回结构化 4xx 而非 500。
func TestCreateShadow_StructuredErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("non_oauth_parent_400", func(t *testing.T) {
		repo := newSparkShadowRepoStub()
		svc := newOriginalAccountEditor(repo)
		parent := &accountcore.Record{Name: "apikey-parent", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: billing.StatusActive}
		require.NoError(t, repo.Create(ctx, parent))
		_, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "s"})
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err), "非 OAuth 母账号应 400")
	})

	t.Run("duplicate_409", func(t *testing.T) {
		repo := newSparkShadowRepoStub()
		svc := newOriginalAccountEditor(repo)
		parent := &accountcore.Record{Name: "p", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Credentials: map[string]any{"chatgpt_account_id": "o"}}
		require.NoError(t, repo.Create(ctx, parent))
		_, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "s1"})
		require.NoError(t, err)
		_, err = svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "s2"})
		require.Error(t, err)
		require.Equal(t, http.StatusConflict, s15httpx.ErrorCode(err), "重复创建应 409")
	})
}

// TestUpdateAccount_RejectsTypeChangeOnShadow 验证外审 G7:影子 type 不可被普通更新改坏。
func TestUpdateAccount_RejectsTypeChangeOnShadow(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)
	parent := &accountcore.Record{
		Name: "type-parent", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Credentials: map[string]any{"chatgpt_account_id": "org-t"},
	}
	require.NoError(t, repo.Create(ctx, parent))
	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "type-shadow"})
	require.NoError(t, err)

	_, err = svc.UpdateAccount(ctx, shadow.ID, &accountcore.UpdateAccountInput{Type: capability.AccountTypeAPIKey})
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err), "改影子 type 应 400")
	require.Equal(t, capability.AccountTypeOAuth, repo.accounts[shadow.ID].Type, "影子 type 必须保持 oauth")

	_, err = svc.UpdateAccount(ctx, shadow.ID, &accountcore.UpdateAccountInput{Type: capability.AccountTypeOAuth})
	require.NoError(t, err, "传入相同 type 应允许")
}

// TestBulkUpdateAccounts_RejectsCredentialWriteToShadow 验证外审 G5:批量更新携带凭据时
// 目标含影子必须被拒(与单账号 UpdateAccount 守卫对齐,堵住 bulk 绕过)。
func TestBulkUpdateAccounts_RejectsCredentialWriteToShadow(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)
	parent := &accountcore.Record{
		Name: "bulk-parent", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Credentials: map[string]any{"chatgpt_account_id": "org-b", "access_token": "t"},
	}
	require.NoError(t, repo.Create(ctx, parent))
	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "bulk-shadow"})
	require.NoError(t, err)

	_, err = svc.BulkUpdateAccounts(ctx, &accountcore.BulkUpdateAccountsInput{
		AccountIDs:  []int64{shadow.ID},
		Credentials: map[string]any{"access_token": "leaked"},
	})
	require.Error(t, err, "批量给影子写凭据必须被拒")
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err), "应 400")

	require.Empty(t, repo.accounts[shadow.ID].GetOpenAIAccessToken(), "影子 access_token 必须保持为空 —— 批量写入未生效")
}

func TestDeleteAccount_CascadeToShadow(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)

	parent := &accountcore.Record{
		Name:        "cascade-parent",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Credentials: map[string]any{"chatgpt_account_id": "org-cascade"},
	}
	require.NoError(t, repo.Create(ctx, parent))

	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "cascade-shadow"})
	require.NoError(t, err)
	shadowID := shadow.ID

	_, ok := repo.accounts[parent.ID]
	require.True(t, ok)
	_, ok = repo.accounts[shadowID]
	require.True(t, ok)

	require.NoError(t, svc.DeleteAccount(ctx, parent.ID))

	_, ok = repo.accounts[parent.ID]
	require.False(t, ok, "parent account should be deleted")

	_, ok = repo.accounts[shadowID]
	require.False(t, ok, "shadow account should be cascade-deleted")
}

// TestUpdateAccount_PropagatesProxyToShadow verifies that updating a parent
// account's ProxyID propagates the new value to its spark shadow.
func TestUpdateAccount_PropagatesProxyToShadow(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)

	oldProxy := int64(7)
	parent := &accountcore.Record{
		Name:        "proxy-parent",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		ProxyID:     &oldProxy,
		Credentials: map[string]any{"chatgpt_account_id": "org-proxy"},
	}
	require.NoError(t, repo.Create(ctx, parent))

	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "proxy-shadow"})
	require.NoError(t, err)
	shadowID := shadow.ID

	newProxy := int64(42)
	_, err = svc.UpdateAccount(ctx, parent.ID, &accountcore.UpdateAccountInput{ProxyID: &newProxy})
	require.NoError(t, err)

	storedShadow, ok := repo.accounts[shadowID]
	require.True(t, ok)
	require.NotNil(t, storedShadow.ProxyID)
	require.Equal(t, newProxy, *storedShadow.ProxyID)
}

// TestUpdateAccount_RejectsCredentialWriteToShadow 验证安全不变量「影子绝不持有鉴权凭据」
// 在通用更新路径(UpdateAccount,被 edit/re-auth/refresh/batch 共用)上也被守住:
// 对影子写入 access_token/refresh_token 必须被拒绝,且影子的 access_token/refresh_token
// 保持为空(Credentials 本身允许持有 CreateShadow 写入的 model_mapping,故不能断言整体为空)。
func TestUpdateAccount_RejectsCredentialWriteToShadow(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)

	parent := &accountcore.Record{
		Name:        "cred-parent",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Credentials: map[string]any{"access_token": "parent-secret", "refresh_token": "parent-rt"},
	}
	require.NoError(t, repo.Create(ctx, parent))

	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "cred-shadow"})
	require.NoError(t, err)
	require.Empty(t, shadow.GetOpenAIAccessToken(), "前提:影子创建后不持有 access_token")
	require.Empty(t, shadow.GetOpenAIRefreshToken(), "前提:影子创建后不持有 refresh_token")

	_, err = svc.UpdateAccount(ctx, shadow.ID, &accountcore.UpdateAccountInput{
		Credentials: map[string]any{"access_token": "leaked", "refresh_token": "leaked-rt"},
	})
	require.Error(t, err, "对影子写入凭据必须被拒绝")

	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err), "应映射为 400 而非 500")

	storedShadow, ok := repo.accounts[shadow.ID]
	require.True(t, ok)
	require.Empty(t, storedShadow.GetOpenAIAccessToken(), "影子 access_token 必须保持为空 —— 凭据未被写入")
	require.Empty(t, storedShadow.GetOpenAIRefreshToken(), "影子 refresh_token 必须保持为空 —— 凭据未被写入")

	newPriority := 5
	_, err = svc.UpdateAccount(ctx, shadow.ID, &accountcore.UpdateAccountInput{Priority: &newPriority})
	require.NoError(t, err, "影子的非凭据字段更新应正常")
}

// TestBulkUpdateAccounts_PropagatesProxyToShadow verifies that bulk-updating
// accounts' ProxyID propagates the new value to each account's spark shadow.
func TestBulkUpdateAccounts_PropagatesProxyToShadow(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)

	oldProxy := int64(7)
	parent := &accountcore.Record{
		Name:        "bulk-parent",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		ProxyID:     &oldProxy,
		Credentials: map[string]any{"chatgpt_account_id": "org-bulk"},
	}
	require.NoError(t, repo.Create(ctx, parent))

	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "bulk-shadow"})
	require.NoError(t, err)
	shadowID := shadow.ID

	newProxy := int64(99)
	_, err = svc.BulkUpdateAccounts(ctx, &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{parent.ID},
		ProxyID:    &newProxy,
	})
	require.NoError(t, err)

	storedShadow, ok := repo.accounts[shadowID]
	require.True(t, ok)
	require.NotNil(t, storedShadow.ProxyID)
	require.Equal(t, newProxy, *storedShadow.ProxyID)
}

// raceCreateRepoStub 模拟并发竞态:对影子的 Create 撞一母一影唯一索引(返回错误),
// 且复查时另一并发请求的影子已存在 → CreateShadow 应映射为结构化 409(外审 A/P1)。
type raceCreateRepoStub struct {
	*sparkShadowRepoStub
}

func (s *raceCreateRepoStub) Create(ctx context.Context, account *accountcore.Record) error {
	if account.ParentAccountID != nil {

		s.nextID++
		phantom := *account
		phantom.ID = s.nextID
		s.accounts[phantom.ID] = &phantom
		return errors.New(`duplicate key value violates unique constraint "uq_accounts_spark_shadow_per_parent"`)
	}
	return s.sparkShadowRepoStub.Create(ctx, account)
}

// TestCreateShadow_DefaultsNameFromParent 验证外审 E/P2:空 name 不应 500,
// 而是默认 "<母账号名> (Spark)"。
func TestCreateShadow_DefaultsNameFromParent(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)
	parent := &accountcore.Record{
		Name: "mum", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Credentials: map[string]any{"chatgpt_account_id": "o"},
	}
	require.NoError(t, repo.Create(ctx, parent))

	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "   "})
	require.NoError(t, err, "空/空白 name 不应 500,应默认命名")
	require.Equal(t, "mum (Spark)", shadow.Name)
}

// TestCreateShadow_ConcurrentCreateReturns409 验证外审 A/P1:并发竞态下预查放行后
// Create 撞唯一索引,应映射结构化 409 而非裸 500。
func TestCreateShadow_ConcurrentCreateReturns409(t *testing.T) {
	ctx := context.Background()
	base := newSparkShadowRepoStub()
	repo := &raceCreateRepoStub{sparkShadowRepoStub: base}
	svc := newOriginalAccountEditor(repo)
	parent := &accountcore.Record{
		Name: "p", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Credentials: map[string]any{"chatgpt_account_id": "o"},
	}
	require.NoError(t, base.Create(ctx, parent))

	_, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "s"})
	require.Error(t, err)
	require.Equal(t, http.StatusConflict, s15httpx.ErrorCode(err), "并发竞态撞唯一索引应映射 409 而非 500")
}

// TestUpdateAccount_RejectsParentTypeChangeWithShadow 验证外审 D/P1:母账号有 spark 影子时,
// 不能把 type 改出 OpenAI OAuth(否则影子被调度后透传凭据解析必失败)。
func TestUpdateAccount_RejectsParentTypeChangeWithShadow(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)
	parent := &accountcore.Record{
		Name: "p", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Credentials: map[string]any{"chatgpt_account_id": "o"},
	}
	require.NoError(t, repo.Create(ctx, parent))
	_, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "s"})
	require.NoError(t, err)

	_, err = svc.UpdateAccount(ctx, parent.ID, &accountcore.UpdateAccountInput{Type: capability.AccountTypeAPIKey})
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err), "母账号有影子时改 type 出 oauth 应 400")
	require.Equal(t, capability.AccountTypeOAuth, repo.accounts[parent.ID].Type, "母账号 type 必须保持 oauth")

	_, err = svc.UpdateAccount(ctx, parent.ID, &accountcore.UpdateAccountInput{Type: capability.AccountTypeOAuth})
	require.NoError(t, err, "传入相同 type(no-op)应允许")
}

// TestUpdateAccount_IgnoresProxyChangeOnShadow 验证外审 B/P1:影子 proxy 恒继承母账号,
// 普通更新不得独立改动。
func TestUpdateAccount_IgnoresProxyChangeOnShadow(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)
	parentProxy := int64(7)
	parent := &accountcore.Record{
		Name: "p", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, ProxyID: &parentProxy,
		Credentials: map[string]any{"chatgpt_account_id": "o"},
	}
	require.NoError(t, repo.Create(ctx, parent))
	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "s"})
	require.NoError(t, err)
	require.NotNil(t, repo.accounts[shadow.ID].ProxyID)
	require.Equal(t, parentProxy, *repo.accounts[shadow.ID].ProxyID, "前提:影子继承母 proxy=7")

	newProxy := int64(42)
	_, err = svc.UpdateAccount(ctx, shadow.ID, &accountcore.UpdateAccountInput{ProxyID: &newProxy})
	require.NoError(t, err, "影子的非 proxy 字段更新仍应成功")
	require.NotNil(t, repo.accounts[shadow.ID].ProxyID)
	require.Equal(t, parentProxy, *repo.accounts[shadow.ID].ProxyID, "影子 proxy 不应被独立改动,恒继承母账号")
}

func TestUpdateAccount_ShadowEmptyCredentialsClearsModelMapping(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)
	parentID := int64(1)
	shadow := &accountcore.Record{
		Name:            "s",
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		Status:          billing.StatusActive,
		ParentAccountID: &parentID,
		QuotaDimension:  accountcore.QuotaDimensionSpark,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.3-codex-spark": "gpt-5.3-codex-spark",
			},
		},
	}
	require.NoError(t, repo.Create(ctx, shadow))

	updated, err := svc.UpdateAccount(ctx, shadow.ID, &accountcore.UpdateAccountInput{
		Credentials: map[string]any{},
	})

	require.NoError(t, err)
	require.Empty(t, updated.Credentials)
	require.Empty(t, repo.accounts[shadow.ID].Credentials)
}

func TestUpdateAccount_ShadowRejectsAuthCredentials(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	svc := newOriginalAccountEditor(repo)
	parentID := int64(1)
	shadow := &accountcore.Record{
		Name:            "s",
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		Status:          billing.StatusActive,
		ParentAccountID: &parentID,
		QuotaDimension:  accountcore.QuotaDimensionSpark,
		Credentials:     map[string]any{},
	}
	require.NoError(t, repo.Create(ctx, shadow))

	_, err := svc.UpdateAccount(ctx, shadow.ID, &accountcore.UpdateAccountInput{
		Credentials: map[string]any{"access_token": "leak"},
	})

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
	require.Empty(t, repo.accounts[shadow.ID].Credentials)
}

// GetByIDs 保留原 Gemini 替身的按请求顺序读取，不新增缺失项。
func (s *sparkShadowRepoStub) GetByIDs(_ context.Context, ids []int64) ([]*accountcore.Record, error) {
	var out []*accountcore.Record
	for _, id := range ids {
		if value, ok := s.accountsByID[id]; ok {
			out = append(out, accountcore.CloneRecord(value))
		}
	}
	return out, nil
}

// BulkUpdate 保留原替身的零行结果，影子同步仍由真实管理用例执行。
func (*sparkShadowRepoStub) BulkUpdate(context.Context, []int64, accountcore.AccountBulkUpdate) (int64, error) {
	return 0, nil
}
func (*sparkShadowRepoStub) ResetQuotaUsedAndClearRateLimitCooldown(context.Context, int64) error {
	return nil
}
