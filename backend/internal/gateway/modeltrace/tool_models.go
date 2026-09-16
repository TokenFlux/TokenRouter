package modeltrace

import (
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/modelmap"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// RewriteAPIKeyAdditionalModels 重定向 Responses 工具声明中的附加模型。
func RewriteAPIKeyAdditionalModels(body []byte, mapping map[string]string) ([]byte, error) {
	if len(body) == 0 || len(mapping) == 0 || !gjson.ValidBytes(body) {
		return body, nil
	}
	rewritten := body
	for index, tool := range gjson.GetBytes(body, "tools").Array() {
		model := strings.TrimSpace(tool.Get("model").String())
		mappedModel, matched := modelmap.Resolve(mapping, model)
		if !matched {
			continue
		}
		var err error
		rewritten, err = sjson.SetBytes(rewritten, fmt.Sprintf("tools.%d.model", index), mappedModel)
		if err != nil {
			return body, err
		}
	}
	return rewritten, nil
}
