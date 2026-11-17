// Package modelcatalog resolves model metadata, prices, and execution attributes
// from models.dev publications and configured supplements.
//
// Start here:
//   - catalog.go parses publications and selects queryable model identities.
//   - index.go indexes complete model identities and attribute sources.
//   - reader.go defines the metadata access used by consumers.
//
// File groups:
//   - catalog.go, index.go, and origins.go organize model identity and authorship.
//   - attributes*.go and capabilities.go validate and combine model declarations.
//   - catalog.go retains service SDK declarations independently of price eligibility.
package modelcatalog
