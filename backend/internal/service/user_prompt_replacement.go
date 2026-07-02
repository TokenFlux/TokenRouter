package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	// UserPromptReplacementTypeStatic 表示替换成管理员填写的固定文本。
	UserPromptReplacementTypeStatic = "static"
	// UserPromptReplacementTypeTimezoneName 表示替换成规则配置的 IANA 时区名。
	UserPromptReplacementTypeTimezoneName = "timezone_name"
	// UserPromptReplacementTypeCurrentTime 表示替换成目标时区下的当前时间。
	UserPromptReplacementTypeCurrentTime = "current_time"
	// UserPromptReplacementScopeEnvironmentContext 表示仅在单个 environment_context 块内执行规则。
	UserPromptReplacementScopeEnvironmentContext = "environment_context"

	defaultUserPromptReplacementTimezone   = "Asia/Tokyo"
	defaultUserPromptReplacementTimeLayout = "2006-01-02"
	defaultUserPromptReplacementCacheTTL   = 60 * time.Second
	defaultUserPromptReplacementErrorTTL   = 5 * time.Second
)

// UserPromptReplacementConfig 是管理员配置的用户提示词替换规则集合。
type UserPromptReplacementConfig struct {
	Enabled bool                        `json:"enabled"`
	Rules   []UserPromptReplacementRule `json:"rules"`
}

// UserPromptReplacementRule 是单条用户文本替换规则。
type UserPromptReplacementRule struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	Pattern         string `json:"pattern"`
	TargetGroup     int    `json:"target_group"`
	ReplacementType string `json:"replacement_type"`
	Scope           string `json:"scope,omitempty"`
	StaticText      string `json:"static_text,omitempty"`
	Timezone        string `json:"timezone,omitempty"`
	TimeFormat      string `json:"time_format,omitempty"`
}

type compiledUserPromptReplacementRule struct {
	rule    UserPromptReplacementRule
	pattern *regexp.Regexp
	loc     *time.Location
}

type compiledUserPromptReplacementConfig struct {
	enabled   bool
	rules     []compiledUserPromptReplacementRule
	expiresAt int64
}

var userPromptReplacementCache atomic.Value // *compiledUserPromptReplacementConfig

// DefaultUserPromptReplacementConfig 返回用户提示词替换的内置默认规则。
func DefaultUserPromptReplacementConfig() *UserPromptReplacementConfig {
	return &UserPromptReplacementConfig{
		Enabled: true,
		Rules: []UserPromptReplacementRule{
			{
				ID:              "environment-context-timezone-japan",
				Name:            "environment_context timezone -> Asia/Tokyo",
				Enabled:         true,
				Pattern:         `(?s)(<environment_context\b[^>]*>.*?<timezone>)([^<]*)(</timezone>.*?</environment_context>)`,
				TargetGroup:     2,
				ReplacementType: UserPromptReplacementTypeTimezoneName,
				Scope:           UserPromptReplacementScopeEnvironmentContext,
				Timezone:        defaultUserPromptReplacementTimezone,
			},
			{
				ID:              "environment-context-current-date-japan",
				Name:            "environment_context current_date -> Asia/Tokyo today",
				Enabled:         true,
				Pattern:         `(?s)(<environment_context\b[^>]*>.*?<current_date>)([^<]*)(</current_date>.*?</environment_context>)`,
				TargetGroup:     2,
				ReplacementType: UserPromptReplacementTypeCurrentTime,
				Scope:           UserPromptReplacementScopeEnvironmentContext,
				Timezone:        defaultUserPromptReplacementTimezone,
				TimeFormat:      defaultUserPromptReplacementTimeLayout,
			},
		},
	}
}

func defaultUserPromptReplacementConfigJSON() string {
	raw, err := json.Marshal(DefaultUserPromptReplacementConfig())
	if err != nil {
		return `{"enabled":true,"rules":[]}`
	}
	return string(raw)
}

