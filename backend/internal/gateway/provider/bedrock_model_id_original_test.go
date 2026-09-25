package provider

import (
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveBedrockModelID(t *testing.T) {
	t.Run("default alias resolves and adjusts region", func(t *testing.T) {
		account := &accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "eu-west-1",
			}}

		modelID, ok := (ModelPolicy{Record: account}).Bedrock("claude-sonnet-4-5")
		require.True(t, ok)
		assert.Equal(t, "eu.anthropic.claude-sonnet-4-5-20250929-v1:0", modelID)
	})

	t.Run("custom alias mapping reuses default bedrock mapping", func(t *testing.T) {
		account := &accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "ap-southeast-2",
				"model_mapping": map[string]any{
					"claude-*": "claude-opus-4-6",
				},
			}}

		modelID, ok := (ModelPolicy{Record: account}).Bedrock("claude-opus-4-6-thinking")
		require.True(t, ok)
		assert.Equal(t, "au.anthropic.claude-opus-4-6-v1", modelID)
	})

	t.Run("default opus 4.8 mapping uses regional Bedrock model id", func(t *testing.T) {
		account := &accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "eu-west-1",
			}}

		modelID, ok := (ModelPolicy{Record: account}).Bedrock("claude-opus-4-8")
		require.True(t, ok)
		assert.Equal(t, "eu.anthropic.claude-opus-4-8", modelID)
	})

	t.Run("默认 Fable 5 映射使用官方 Bedrock 模型 ID", func(t *testing.T) {
		account := &accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeBedrock,
			Credentials: map[string]any{
				"aws_region":       "eu-west-1",
				"aws_force_global": "true",
			}}

		modelID, ok := (ModelPolicy{Record: account}).Bedrock("claude-fable-5")
		require.True(t, ok)
		assert.Equal(t, "global.anthropic.claude-fable-5", modelID)
	})

	t.Run("默认 Fable 5.1 映射使用官方 Bedrock 模型 ID", func(t *testing.T) {
		account := &accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeBedrock,
			Credentials: map[string]any{
				"aws_region":       "eu-west-1",
				"aws_force_global": "true",
			}}

		modelID, ok := (ModelPolicy{Record: account}).Bedrock("claude-fable-5-1")
		require.True(t, ok)
		assert.Equal(t, "global.anthropic.claude-fable-5-1", modelID)
	})

	t.Run("force global rewrites anthropic regional model id", func(t *testing.T) {
		account := &accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeBedrock,
			Credentials: map[string]any{
				"aws_region":       "us-east-1",
				"aws_force_global": "true",
				"model_mapping": map[string]any{
					"claude-sonnet-4-6": "us.anthropic.claude-sonnet-4-6",
				},
			}}

		modelID, ok := (ModelPolicy{Record: account}).Bedrock("claude-sonnet-4-6")
		require.True(t, ok)
		assert.Equal(t, "global.anthropic.claude-sonnet-4-6", modelID)
	})

	t.Run("已登记基础模型使用当前区域推理 ID", func(t *testing.T) {
		account := &accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "us-east-1",
			}}

		modelID, ok := (ModelPolicy{Record: account}).Bedrock("anthropic.claude-haiku-4-5-20251001-v1:0")
		require.True(t, ok)
		assert.Equal(t, "us.anthropic.claude-haiku-4-5-20251001-v1:0", modelID)
	})

	t.Run("unsupported alias returns false", func(t *testing.T) {
		account := &accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "us-east-1",
			}}

		_, ok := (ModelPolicy{Record: account}).Bedrock("claude-3-5-sonnet-20241022")
		assert.False(t, ok)
	})
}
