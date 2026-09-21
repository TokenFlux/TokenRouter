//go:build unit

package account_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"github.com/stretchr/testify/require"
)

// sparkShadowGroupRepoStub 嵌入 groupRepoStub(其余方法 panic),仅覆写
// ListActiveByPlatform 以供 F4 默认绑组测试。
type sparkShadowGroupRepoStub struct {
	routing.GroupRepository
	groups []routing.Group
}

func (s *sparkShadowGroupRepoStub) ListActiveByPlatform(_ context.Context, _ string) ([]routing.Group, error) {
	return s.groups, nil
}

// TestCreateShadow_DefaultGroupBinding 验证外审 F4:未指定 group_ids 时
// 影子回落绑定 openai-default 组(否则无组、组内路由选不到)。
func TestCreateShadow_DefaultGroupBinding(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	groupRepo := &sparkShadowGroupRepoStub{
		groups: []routing.Group{
			{ID: 99, Name: capability.PlatformOpenAI + "-default"},
			{ID: 7, Name: "some-other-group"},
		},
	}
	svc := newOriginalAccountEditor(repo, originalShadowGroups{groupRepo})

	parent := &accountcore.Record{
		Name: "grp-parent", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Credentials: map[string]any{"chatgpt_account_id": "org-g"},
	}
	require.NoError(t, repo.Create(ctx, parent))

	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "grp-shadow"})
	require.NoError(t, err)
	require.Equal(t, []int64{99}, repo.groupsOf[shadow.ID], "未指定分组应回落绑定 openai-default(id=99)")
}

// TestCreateShadow_InheritsParentGroups 验证外审 G1:未指定 group_ids 时
// 影子继承母账号当前分组(而非仅 openai-default),以便母在自定义组时影子也可路由。
func TestCreateShadow_InheritsParentGroups(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()

	groupRepo := &sparkShadowGroupRepoStub{groups: []routing.Group{{ID: 99, Name: capability.PlatformOpenAI + "-default"}}}
	svc := newOriginalAccountEditor(repo, originalShadowGroups{groupRepo})

	parent := &accountcore.Record{
		Name: "grp-parent", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, GroupIDs: []int64{11, 22},
		Credentials: map[string]any{"chatgpt_account_id": "org-grp"},
	}
	require.NoError(t, repo.Create(ctx, parent))

	shadow, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "grp-shadow"})
	require.NoError(t, err)
	require.Equal(t, []int64{11, 22}, repo.groupsOf[shadow.ID], "未指定分组应继承母账号分组,而非 openai-default")
}

// bindFailRepoStub 让 BindGroups 失败,用于验证绑组失败时补偿删除刚建的影子(外审 C/P1)。
type bindFailRepoStub struct {
	*sparkShadowRepoStub
}

func (s *bindFailRepoStub) BindGroups(_ context.Context, _ int64, _ []int64) error {
	return errors.New("simulated bind failure")
}

// sparkShadowValidatingGroupRepoStub 实现 groupExistenceBatchReader(ExistsByIDs),
// 使 validateGroupIDsExist 走批量存在性校验路径。
type sparkShadowValidatingGroupRepoStub struct {
	routing.GroupRepository
	existing map[int64]bool
}

func (s *sparkShadowValidatingGroupRepoStub) ExistsByIDs(_ context.Context, ids []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = s.existing[id]
	}
	return out, nil
}

// TestCreateShadow_InvalidGroupRejectedNoOrphan 验证外审 C/P1:显式无效分组应在
// 创建前被拒,不留孤儿影子。
func TestCreateShadow_InvalidGroupRejectedNoOrphan(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	groupRepo := &sparkShadowValidatingGroupRepoStub{existing: map[int64]bool{7: true}}
	svc := newOriginalAccountEditor(repo, originalShadowGroups{groupRepo})
	parent := &accountcore.Record{
		Name: "p", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Credentials: map[string]any{"chatgpt_account_id": "o"},
	}
	require.NoError(t, repo.Create(ctx, parent))

	_, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "s", GroupIDs: []int64{999}})
	require.Error(t, err, "无效分组应在创建前被拒")

	shadows, qerr := repo.ListShadowsByParent(ctx, parent.ID)
	require.NoError(t, qerr)
	require.Empty(t, shadows, "无效分组应在创建前被拒,不应建出影子")
}

