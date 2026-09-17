package gateway

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

// ClientSettingsOptions 只接收平台原有纯校验与动态缺省读取，不安装第二份缓存。
type ClientSettingsOptions struct {
	NormalizeUserAgentVersion func(string) string
	DefaultUserAgentVersion   func() string
}
type cachedAntigravityUserAgentVersion struct {
	version   string
	expiresAt int64 // unix nano
}

type cachedOpenAICodexUserAgent struct {
	value     string
	expiresAt int64 // unix nano
}

const antigravityUserAgentVersionCacheTTL = 60 * time.Second

const antigravityUserAgentVersionErrorTTL = 5 * time.Second

const antigravityUserAgentVersionDBTimeout = 5 * time.Second

const openAICodexUserAgentCacheTTL = 60 * time.Second

const openAICodexUserAgentErrorTTL = 5 * time.Second

const openAICodexUserAgentDBTimeout = 5 * time.Second

const DefaultOpenAICodexUserAgent = "codex-tui/0.144.1 (Ubuntu 22.4.0; x86_64) xterm-256color (codex-tui; 0.144.1)"

const openAIAllowCodexPluginCacheTTL = 60 * time.Second

const openAIAllowCodexPluginErrorTTL = 5 * time.Second

const openAIAllowCodexPluginDBTimeout = 5 * time.Second

type cachedOpenAIAllowCodexPlugin struct {
	value     bool
	expiresAt int64 // unix nano
}

func (s *RuntimeSettings) GetAntigravityUserAgentVersion(ctx context.Context) string {
	fallback := s.clientOptions.DefaultUserAgentVersion()
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	if cached, ok := s.antigravityUAVersionCache.Load().(*cachedAntigravityUserAgentVersion); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.version
		}
	}

	result, _, _ := s.antigravityUAVersionSF.Do("antigravity_user_agent_version", func() (any, error) {
		if cached, ok := s.antigravityUAVersionCache.Load().(*cachedAntigravityUserAgentVersion); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.version, nil
			}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), antigravityUserAgentVersionDBTimeout)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyAntigravityUserAgentVersion)
		if err != nil && !errors.Is(err, s.notFound) {
			slog.Warn("failed to get antigravity user agent version setting", "error", err)
			s.antigravityUAVersionCache.Store(&cachedAntigravityUserAgentVersion{
				version:   fallback,
				expiresAt: time.Now().Add(antigravityUserAgentVersionErrorTTL).UnixNano(),
			})
			return fallback, nil
		}
		version := s.clientOptions.NormalizeUserAgentVersion(value)
		if version == "" {
			version = fallback
		}
		s.antigravityUAVersionCache.Store(&cachedAntigravityUserAgentVersion{
			version:   version,
			expiresAt: time.Now().Add(antigravityUserAgentVersionCacheTTL).UnixNano(),
		})
		return version, nil
	})
	if version, ok := result.(string); ok && version != "" {
		return version
	}
	return fallback
}

func (s *RuntimeSettings) GetOpenAICodexUserAgent(ctx context.Context) string {
	fallback := DefaultOpenAICodexUserAgent
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	if cached, ok := s.openAICodexUACache.Load().(*cachedOpenAICodexUserAgent); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.value
		}
	}

	result, _, _ := s.openAICodexUASF.Do("openai_codex_user_agent", func() (any, error) {
		if cached, ok := s.openAICodexUACache.Load().(*cachedOpenAICodexUserAgent); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.value, nil
			}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAICodexUserAgentDBTimeout)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAICodexUserAgent)
		if err != nil && !errors.Is(err, s.notFound) {
			slog.Warn("failed to get openai codex user agent setting", "error", err)
			s.openAICodexUACache.Store(&cachedOpenAICodexUserAgent{
				value:     fallback,
				expiresAt: time.Now().Add(openAICodexUserAgentErrorTTL).UnixNano(),
			})
			return fallback, nil
		}
		ua := strings.TrimSpace(value)
		if ua == "" {
			ua = fallback
		}
		s.openAICodexUACache.Store(&cachedOpenAICodexUserAgent{
			value:     ua,
			expiresAt: time.Now().Add(openAICodexUserAgentCacheTTL).UnixNano(),
		})
		return ua, nil
	})
	if ua, ok := result.(string); ok && ua != "" {
		return ua
	}
	return fallback
}

