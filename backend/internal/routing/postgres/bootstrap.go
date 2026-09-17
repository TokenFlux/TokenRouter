package postgres

import (
	"context"
	"fmt"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/group"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// simpleModeDefaultGroupDescription 标识由简易模式自动创建的默认分组。
const simpleModeDefaultGroupDescription = "Auto-created default group"

func EnsureSimpleModeDefaultGroups(ctx context.Context, client *dbent.Client) error {
	if client == nil {
		return fmt.Errorf("nil ent client")
	}

	if err := backfillSimpleModeGrokDefaultImageGeneration(ctx, client); err != nil {
		return err
	}

	requiredByPlatform := map[string]int{
		capability.PlatformAnthropic:   1,
		capability.PlatformOpenAI:      1,
		capability.PlatformGemini:      1,
		capability.PlatformAntigravity: 2,
		capability.PlatformGrok:        1,
	}

	for platform, minCount := range requiredByPlatform {
		count, err := client.Group.Query().
			Where(group.PlatformEQ(platform), group.DeletedAtIsNil()).
			Count(ctx)
		if err != nil {
			return fmt.Errorf("count groups for platform %s: %w", platform, err)
		}

		if platform == capability.PlatformAntigravity {
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
		SetStatus("active").
		SetRateMultiplier(1.0).
		SetIsExclusive(false).
		SetAllowImageGeneration(platform == capability.PlatformGrok).
		SetAllowedProtocols(capability.DefaultGroupClientProtocols(platform)).
		SetProtocolFallbacks(capability.DefaultProtocolFallbacks(platform)).
		SetResponsesImagePolicy("inherit").
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
			group.NameEQ(capability.PlatformGrok+"-default"),
			group.PlatformEQ(capability.PlatformGrok),
			group.DescriptionEQ(simpleModeDefaultGroupDescription),
			group.StatusEQ("active"),
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
