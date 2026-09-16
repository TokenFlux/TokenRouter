// 协议 scanner 的旧入口只转接，状态仍由每条流独立拥有。
package service

import (
	"bufio"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

func splitOpenAIConcatenatedJSONDocuments(payload []byte) ([][]byte, bool) {
	return wire.SplitConcatenatedJSONDocuments(payload)
}

type openAISSEJSONDocumentScanner = wire.SSEJSONDocumentScanner

func newOpenAISSEJSONDocumentScanner(scanner *bufio.Scanner) *openAISSEJSONDocumentScanner {
	return wire.NewSSEJSONDocumentScanner(scanner)
}
