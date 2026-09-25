// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"errors"
	"fmt"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// CreateShadow 为指定 OpenAI OAuth 母账号创建 spark 维度影子账号（一母一影）。
// 安全不变量：Credentials 恒不含 auth token（仅 model_mapping，守卫 isAllowedSparkShadowCredentialsUpdate 放行）。
func (s *Admin) CreateShadow(ctx context.Context, parentID int64, opts ShadowOptions) (*Record, error) {
	// 1. 加载母账号并校验平台/类型
	parent, err := s.accountRepo.GetByID(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("get parent account: %w", err)
	}
	if !parent.IsOpenAIOAuth() {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "SPARK_SHADOW_INVALID_PARENT",
			"spark shadow requires an OpenAI OAuth parent account")
	}
	// G6:母账号本身不能是影子,否则会建出二级影子——resolveCredentialAccount 只解一层,
	// 会解析到无凭据的一级影子,进入坏调度/上游失败。
	if parent.IsCredentialShadow() {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "SPARK_SHADOW_PARENT_IS_SHADOW",
			"spark shadow parent must be a real account, not another spark shadow")
	}

	// 2. 一母一影校验
	shadows, err := s.accountRepo.ListShadowsByParent(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("check existing spark shadows: %w", err)
	}
	if len(shadows) > 0 {
		return nil, infraerrors.New(infraerrors.CategoryConflict, "SPARK_SHADOW_ALREADY_EXISTS",
			"parent account already has a spark shadow account")
	}

	// 3. 解析分组。未指定 GroupIDs 时:优先**继承母账号当前分组**(影子与母同路由域,母在自定义
	// 组时该组的 spark 请求也能选到影子;G1 决策);母无分组再回落 openai-default(F4)。
	// 显式指定 GroupIDs 时,与 UpdateAccount 对齐先校验存在性(创建前),避免建出影子后再因无效组
	// 失败而留下孤儿影子(一母一影唯一索引会挡住重试)——外审 C/P1。
	groupIDs := opts.GroupIDs
	if len(groupIDs) > 0 {
		if s.options.Groups != nil {
			if err := s.ValidateGroupIDs(ctx, groupIDs); err != nil {
				return nil, err
			}
		}
	} else if len(parent.GroupIDs) > 0 {
		groupIDs = append([]int64(nil), parent.GroupIDs...)
	} else if s.options.Groups != nil {
		defaultGroupName := PlatformOpenAI + "-default"
		if groups, gerr := s.options.Groups.ActiveGroups(ctx, PlatformOpenAI); gerr == nil {
			for _, g := range groups {
				if g.Name == defaultGroupName {
					groupIDs = []int64{g.ID}
					break
				}
			}
		}
	}

	// 4. 构造影子账号（安全不变量：Credentials 恒不含 auth token，仅含 model_mapping）。
	// name 为空时默认 "<母账号名> (Spark)"——否则空 name 会在 ent(name NotEmpty)处变成裸 500
	// (外审 E/P2);并 rune 安全截断到 ent MaxLen(100)。
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = parent.Name + " (Spark)"
	}
	if runes := []rune(name); len(runes) > 100 {
		name = string(runes[:100])
	}
	// 并发未指定(<=0)时继承母账号，避免 0 被限流器解读为"无限并发"（外审 F3）。
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = parent.Concurrency
	}
	// 优先级未指定(<=0)时继承母账号——前端一键创建只传 name,opts.Priority 省略即 0,而调度
	// 比较是「数值越小越优先」(openai_account_scheduler.isOpenAIAccountCandidateBetter),且 repo
	// 显式 SetPriority 会绕过 ent 默认 50,直写 0 会让影子意外抢到最高优先级(外审第5轮 P1)。
	// 与上方 Concurrency 一致采用「省略继承母账号」语义(影子的 proxy/分组/并发亦全部继承母账号)。
	priority := opts.Priority
	if priority <= 0 {
		priority = parent.Priority
	}
	shadow := &Record{
		Name:            name,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		Status:          StatusActive,
		Credentials:     map[string]any{"model_mapping": s.options.ShadowModels()},
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
		ProxyID:         parent.ProxyID,
		Priority:        priority,
		Concurrency:     concurrency,
		Schedulable:     true,
	}

	// 5. 持久化（Create 填充 shadow.ID）。并发竞态:预查(步骤2)放行后另一请求抢先建成,本次会撞
	// 一母一影唯一索引。复查确认确为"已存在"竞态时返回结构化 409 而非裸 500——外审 A/P1。
	if err := s.accountRepo.Create(ctx, shadow); err != nil {
		if existing, qerr := s.accountRepo.ListShadowsByParent(ctx, parentID); qerr == nil && len(existing) > 0 {
			return nil, infraerrors.New(infraerrors.CategoryConflict, "SPARK_SHADOW_ALREADY_EXISTS",
				"parent account already has a spark shadow account")
		}
		return nil, fmt.Errorf("create spark shadow: %w", err)
	}

	// 6. 绑定分组。注意:create+bind 非单一 DB 事务(通用 Create 走 r.client、outbox 走 r.sql,
	// 无现成共享事务路径),故绑组失败时做 best-effort 补偿删除刚建的影子,避免半成品影子(否则
	// 一母一影唯一索引会挡住重试)——外审 C/P1。补偿删除用 detached ctx,即便请求 ctx 已取消/超时
	// 仍能完成清理(外审第4轮);进程崩溃这种极端仍可能残留,属已知权衡。
	if len(groupIDs) > 0 {
		if err := s.accountRepo.BindGroups(ctx, shadow.ID, groupIDs); err != nil {
			if delErr := s.accountRepo.Delete(context.WithoutCancel(ctx), shadow.ID); delErr != nil {
				s.options.Error("spark_shadow_bind_groups_rollback_failed",
					"shadow_id", shadow.ID, "parent_id", parentID, "delete_err", delErr)
			}
			return nil, fmt.Errorf("bind groups for spark shadow: %w", err)
		}
		shadow.GroupIDs = groupIDs
	}

	return shadow, nil
}

