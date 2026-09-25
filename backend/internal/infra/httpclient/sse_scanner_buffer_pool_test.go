package httpclient_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/stretchr/testify/require"
)

func TestSSEScannerBuf64KPool_GetPutDoesNotPanic(t *testing.T) {
	buf := httpclient.GetSSEScannerBuf64K()
	require.NotNil(t, buf)
	require.Equal(t, httpclient.SSEScannerBuf64KSize, len(buf[:]))

	buf[0] = 1
	httpclient.PutSSEScannerBuf64K(buf)

	// 允许传入 nil，确保不会 panic
	httpclient.PutSSEScannerBuf64K(nil)
}
