package httpapi

import (
	"bufio"
	"io"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

func (p *OpenAIResponseOutput) Scanner(r io.Reader) *bufio.Scanner {
	maxLineSize := openAIResponseDefaultMaxLineSize
	if p.Options.Configured && p.Options.MaxLineSize > 0 {
		maxLineSize = p.Options.MaxLineSize
	}
	return openai.NewCompatSSEScanner(r, maxLineSize)
}
