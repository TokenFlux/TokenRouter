// 客户端工具 JSON 包装保留单步转换、数值精度与原错误语义。
package bridge

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

func AdaptResponsesClientToolsJSON(body []byte, upstream string) ([]byte, ResponsesClientToolMapping, error) {
	return AdaptResponsesClientToolsJSONWithMapping(
		body,
		upstream,
		ResponsesClientToolMapping{},
	)
}
func AdaptResponsesClientToolsJSONWithMapping(
	body []byte,
	upstream string,
	inherited ResponsesClientToolMapping,
	inheritedLoweredTools ...[]any,
) ([]byte, ResponsesClientToolMapping, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var requestBody map[string]any
	if err := decoder.Decode(&requestBody); err != nil {
		return body, ResponsesClientToolMapping{}, fmt.Errorf("decode %s Responses client tools: %w", upstream, err)
	}

	mapping, changed, err := AdaptResponsesClientToolsWithInheritedMapping(requestBody, inherited, inheritedLoweredTools...)
	if err != nil {
		return body, ResponsesClientToolMapping{}, err
	}
	if !changed {
		return body, mapping, nil
	}
	rebuilt, err := wirejson.Marshal(requestBody)
	if err != nil {
		return body, ResponsesClientToolMapping{}, fmt.Errorf("encode %s Responses client tools: %w", upstream, err)
	}
	return rebuilt, mapping, nil
}
