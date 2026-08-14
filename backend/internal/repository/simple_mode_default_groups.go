package repository

import (
	"context"
	"fmt"

	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/BrandonVee/TokenRouter/ent/group"
	"github.com/BrandonVee/TokenRouter/internal/domain"
	"github.com/BrandonVee/TokenRouter/internal/service"
)

// simpleModeDefaultGroupDescription 标识由简易模式自动创建的默认分组。
const simpleModeDefaultGroupDescription = "Auto-created default group"

func ensureSimpleModeDefaultGroups(ctx context.Context, client *dbent.Client) error {
	if client == nil {
		return fmt.Errorf("nil ent client")
	}

	if err := backfillSimpleModeGrokDefaultImageGeneration(ctx, client); err != nil {
		return err
	}

	requiredByPlatform := map[string]int{
		service.PlatformAnthropic:   1,
		service.PlatformOpenAI:      1,
		service.PlatformGemini:      1,
		service.PlatformAntigravity: 2,
		service.PlatformGrok:        1,
	}

	for platform, minCount := range requiredByPlatform {
		count, err := client.Group.Query().
			Where(group.PlatformEQ(platform), group.DeletedAtIsNil()).
			Count(ctx)
		if err != nil {
			return fmt.Errorf("count groups for platform %s: %w", platform, err)
		}

		if platform == service.PlatformAntigravity {
			if count < minCount {
				for i := count; i < minCount; i++ {
					name := fmt.Sprintf("%s-default-%d", platform, i+1)
					if err := createGroupIfNotExists(ctx, client, name, platform); err != nil {
						return err
					}
				}
			}
			continue
		}

		// 非 Antigravity 平台确保存在 <platform>-default 分组。
		name := platform + "-default"
		if err := createGroupIfNotExists(ctx, client, name, platform); err != nil {
			return err
		}
	}

	return nil
}

func createGroupIfNotExists(ctx context.Context, client *dbent.Client, name, platform string) error {
	exists, err := client.Group.Query().
		Where(group.NameEQ(name), group.DeletedAtIsNil()).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("check group exists %s: %w", name, err)
	}
	if exists {
		return nil
	}

	_, err = client.Group.Create().
		SetName(name).
		SetDescription(simpleModeDefaultGroupDescription).
		SetPlatform(platform).
		SetStatus(service.StatusActive).
		SetRateMultiplier(1.0).
		SetIsExclusive(false).
		SetAllowImageGeneration(platform == service.PlatformGrok).
		SetAllowedClientProtocols(domain.DefaultGroupClientProtocols(platform)).
		Save(ctx)
	if err != nil {
		if dbent.IsConstraintError(err) {
			// 多实例并发启动可能竞争创建同名分组，此时按成功处理。
			return nil
		}
		return fmt.Errorf("create default group %s: %w", name, err)
	}
	return nil
}

// backfillSimpleModeGrokDefaultImageGeneration 只修复仍带自动创建标记的活跃 Grok 默认分组。
func backfillSimpleModeGrokDefaultImageGeneration(ctx context.Context, client *dbent.Client) error {
	_, err := client.Group.Update().
		Where(
			group.NameEQ(service.PlatformGrok+"-default"),
			group.PlatformEQ(service.PlatformGrok),
			group.DescriptionEQ(simpleModeDefaultGroupDescription),
			group.StatusEQ(service.StatusActive),
			group.AllowImageGenerationEQ(false),
			group.DeletedAtIsNil(),
		).
		SetAllowImageGeneration(true).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("backfill auto-created grok default image generation: %w", err)
	}
	return nil
}
