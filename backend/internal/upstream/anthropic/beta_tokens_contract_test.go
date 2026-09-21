package anthropic

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterBetaTokens(t *testing.T) {
	tokens := []string{"interleaved-thinking-2025-05-14", "tool-search-tool-2025-10-19"}
	filterSet := map[string]struct{}{
		"tool-search-tool-2025-10-19": {},
	}

	assert.Equal(t, []string{"interleaved-thinking-2025-05-14"}, FilterBetaTokens(tokens, filterSet))
	assert.Equal(t, tokens, FilterBetaTokens(tokens, nil))
	assert.Nil(t, FilterBetaTokens(nil, filterSet))
}
