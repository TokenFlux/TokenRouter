package errors_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	legacy "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// TestLegacyAndCategoryErrorCompatibility 验证旧错误消费者与新类别入口共享类型、比较和 HTTP 结果。
func TestLegacyAndCategoryErrorCompatibility(t *testing.T) {
	for _, code := range []int{200, 400, 401, 403, 404, 409, 418, 429, 499, 500, 502, 503, 504, 529} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			old := legacy.New(code, "stable_reason", "safe message").WithMetadata(map[string]string{"scope": "read"})
			fresh := apperror.New(apperror.Category(code), "stable_reason", "safe message").WithMetadata(map[string]string{"scope": "read"})
			if old.Error() != fresh.Error() || !errors.Is(old, fresh) || !errors.Is(fresh, old) {
				t.Fatal("error identity changed")
			}
			wrapped := fmt.Errorf("outer: %w", fresh)
			var target *legacy.ApplicationError
			if !errors.As(wrapped, &target) || target != fresh {
				t.Fatal("legacy errors.As must retain the same entity")
			}
			oldCode, oldBody := legacy.ToHTTP(old)
			newCode, newBody := httpx.ToHTTP(wrapped)
			if oldCode != code || newCode != code || !reflect.DeepEqual(oldBody, newBody) || legacy.Code(wrapped) != code {
				t.Fatal("HTTP projection changed")
			}
			newBody.Metadata["scope"] = "write"
			if fresh.Metadata["scope"] != "read" {
				t.Fatal("HTTP metadata must be copied")
			}
		})
	}
	if legacy.Code(nil) != 200 || apperror.Reason(nil) != "" || apperror.Message(nil) != "" {
		t.Fatal("nil compatibility changed")
	}
	cause := errors.New("private database detail")
	converted := apperror.FromError(cause)
	_, body := httpx.ToHTTP(converted)
	if body.Message != "internal error" || !errors.Is(converted, cause) {
		t.Fatal("generic error must preserve its cause and safe response")
	}
}
