package messageforward

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

func TestResolveBedrockBetaTokensForRequest_BlocksOnOriginalAnthropicToken(t *testing.T) {
	settings := &anthropic.BetaPolicySettings{
		Rules: []anthropic.BetaPolicyRule{
			{
				BetaToken:    "advanced-tool-use-2025-11-20",
				Action:       anthropic.BetaPolicyActionBlock,
				Scope:        anthropic.BetaPolicyScopeAll,
				ErrorMessage: "advanced tool use is blocked",
			},
		},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(map[string]string{gateway.SettingKeyBetaPolicySettings: string(raw)})}, Options{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeBedrock}}

	_, err = svc.bedrockBetaTokens(
		context.Background(),
		account,
		"advanced-tool-use-2025-11-20",
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		"us.anthropic.claude-opus-4-6-v1",
	)
	if err == nil {
		t.Fatal("expected raw advanced-tool-use token to be blocked before Bedrock transform")
	}
	if err.Error() != "advanced tool use is blocked" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveBedrockBetaTokensForRequest_FiltersAfterBedrockTransform(t *testing.T) {
	settings := &anthropic.BetaPolicySettings{
		Rules: []anthropic.BetaPolicyRule{
			{
				BetaToken: "tool-search-tool-2025-10-19",
				Action:    anthropic.BetaPolicyActionFilter,
				Scope:     anthropic.BetaPolicyScopeAll,
			},
		},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(map[string]string{gateway.SettingKeyBetaPolicySettings: string(raw)})}, Options{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeBedrock}}

	betaTokens, err := svc.bedrockBetaTokens(
		context.Background(),
		account,
		"advanced-tool-use-2025-11-20",
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		"us.anthropic.claude-opus-4-6-v1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, token := range betaTokens {
		if token == "tool-search-tool-2025-10-19" {
			t.Fatalf("expected transformed Bedrock token to be filtered")
		}
	}
}

// TestResolveBedrockBetaTokensForRequest_BlocksBodyAutoInjectedComputerUse 验证：
// 管理员 block 了 computer-use，客户端不在 header 中带该 token，
// 但请求体包含 computer_use 工具 → 自动注入后应被 block。
func TestResolveBedrockBetaTokensForRequest_BlocksBodyAutoInjectedComputerUse(t *testing.T) {
	settings := &anthropic.BetaPolicySettings{
		Rules: []anthropic.BetaPolicyRule{
			{
				BetaToken:    "computer-use-2025-11-24",
				Action:       anthropic.BetaPolicyActionBlock,
				Scope:        anthropic.BetaPolicyScopeAll,
				ErrorMessage: "computer use is blocked",
			},
		},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(map[string]string{gateway.SettingKeyBetaPolicySettings: string(raw)})}, Options{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeBedrock}}

	// header 中不带 beta token，但 body 中有 computer_use 工具
	_, err = svc.bedrockBetaTokens(
		context.Background(),
		account,
		"", // 空 header
		[]byte(`{"tools":[{"type":"computer_20250124","name":"computer"}],"messages":[{"role":"user","content":"hi"}]}`),
		"us.anthropic.claude-opus-4-6-v1",
	)
	if err == nil {
		t.Fatal("expected body-injected computer-use to be blocked")
	}
	if err.Error() != "computer use is blocked" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestResolveBedrockBetaTokensForRequest_BlocksBodyAutoInjectedToolSearch 验证：
// 管理员 block 了 tool-search-tool，客户端不在 header 中带 beta token，
// 但请求体包含 tool search 工具 → 自动注入后应被 block。
func TestResolveBedrockBetaTokensForRequest_BlocksBodyAutoInjectedToolSearch(t *testing.T) {
	settings := &anthropic.BetaPolicySettings{
		Rules: []anthropic.BetaPolicyRule{
			{
				BetaToken:    "tool-search-tool-2025-10-19",
				Action:       anthropic.BetaPolicyActionBlock,
				Scope:        anthropic.BetaPolicyScopeAll,
				ErrorMessage: "tool search is blocked",
			},
		},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(map[string]string{gateway.SettingKeyBetaPolicySettings: string(raw)})}, Options{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeBedrock}}

	// header 中不带 beta token，但 body 中有 tool_search_tool 工具
	_, err = svc.bedrockBetaTokens(
		context.Background(),
		account,
		"",
		[]byte(`{"tools":[{"type":"tool_search_tool_regex_20251119","name":"search"}],"messages":[{"role":"user","content":"hi"}]}`),
		"us.anthropic.claude-sonnet-4-6",
	)
	if err == nil {
		t.Fatal("expected body-injected tool-search-tool to be blocked")
	}
	if err.Error() != "tool search is blocked" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestResolveBedrockBetaTokensForRequest_PassesWhenNoBlockRuleMatches 验证：
// body 自动注入的 token 如果没有对应的 block 规则，应正常通过。
func TestResolveBedrockBetaTokensForRequest_PassesWhenNoBlockRuleMatches(t *testing.T) {
	settings := &anthropic.BetaPolicySettings{
		Rules: []anthropic.BetaPolicyRule{
			{
				BetaToken:    "context-1m-2025-08-07",
				Action:       anthropic.BetaPolicyActionBlock,
				Scope:        anthropic.BetaPolicyScopeAll,
				ErrorMessage: "context is blocked",
			},
		},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(map[string]string{gateway.SettingKeyBetaPolicySettings: string(raw)})}, Options{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeBedrock}}

	// body 中有 computer_use 工具（会注入 computer-use token），但 block 规则只针对 context-1m
	tokens, err := svc.bedrockBetaTokens(
		context.Background(),
		account,
		"",
		[]byte(`{"tools":[{"type":"computer_20250124","name":"computer"}],"messages":[{"role":"user","content":"hi"}]}`),
		"us.anthropic.claude-opus-4-6-v1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, token := range tokens {
		if token == "computer-use-2025-11-24" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected computer-use token to be present")
	}
}
