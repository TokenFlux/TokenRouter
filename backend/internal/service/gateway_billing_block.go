package service

import claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

func computeClaudeCodeFingerprint(body []byte, version string) string {
	return claude.ComputeClaudeCodeFingerprint(body, version)
}

func extractFirstUserText(body []byte) string { return claude.ExtractFirstUserText(body) }

func buildBillingAttributionText(body []byte, cliVersion string) (string, error) {
	return claude.BuildBillingAttributionText(body, cliVersion)
}
