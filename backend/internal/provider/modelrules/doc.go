// Package modelrules resolves provider route attributes and protocol encoding
// rules against a pinned model catalog and a group's configured overrides.
//
// Start here:
//   - target.go identifies the actual service, account and outbound model.
//   - resolution.go merges the sources and evaluates request conditions.
//   - platform.go composes provider access policies with adapter restrictions.
//
// File groups:
//   - openai.go, anthropic.go, grok.go and deepseek.go define author and route rules.
//   - bedrock.go, antigravity.go and ollama.go apply hosted service restrictions.
//   - media.go dispatches image and video rules, including gemini.go.
//   - opencode.go interprets service offerings and encoding.go handles shared wire formats.
package modelrules