func parseUserPromptReplacementConfig(raw string) *UserPromptReplacementConfig {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultUserPromptReplacementConfig()
	}
	var cfg UserPromptReplacementConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		slog.Warn("user_prompt_replacement_config_parse_failed", "error", err)
		return DefaultUserPromptReplacementConfig()
	}
	if cfg.Rules == nil {
		cfg.Rules = []UserPromptReplacementRule{}
	}
	return &cfg
}

func normalizeUserPromptReplacementConfig(cfg *UserPromptReplacementConfig) (*UserPromptReplacementConfig, error) {
	if cfg == nil {
		return DefaultUserPromptReplacementConfig(), nil
	}
	normalized := &UserPromptReplacementConfig{
		Enabled: cfg.Enabled,
		Rules:   make([]UserPromptReplacementRule, 0, len(cfg.Rules)),
	}
	for idx, rule := range cfg.Rules {
		rule.ID = strings.TrimSpace(rule.ID)
		rule.Name = strings.TrimSpace(rule.Name)
		rule.Pattern = strings.TrimSpace(rule.Pattern)
		rule.ReplacementType = strings.TrimSpace(rule.ReplacementType)
		rule.Scope = strings.TrimSpace(rule.Scope)
		rule.Timezone = strings.TrimSpace(rule.Timezone)
		rule.TimeFormat = strings.TrimSpace(rule.TimeFormat)
		if rule.Scope == "" && isDefaultEnvironmentContextUserPromptReplacementRuleID(rule.ID) {
			// 兼容已保存的默认规则：旧配置没有 scope 字段，但仍应只在单个 environment_context 块内生效。
			rule.Scope = UserPromptReplacementScopeEnvironmentContext
		}
		if rule.ID == "" {
			rule.ID = fmt.Sprintf("rule-%d", idx+1)
		}
		if rule.Name == "" {
			rule.Name = rule.ID
		}
		if rule.Pattern == "" {
			return nil, infraerrors.BadRequest("INVALID_USER_PROMPT_REPLACEMENT_RULE", "replacement pattern is required")
		}
		compiled, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, infraerrors.BadRequest("INVALID_USER_PROMPT_REPLACEMENT_RULE", "replacement pattern is invalid: "+err.Error())
		}
		if rule.TargetGroup < 0 || rule.TargetGroup > compiled.NumSubexp() {
			return nil, infraerrors.BadRequest("INVALID_USER_PROMPT_REPLACEMENT_RULE", "replacement target_group is out of range")
		}
		switch rule.Scope {
		case "", UserPromptReplacementScopeEnvironmentContext:
		default:
			return nil, infraerrors.BadRequest("INVALID_USER_PROMPT_REPLACEMENT_RULE", "replacement scope is invalid")
		}
		switch rule.ReplacementType {
		case UserPromptReplacementTypeStatic:
		case UserPromptReplacementTypeTimezoneName, UserPromptReplacementTypeCurrentTime:
			if rule.Timezone == "" {
				rule.Timezone = defaultUserPromptReplacementTimezone
			}
			if _, err := time.LoadLocation(rule.Timezone); err != nil {
				return nil, infraerrors.BadRequest("INVALID_USER_PROMPT_REPLACEMENT_RULE", "replacement timezone is invalid")
			}
			if rule.ReplacementType == UserPromptReplacementTypeCurrentTime && rule.TimeFormat == "" {
				rule.TimeFormat = defaultUserPromptReplacementTimeLayout
			}
		default:
			return nil, infraerrors.BadRequest("INVALID_USER_PROMPT_REPLACEMENT_RULE", "replacement_type is invalid")
		}
		normalized.Rules = append(normalized.Rules, rule)
	}
	return normalized, nil
}

func isDefaultEnvironmentContextUserPromptReplacementRuleID(id string) bool {
	switch id {
	case "environment-context-timezone-japan", "environment-context-current-date-japan":
		return true
	default:
		return false
	}
}