func (s *RuntimeSettings) IsOpenAIAllowClaudeCodeCodexPluginEnabled(ctx context.Context) bool {
	if cached, ok := s.openAIAllowCodexPluginCache.Load().(*cachedOpenAIAllowCodexPlugin); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.value
		}
	}
	result, _, _ := s.openAIAllowCodexPluginSF.Do("openai_allow_codex_plugin_enabled", func() (any, error) {
		if cached, ok := s.openAIAllowCodexPluginCache.Load().(*cachedOpenAIAllowCodexPlugin); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.value, nil
			}
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIAllowCodexPluginDBTimeout)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAIAllowClaudeCodeCodexPlugin)
		if err != nil {
			if errors.Is(err, s.notFound) {
				// 设置不存在 → 默认关闭，正常 TTL 缓存
				s.openAIAllowCodexPluginCache.Store(&cachedOpenAIAllowCodexPlugin{
					value:     false,
					expiresAt: time.Now().Add(openAIAllowCodexPluginCacheTTL).UnixNano(),
				})
				return false, nil
			}
			slog.Warn("failed to get openai_allow_claude_code_codex_plugin setting", "error", err)
			// DB 错误 → 安全默认关闭，短 TTL 快速重试
			s.openAIAllowCodexPluginCache.Store(&cachedOpenAIAllowCodexPlugin{
				value:     false,
				expiresAt: time.Now().Add(openAIAllowCodexPluginErrorTTL).UnixNano(),
			})
			return false, nil
		}
		enabled := value == "true"
		s.openAIAllowCodexPluginCache.Store(&cachedOpenAIAllowCodexPlugin{
			value:     enabled,
			expiresAt: time.Now().Add(openAIAllowCodexPluginCacheTTL).UnixNano(),
		})
		return enabled, nil
	})
	if val, ok := result.(bool); ok {
		return val
	}
	return false
}

// PublishClientUserAgents 保留综合设置的原发布顺序和 TTL。
func (s *RuntimeSettings) PublishClientUserAgents(version, codex string) {
	s.antigravityUAVersionSF.Forget("antigravity_user_agent_version")
	antigravityUserAgentVersion := s.clientOptions.NormalizeUserAgentVersion(version)
	if antigravityUserAgentVersion == "" {
		antigravityUserAgentVersion = s.clientOptions.DefaultUserAgentVersion()
	}
	s.antigravityUAVersionCache.Store(&cachedAntigravityUserAgentVersion{
		version:   antigravityUserAgentVersion,
		expiresAt: time.Now().Add(antigravityUserAgentVersionCacheTTL).UnixNano(),
	})
	s.openAICodexUASF.Forget("openai_codex_user_agent")
	codexUA := strings.TrimSpace(codex)
	if codexUA == "" {
		codexUA = DefaultOpenAICodexUserAgent
	}
	s.openAICodexUACache.Store(&cachedOpenAICodexUserAgent{
		value:     codexUA,
		expiresAt: time.Now().Add(openAICodexUserAgentCacheTTL).UnixNano(),
	})
}

// PublishCodexPlugin 在持久化后发布已合并开关。
func (s *RuntimeSettings) PublishCodexPlugin(enabled bool) {
	s.openAIAllowCodexPluginSF.Forget("openai_allow_codex_plugin_enabled")
	s.openAIAllowCodexPluginCache.Store(&cachedOpenAIAllowCodexPlugin{
		value:     enabled,
		expiresAt: time.Now().Add(openAIAllowCodexPluginCacheTTL).UnixNano(),
	})
}
