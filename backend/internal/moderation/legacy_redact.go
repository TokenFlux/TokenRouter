// Legacy 出口只服务未迁入口，算法和状态仍在本包，S15/S16 删除。
package moderation

var LegacyContentModerationSecretPatterns = contentModerationSecretPatterns

func LegacyRedactContentModerationSecrets(text string) string {
	return redactContentModerationSecrets(text)
}