func compileUserPromptReplacementConfig(cfg *UserPromptReplacementConfig, ttl time.Duration) (*compiledUserPromptReplacementConfig, error) {
	normalized, err := normalizeUserPromptReplacementConfig(cfg)
	if err != nil {
		return nil, err
	}
	compiled := &compiledUserPromptReplacementConfig{
		enabled:   normalized.Enabled,
		rules:     make([]compiledUserPromptReplacementRule, 0, len(normalized.Rules)),
		expiresAt: time.Now().Add(ttl).UnixNano(),
	}
	for _, rule := range normalized.Rules {
		if !rule.Enabled {
			continue
		}
		pattern, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, err
		}
		var loc *time.Location
		if rule.ReplacementType == UserPromptReplacementTypeTimezoneName || rule.ReplacementType == UserPromptReplacementTypeCurrentTime {
			loc, err = time.LoadLocation(rule.Timezone)
			if err != nil {
				return nil, err
			}
		}
		compiled.rules = append(compiled.rules, compiledUserPromptReplacementRule{
			rule:    rule,
			pattern: pattern,
			loc:     loc,
		})
	}
	return compiled, nil
}

// GetUserPromptReplacementConfig 返回当前用户提示词替换配置。
func (s *SettingService) GetUserPromptReplacementConfig(ctx context.Context) (*UserPromptReplacementConfig, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultUserPromptReplacementConfig(), nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyUserPromptReplacementConfig)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultUserPromptReplacementConfig(), nil
		}
		return nil, fmt.Errorf("get user prompt replacement config: %w", err)
	}
	return parseUserPromptReplacementConfig(raw), nil
}

// SetUserPromptReplacementConfig 保存用户提示词替换配置。
func (s *SettingService) SetUserPromptReplacementConfig(ctx context.Context, cfg *UserPromptReplacementConfig) error {
	normalized, err := normalizeUserPromptReplacementConfig(cfg)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal user prompt replacement config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyUserPromptReplacementConfig, string(raw)); err != nil {
		return fmt.Errorf("set user prompt replacement config: %w", err)
	}
	userPromptReplacementCache.Store((*compiledUserPromptReplacementConfig)(nil))
	return nil
}

func (s *SettingService) getCompiledUserPromptReplacementConfig(ctx context.Context) *compiledUserPromptReplacementConfig {
	if cached, ok := userPromptReplacementCache.Load().(*compiledUserPromptReplacementConfig); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached
		}
	}
	cfg, err := s.GetUserPromptReplacementConfig(ctx)
	if err != nil {
		slog.Warn("user_prompt_replacement_config_load_failed", "error", err)
		entry := &compiledUserPromptReplacementConfig{enabled: false, expiresAt: time.Now().Add(defaultUserPromptReplacementErrorTTL).UnixNano()}
		userPromptReplacementCache.Store(entry)
		return entry
	}
	compiled, err := compileUserPromptReplacementConfig(cfg, defaultUserPromptReplacementCacheTTL)
	if err != nil {
		slog.Warn("user_prompt_replacement_config_compile_failed", "error", err)
		entry := &compiledUserPromptReplacementConfig{enabled: false, expiresAt: time.Now().Add(defaultUserPromptReplacementErrorTTL).UnixNano()}
		userPromptReplacementCache.Store(entry)
		return entry
	}
	userPromptReplacementCache.Store(compiled)
	return compiled
}

func replacementValueForRule(rule compiledUserPromptReplacementRule, now time.Time) string {
	switch rule.rule.ReplacementType {
	case UserPromptReplacementTypeTimezoneName:
		return rule.rule.Timezone
	case UserPromptReplacementTypeCurrentTime:
		layout := rule.rule.TimeFormat
		if layout == "" {
			layout = defaultUserPromptReplacementTimeLayout
		}
		loc := rule.loc
		if loc == nil {
			loc = time.Local
		}
		return now.In(loc).Format(layout)
	default:
		return rule.rule.StaticText
	}
}