// propagateProxyToShadows syncs proxyID to all spark shadow accounts of parentID.
// It is called synchronously so that proxy changes are immediately consistent;
// accountRepo.Update triggers the scheduler outbox + cache propagation internally.
// Calling this for a non-parent account is a harmless no-op.
func (s *Admin) propagateProxyToShadows(ctx context.Context, parentID int64, proxyID *int64) error {
	return PropagateAccountProxyToShadows(ctx, s.accountRepo, parentID, proxyID)
}

// PropagateAccountProxyToShadows 把母账号的 proxy 同步到其所有 spark 影子(影子 proxy 恒继承母账号)。
// 供 AdminService 编辑路径与 CRS 同步路径共用——后者改动母账号 proxy 后必须同样传播,否则影子保留
// 旧 proxy 出现出站漂移(外审第8轮)。
func PropagateAccountProxyToShadows(ctx context.Context, repo ShadowProxyStore, parentID int64, proxyID *int64) error {
	shadows, err := repo.ListShadowsByParent(ctx, parentID)
	if err != nil {
		return fmt.Errorf("list spark shadows for proxy propagation: %w", err)
	}
	for _, shadow := range shadows {
		shadow.ProxyID = proxyID
		if err := WriteConfiguration(ctx, repo, shadow, ConfigurationChange{Fields: ConfigProxyID}); err != nil {
			return fmt.Errorf("update spark shadow %d proxy: %w", shadow.ID, err)
		}
	}
	return nil
}

func (s *Admin) RevertAccountProxyFallback(ctx context.Context, id int64) error {
	if err := s.accountRepo.RevertProxyFallback(ctx, id); err != nil {
		return err
	}
	// 加载回退后的账号以获取实际 ProxyID，再传播到影子账号
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get account after proxy revert: %w", err)
	}
	return s.propagateProxyToShadows(ctx, id, account.ProxyID)
}
func (s *Admin) ValidateGroupIDs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if s.options.Groups == nil {
		return errors.New("group repository not configured")
	}
	return s.options.Groups.ValidateGroups(ctx, ids)
}
