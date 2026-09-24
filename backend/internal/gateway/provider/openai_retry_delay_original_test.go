//go:build unit

package provider

import (
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

func TestOpenAI429RetryDelayHonorsBoundedRetryAfter(t *testing.T) {
	deadline := time.Now().Add(accountcore.RuntimeRetryWindow)
	require.Equal(t, openAIOAuth429RetryDelay, OpenAI429RetryDelay(nil, deadline))
	require.Equal(t, openAIOAuth429MaxRetryDelay, OpenAI429RetryDelay(http.Header{"Retry-After": []string{"90"}}, deadline))
}