// TestCreateShadow_BindFailureRollsBackShadow 验证外审 C/P1:绑组失败时补偿删除
// 刚建的影子,不留孤儿(否则一母一影唯一索引会挡住重试)。
func TestCreateShadow_BindFailureRollsBackShadow(t *testing.T) {
	ctx := context.Background()
	base := newSparkShadowRepoStub()
	repo := &bindFailRepoStub{sparkShadowRepoStub: base}
	groupRepo := &sparkShadowValidatingGroupRepoStub{existing: map[int64]bool{7: true}}
	svc := newOriginalAccountEditor(repo, originalShadowGroups{groupRepo})
	parent := &accountcore.Record{
		Name: "p", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Credentials: map[string]any{"chatgpt_account_id": "o"},
	}
	require.NoError(t, base.Create(ctx, parent))

	_, err := svc.CreateShadow(ctx, parent.ID, accountcore.ShadowOptions{Name: "s", GroupIDs: []int64{7}})
	require.Error(t, err, "绑组失败应返回错误")

	shadows, qerr := base.ListShadowsByParent(ctx, parent.ID)
	require.NoError(t, qerr)
	require.Empty(t, shadows, "绑组失败后应补偿删除影子,不留孤儿")
}

func TestUpdateAccount_ShadowAllowsModelMappingAndGroupUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	groupRepo := &sparkShadowValidatingGroupRepoStub{existing: map[int64]bool{7: true}}
	svc := newOriginalAccountEditor(repo, originalShadowGroups{groupRepo})
	parentID := int64(1)
	parent := &accountcore.Record{
		ID:       parentID,
		Name:     "p",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"access_token":       "parent-token",
			"chatgpt_account_id": "org-parent",
		},
	}
	require.NoError(t, repo.Create(ctx, parent))
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

	groupIDs := []int64{7}
	updated, err := svc.UpdateAccount(ctx, shadow.ID, &accountcore.UpdateAccountInput{
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.3-codex-spark": "gpt-5.3-codex-spark",
			},
		},
		GroupIDs: &groupIDs,
	})

	require.NoError(t, err)
	require.Equal(t, []int64{7}, repo.groupsOf[shadow.ID])
	require.Equal(t, map[string]any{"gpt-5.3-codex-spark": "gpt-5.3-codex-spark"}, updated.Credentials["model_mapping"])
	require.Empty(t, updated.GetOpenAIAccessToken(), "影子账号不可持有母账号 access_token")
}

// TestBulkUpdateAccounts_RejectsProxyChangeOnShadow 验证外审第4轮 P1:批量更新携带 proxy 且
// 目标含影子必须被拒(与单账号 UpdateAccount 守卫对齐,堵住 bulk 绕过"proxy 恒继承母账号")。
func TestBulkUpdateAccounts_RejectsProxyChangeOnShadow(t *testing.T) {
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

	newProxy := int64(42)
	_, err = svc.BulkUpdateAccounts(ctx, &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{shadow.ID},
		ProxyID:    &newProxy,
	})
	require.Error(t, err, "批量给影子改 proxy 必须被拒")
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err), "应 400")
	require.NotNil(t, repo.accounts[shadow.ID].ProxyID)
	require.Equal(t, parentProxy, *repo.accounts[shadow.ID].ProxyID, "影子 proxy 必须保持继承母账号")
}

// originalShadowGroups 保留原分组查询及校验路径，只投影账号需要的字段。
type originalShadowGroups struct{ routing.GroupRepository }

func (g originalShadowGroups) DefaultGroup(ctx context.Context, platform string) (*accountcore.GroupReference, error) {
	v, err := routing.FindPlatformDefaultGroup(ctx, g.GroupRepository, platform)
	return originalShadowGroupReference(v), err
}
func (g originalShadowGroups) GetGroup(ctx context.Context, id int64) (*accountcore.GroupReference, error) {
	v, err := g.GetByID(ctx, id)
	return originalShadowGroupReference(v), err
}
func (g originalShadowGroups) ActiveGroups(ctx context.Context, platform string) ([]accountcore.GroupReference, error) {
	rows, err := g.ListActiveByPlatform(ctx, platform)
	if rows == nil {
		return nil, err
	}
	out := make([]accountcore.GroupReference, len(rows))
	for i := range rows {
		out[i] = *originalShadowGroupReference(&rows[i])
	}
	return out, err
}
func (g originalShadowGroups) ValidateGroups(ctx context.Context, ids []int64) error {
	return routing.ValidateGroupIDs(ctx, g.GroupRepository, ids)
}
func originalShadowGroupReference(v *routing.Group) *accountcore.GroupReference {
	if v == nil {
		return nil
	}
	return &accountcore.GroupReference{ID: v.ID, Name: v.Name, Platform: v.Platform, RequireOAuthOnly: v.RequireOAuthOnly}
}