func applyCompiledUserPromptReplacementRules(text string, rules []compiledUserPromptReplacementRule, now time.Time) string {
	for _, rule := range rules {
		if rule.rule.Scope == UserPromptReplacementScopeEnvironmentContext {
			text = applyEnvironmentContextScopedUserPromptReplacementRule(text, rule, now)
			continue
		}
		replacement := replacementValueForRule(rule, now)
		text = rule.pattern.ReplaceAllStringFunc(text, func(match string) string {
			ranges := rule.pattern.FindStringSubmatchIndex(match)
			group := rule.rule.TargetGroup
			if len(ranges) < group*2+2 {
				return match
			}
			start := ranges[group*2]
			end := ranges[group*2+1]
			if start < 0 || end < start {
				return match
			}
			return match[:start] + replacement + match[end:]
		})
	}
	return text
}

func applyEnvironmentContextScopedUserPromptReplacementRule(text string, rule compiledUserPromptReplacementRule, now time.Time) string {
	var builder strings.Builder
	cursor := 0
	for {
		startRel := strings.Index(text[cursor:], "<environment_context")
		if startRel < 0 {
			break
		}
		start := cursor + startRel
		closeStartRel := strings.Index(text[start:], "</environment_context>")
		if closeStartRel < 0 {
			break
		}
		end := start + closeStartRel + len("</environment_context>")
		if builder.Len() == 0 {
			builder.Grow(len(text))
		}
		_, _ = builder.WriteString(text[cursor:start])
		// 默认规则需要限制在单个 environment_context 块内，避免正则中的 .*? 横跨多个块误替换。
		_, _ = builder.WriteString(applySingleUserPromptReplacementRule(text[start:end], rule, now))
		cursor = end
	}
	if builder.Len() == 0 {
		return text
	}
	_, _ = builder.WriteString(text[cursor:])
	return builder.String()
}

func applySingleUserPromptReplacementRule(text string, rule compiledUserPromptReplacementRule, now time.Time) string {
	replacement := replacementValueForRule(rule, now)
	return rule.pattern.ReplaceAllStringFunc(text, func(match string) string {
		ranges := rule.pattern.FindStringSubmatchIndex(match)
		group := rule.rule.TargetGroup
		if len(ranges) < group*2+2 {
			return match
		}
		start := ranges[group*2]
		end := ranges[group*2+1]
		if start < 0 || end < start {
			return match
		}
		return match[:start] + replacement + match[end:]
	})
}

// ApplyUserPromptReplacementToBody 将用户提示词替换规则应用到指定协议的用户文本字段。
func (s *SettingService) ApplyUserPromptReplacementToBody(ctx context.Context, body []byte, protocol string) []byte {
	if s == nil || len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	compiled := s.getCompiledUserPromptReplacementConfig(ctx)
	if compiled == nil || !compiled.enabled || len(compiled.rules) == 0 {
		return body
	}
	now := time.Now()
	switch protocol {
	case "openai_responses":
		return applyUserPromptReplacementOpenAIResponses(body, compiled.rules, now)
	case "gemini":
		return applyUserPromptReplacementGemini(body, compiled.rules, now)
	default:
		return applyUserPromptReplacementMessages(body, compiled.rules, now)
	}
}

// ApplyUserPromptReplacement 将用户提示词替换规则应用到通用网关请求体。
func (s *GatewayService) ApplyUserPromptReplacement(ctx context.Context, body []byte, protocol string) []byte {
	if s == nil || s.settingService == nil {
		return body
	}
	return s.settingService.ApplyUserPromptReplacementToBody(ctx, body, protocol)
}

// ApplyUserPromptReplacement 将用户提示词替换规则应用到 OpenAI 网关请求体。
func (s *OpenAIGatewayService) ApplyUserPromptReplacement(ctx context.Context, body []byte, protocol string) []byte {
	if s == nil || s.settingService == nil {
		return body
	}
	return s.settingService.ApplyUserPromptReplacementToBody(ctx, body, protocol)
}

