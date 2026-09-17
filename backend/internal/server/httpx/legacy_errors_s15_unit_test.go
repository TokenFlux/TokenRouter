//go:build unit

package httpx_test

import (
	"net/http"
	"testing"

	legacy "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/stretchr/testify/require"
)

// 旧构造器只用作错误身份夹具，HTTP 投影唯一入口已归 httpx，兼容夹具退出 S16。
func TestToHTTP_S15Legacy(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantStatusCode int
		wantBody       legacy.Status
	}{
		{
			name:           "nil_error",
			err:            nil,
			wantStatusCode: http.StatusOK,
			wantBody:       legacy.Status{Code: int32(http.StatusOK)},
		},
		{
			name:           "application_error",
			err:            legacy.Forbidden("FORBIDDEN", "no access"),
			wantStatusCode: http.StatusForbidden,
			wantBody: legacy.Status{
				Code:    int32(http.StatusForbidden),
				Reason:  "FORBIDDEN",
				Message: "no access",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := httpx.ToHTTP(tt.err)
			require.Equal(t, tt.wantStatusCode, code)
			require.Equal(t, tt.wantBody, body)
		})
	}
}

func TestToHTTP_MetadataDeepCopy_S15Legacy(t *testing.T) {
	md := map[string]string{"k": "v"}
	appErr := legacy.BadRequest("BAD_REQUEST", "invalid").WithMetadata(md)

	code, body := httpx.ToHTTP(appErr)
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "v", body.Metadata["k"])

	md["k"] = "changed"
	require.Equal(t, "v", body.Metadata["k"])

	appErr.Metadata["k"] = "changed-again"
	require.Equal(t, "v", body.Metadata["k"])
}
