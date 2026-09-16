package clientmeta

import (
	"fmt"
	"strings"
)

// IsHaikuProbe 保留客户端连通性探测的两个条件。
func IsHaikuProbe(model string, maxTokens int) bool {
	return maxTokens == 1 && strings.Contains(strings.ToLower(model), "haiku")
}

// ClaudeVersionRejection 只生成原版本范围拒绝消息，不读取配置或环境。
func ClaudeVersionRejection(clientVersion, minVersion, maxVersion string) string {
	if clientVersion == "" {
		return "Unable to determine Claude Code version. Please update Claude Code: npm update -g @anthropic-ai/claude-code"
	}

	if minVersion != "" && CompareVersions(clientVersion, minVersion) < 0 {
		return fmt.Sprintf("Your Claude Code version (%s) is below the minimum required version (%s). Please update: npm update -g @anthropic-ai/claude-code",
			clientVersion, minVersion)
	}

	if maxVersion != "" && CompareVersions(clientVersion, maxVersion) > 0 {
		return fmt.Sprintf("Your Claude Code version (%s) exceeds the maximum allowed version (%s). "+
			"Please downgrade: npm install -g @anthropic-ai/claude-code@%s && "+
			"set CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 to prevent auto-upgrade",
			clientVersion, maxVersion, maxVersion)
	}

	return ""
}