func applyUserPromptReplacementMessages(body []byte, rules []compiledUserPromptReplacementRule, now time.Time) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body
	}
	updated := body
	messages.ForEach(func(msgKey, msg gjson.Result) bool {
		if msg.Get("role").String() != "user" {
			return true
		}
		base := "messages." + msgKey.String() + ".content"
		content := msg.Get("content")
		updated = replaceTextResultAtPath(updated, base, content, rules, now)
		if content.IsArray() {
			content.ForEach(func(blockKey, block gjson.Result) bool {
				if block.Get("type").String() == "text" {
					path := base + "." + blockKey.String() + ".text"
					updated = replaceTextResultAtPath(updated, path, block.Get("text"), rules, now)
				}
				return true
			})
		}
		return true
	})
	return updated
}

func applyUserPromptReplacementOpenAIResponses(body []byte, rules []compiledUserPromptReplacementRule, now time.Time) []byte {
	input := gjson.GetBytes(body, "input")
	updated := body
	if input.Type == gjson.String {
		return replaceTextResultAtPath(updated, "input", input, rules, now)
	}
	if !input.IsArray() {
		return body
	}
	input.ForEach(func(itemKey, item gjson.Result) bool {
		role := item.Get("role").String()
		if role != "" && role != "user" {
			return true
		}
		base := "input." + itemKey.String()
		if text := item.Get("text"); text.Type == gjson.String {
			updated = replaceTextResultAtPath(updated, base+".text", text, rules, now)
		}
		content := item.Get("content")
		if content.Type == gjson.String {
			updated = replaceTextResultAtPath(updated, base+".content", content, rules, now)
		}
		if content.IsArray() {
			content.ForEach(func(blockKey, block gjson.Result) bool {
				blockType := block.Get("type").String()
				if blockType == "input_text" || blockType == "text" || blockType == "" {
					path := base + ".content." + blockKey.String() + ".text"
					updated = replaceTextResultAtPath(updated, path, block.Get("text"), rules, now)
				}
				return true
			})
		}
		return true
	})
	return updated
}

func applyUserPromptReplacementGemini(body []byte, rules []compiledUserPromptReplacementRule, now time.Time) []byte {
	contents := gjson.GetBytes(body, "contents")
	if !contents.IsArray() {
		return body
	}
	updated := body
	contents.ForEach(func(contentKey, content gjson.Result) bool {
		role := content.Get("role").String()
		if role != "" && role != "user" {
			return true
		}
		parts := content.Get("parts")
		if !parts.IsArray() {
			return true
		}
		parts.ForEach(func(partKey, part gjson.Result) bool {
			path := "contents." + contentKey.String() + ".parts." + partKey.String() + ".text"
			updated = replaceTextResultAtPath(updated, path, part.Get("text"), rules, now)
			return true
		})
		return true
	})
	return updated
}

func replaceTextResultAtPath(body []byte, path string, result gjson.Result, rules []compiledUserPromptReplacementRule, now time.Time) []byte {
	if result.Type != gjson.String {
		return body
	}
	original := result.String()
	replaced := applyCompiledUserPromptReplacementRules(original, rules, now)
	if replaced == original {
		return body
	}
	next, err := sjson.SetBytesOptions(body, path, replaced, &sjson.Options{Optimistic: true})
	if err != nil {
		slog.Warn("user_prompt_replacement_set_path_failed", "path", path, "error", err)
		return body
	}
	return next
}

// UserPromptReplacementRuleTargetGroupOptions 返回前端可用的 capture group 选项。
func UserPromptReplacementRuleTargetGroupOptions(pattern string) []int {
	compiled, err := regexp.Compile(strings.TrimSpace(pattern))
	if err != nil {
		return []int{0}
	}
	options := make([]int, 0, compiled.NumSubexp()+1)
	for i := 0; i <= compiled.NumSubexp(); i++ {
		options = append(options, i)
	}
	return options
}

func userPromptReplacementConfigToRaw(cfg *UserPromptReplacementConfig) (string, error) {
	normalized, err := normalizeUserPromptReplacementConfig(cfg)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal user prompt replacement config: %w", err)
	}
	return string(raw), nil
}
