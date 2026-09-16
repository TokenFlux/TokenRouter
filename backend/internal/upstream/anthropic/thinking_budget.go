// 预算修复保持原常量、字段顺序与 adaptive 例外。
package anthropic

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	// BudgetRectifyBudgetTokens is the budget_tokens value to set when rectifying.
	BudgetRectifyBudgetTokens = 32000
	// BudgetRectifyMaxTokens is the max_tokens value to set when rectifying.
	BudgetRectifyMaxTokens = 64000
	// BudgetRectifyMinMaxTokens is the minimum max_tokens that must exceed budget_tokens.
	BudgetRectifyMinMaxTokens = 32001
)

// IsThinkingBudgetConstraintError detects whether an upstream error message indicates
// a budget_tokens constraint violation (e.g. "budget_tokens >= 1024").
// Matches three conditions (all must be true):
//  1. Contains "budget_tokens" or "budget tokens"
//  2. Contains "thinking"
//  3. Contains ">= 1024" or "greater than or equal to 1024" or ("1024" + "input should be")
func IsThinkingBudgetConstraintError(errMsg string) bool {
	m := strings.ToLower(errMsg)

	// Condition 1: budget_tokens or budget tokens
	hasBudget := strings.Contains(m, "budget_tokens") || strings.Contains(m, "budget tokens")
	if !hasBudget {
		return false
	}

	// Condition 2: thinking
	if !strings.Contains(m, "thinking") {
		return false
	}

	// Condition 3: constraint indicator
	if strings.Contains(m, ">= 1024") || strings.Contains(m, "greater than or equal to 1024") {
		return true
	}
	if strings.Contains(m, "1024") && strings.Contains(m, "input should be") {
		return true
	}

	return false
}

// RectifyThinkingBudget modifies the request body to fix budget_tokens constraint errors.
// It sets thinking.budget_tokens = 32000, thinking.type = "enabled" (unless adaptive),
// and ensures max_tokens >= 32001.
// Returns (modified body, true) if changes were applied, or (original body, false) if not.
func RectifyThinkingBudget(body []byte) ([]byte, bool) {
	// If thinking type is "adaptive", skip rectification entirely
	thinkingType := gjson.GetBytes(body, "thinking.type").String()
	if thinkingType == "adaptive" {
		return body, false
	}

	modified := body
	changed := false

	// Set thinking.type = "enabled"
	if thinkingType != "enabled" {
		if result, err := sjson.SetBytes(modified, "thinking.type", "enabled"); err == nil {
			modified = result
			changed = true
		}
	}

	// Set thinking.budget_tokens = 32000
	currentBudget := gjson.GetBytes(modified, "thinking.budget_tokens").Int()
	if currentBudget != BudgetRectifyBudgetTokens {
		if result, err := sjson.SetBytes(modified, "thinking.budget_tokens", BudgetRectifyBudgetTokens); err == nil {
			modified = result
			changed = true
		}
	}

	// Ensure max_tokens >= BudgetRectifyMinMaxTokens
	maxTokens := gjson.GetBytes(modified, "max_tokens").Int()
	if maxTokens < int64(BudgetRectifyMinMaxTokens) {
		if result, err := sjson.SetBytes(modified, "max_tokens", BudgetRectifyMaxTokens); err == nil {
			modified = result
			changed = true
		}
	}

	return modified, changed
}
