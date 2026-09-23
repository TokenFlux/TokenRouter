package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIRecordUsageInputsCarryQuotaPlatform(t *testing.T) {
	files := []struct {
		name  string
		paths []string
	}{
		{"openai_gateway_handler.go", []string{"openaiattempt/responses_attempts.go", "openaiattempt/openai_messages_attempts.go"}},
		{"openai_chat_completions.go", []string{"openaiattempt/openai_chat_attempts.go"}},
		{"openai_embeddings.go", []string{"mediaentry/openai_embeddings.go"}},
		{"openai_images.go", []string{"mediaentry/media_s11_adapter.go"}},
	}

	for _, item := range files {
		t.Run(item.name, func(t *testing.T) {
			for _, name := range item.paths {
				fset := token.NewFileSet()
				file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
				require.NoError(t, err)

				var missing []token.Position
				matches := 0
				ast.Inspect(file, func(node ast.Node) bool {
					literal, ok := node.(*ast.CompositeLit)
					if !ok || !isOpenAIRecordUsageInputLiteral(literal.Type) {
						return true
					}
					matches++
					if !compositeLiteralHasKey(literal, "QuotaPlatform") {
						missing = append(missing, fset.Position(literal.Lbrace))
					}
					return true
				})

				// 后扣 worker 使用 background context，所有 OpenAI 用量入参必须显式带上请求时算定的平台。
				require.Empty(t, missing, "OpenAI usage post-billing must receive request-time QuotaPlatform")
				// 路径迁移后仍须检查到实际完成输入，不能以零命中作为通过。
				require.Positive(t, matches, "must inspect actual OpenAI completion capture in %s", name)
			}
		})
	}
}

func isOpenAIRecordUsageInputLiteral(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "gatewaycapture" && selector.Sel.Name == "OpenAICapture"
}

func compositeLiteralHasKey(literal *ast.CompositeLit, key string) bool {
	for _, elt := range literal.Elts {
		pair, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		ident, ok := pair.Key.(*ast.Ident)
		if ok && ident.Name == key {
			return true
		}
	}
	return false
}
