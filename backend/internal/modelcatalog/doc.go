// Package modelcatalog resolves model metadata, prices, and execution attributes
// from models.dev publications and configured supplements.
//
// Start here:
//   - catalog.go parses publications and selects queryable model identities.
//   - resolution.go combines catalog attributes with platform and route rules.
//   - reader.go defines the metadata access used by consumers.
//
// File groups:
//   - catalog.go, index.go, and origins.go organize model identity and authorship.
//   - attributes*.go and capabilities.go validate and combine model declarations.
//   - encoding.go selects model payload rules by author identity and protocol.
//   - platform.go, bedrock.go, qoder_catalog.go, and ollama.go describe service rules.
//   - opencode.go indexes service-specific model protocols independently of prices.
//   - media.go supplies known media-generation attributes for supported routes.
package modelcatalog
