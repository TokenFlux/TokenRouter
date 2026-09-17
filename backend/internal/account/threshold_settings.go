package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// 阈值键不包含消费累计，普通更新不会写入账号资金快照。
const SettingKeyAccountSchedulingThresholds = "account_scheduling_thresholds"

// cachedAccountSchedulingThresholds 缓存各平台自动停调阈值。
type cachedAccountSchedulingThresholds struct {
	thresholds map[string]int
	expiresAt  int64 // Unix 纳秒时间戳
}

const accountSchedulingThresholdsCacheTTL = 60 * time.Second
const accountSchedulingThresholdsErrorTTL = 5 * time.Second
const accountSchedulingThresholdsDBTimeout = 5 * time.Second

func DefaultAccountSchedulingThresholds() map[string]int {
	return map[string]int{
		PlatformOpenAI:    100,
		PlatformAnthropic: 100,
		PlatformGrok:      100,
	}
}

func ValidateAndNormalizeAccountSchedulingThresholds(input map[string]int) (map[string]int, error) {
	normalized := DefaultAccountSchedulingThresholds()
	for platform, value := range input {
		allowed := false
		for _, item := range AllowedSchedulingThresholdPlatforms {
			if item == platform {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, apperror.BadRequest("INVALID_ACCOUNT_SCHEDULING_THRESHOLDS", fmt.Sprintf("unknown platform %q", platform))
		}
		if value < 1 || value > 100 {
			return nil, apperror.BadRequest("INVALID_ACCOUNT_SCHEDULING_THRESHOLDS", "platform scheduling threshold must be between 1 and 100")
		}
		normalized[platform] = value
	}
	return normalized, nil
}

func ParseAccountSchedulingThresholdsSetting(raw string) (map[string]int, error) {
	thresholds := DefaultAccountSchedulingThresholds()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return thresholds, nil
	}
	parsed := map[string]int{}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return thresholds, err
	}
	for _, platform := range AllowedSchedulingThresholdPlatforms {
		if value, ok := parsed[platform]; ok {
			thresholds[platform] = BoundedIntOrDefault(value, 1, 100, 100)
		}
	}
	return thresholds, nil
}

func BoundedIntOrDefault(value, minValue, maxValue, defaultValue int) int {
	if value < minValue || value > maxValue {
		return defaultValue
	}
	return value
}

func CloneAccountSchedulingThresholds(input map[string]int) map[string]int {
	if len(input) == 0 {
		return DefaultAccountSchedulingThresholds()
	}
	cloned := make(map[string]int, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

// GetAccountSchedulingThresholds 保留正常与故障 TTL、singleflight 及拷贝语义。
func (s *RuntimeSettings) GetAccountSchedulingThresholds(ctx context.Context) map[string]int {
	if s == nil || s.settingRepo == nil {
		return DefaultAccountSchedulingThresholds()
	}
	if cached, ok := s.accountSchedulingThresholdsCache.Load().(*cachedAccountSchedulingThresholds); ok {
		if cached != nil && len(cached.thresholds) > 0 && time.Now().UnixNano() < cached.expiresAt {
			return CloneAccountSchedulingThresholds(cached.thresholds)
		}
	}

	result, err, _ := s.accountSchedulingThresholdsSF.Do(SettingKeyAccountSchedulingThresholds, func() (any, error) {
		if cached, ok := s.accountSchedulingThresholdsCache.Load().(*cachedAccountSchedulingThresholds); ok {
			if cached != nil && len(cached.thresholds) > 0 && time.Now().UnixNano() < cached.expiresAt {
				return CloneAccountSchedulingThresholds(cached.thresholds), nil
			}
		}

		thresholds := DefaultAccountSchedulingThresholds()
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountSchedulingThresholdsDBTimeout)
		defer cancel()

		raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyAccountSchedulingThresholds)
		if err != nil {
			if errors.Is(err, s.notFound) {
				// 未配置阈值属于稳定默认状态，按正常周期缓存，避免热点路径持续查询数据库。
				s.accountSchedulingThresholdsCache.Store(&cachedAccountSchedulingThresholds{
					thresholds: CloneAccountSchedulingThresholds(thresholds),
					expiresAt:  time.Now().Add(accountSchedulingThresholdsCacheTTL).UnixNano(),
				})
				return CloneAccountSchedulingThresholds(thresholds), nil
			}
			slog.Warn("failed to get account scheduling thresholds, falling back to defaults", "error", err)
			s.accountSchedulingThresholdsCache.Store(&cachedAccountSchedulingThresholds{
				thresholds: CloneAccountSchedulingThresholds(thresholds),
				expiresAt:  time.Now().Add(accountSchedulingThresholdsErrorTTL).UnixNano(),
			})
			return CloneAccountSchedulingThresholds(thresholds), nil
		}

		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			if parsed, err := ParseAccountSchedulingThresholdsSetting(trimmed); err != nil {
				slog.Warn("failed to parse account scheduling thresholds, falling back to defaults", "error", err)
			} else {
				thresholds = parsed
			}
		}

		s.accountSchedulingThresholdsCache.Store(&cachedAccountSchedulingThresholds{
			thresholds: CloneAccountSchedulingThresholds(thresholds),
			expiresAt:  time.Now().Add(accountSchedulingThresholdsCacheTTL).UnixNano(),
		})
		return CloneAccountSchedulingThresholds(thresholds), nil
	})
	if err != nil {
		return DefaultAccountSchedulingThresholds()
	}
	if thresholds, ok := result.(map[string]int); ok {
		return CloneAccountSchedulingThresholds(thresholds)
	}
	return DefaultAccountSchedulingThresholds()
}

// ApplySchedulingThresholds 仅在设置提交后发布；省略时保持原清缓存行为。
func (s *RuntimeSettings) ApplySchedulingThresholds(value map[string]int) {
	s.accountSchedulingThresholdsSF.Forget(SettingKeyAccountSchedulingThresholds)
	if value != nil {
		normalizedThresholds, err := ValidateAndNormalizeAccountSchedulingThresholds(value)
		if err != nil {
			normalizedThresholds = DefaultAccountSchedulingThresholds()
		}
		s.accountSchedulingThresholdsCache.Store(&cachedAccountSchedulingThresholds{
			thresholds: CloneAccountSchedulingThresholds(normalizedThresholds),
			expiresAt:  time.Now().Add(accountSchedulingThresholdsCacheTTL).UnixNano(),
		})
	} else {
		// 请求体部分更新或省略该字段时清除缓存，使下次热点读取从数据库重新加载。
		s.accountSchedulingThresholdsCache.Store(&cachedAccountSchedulingThresholds{})
	}
}
