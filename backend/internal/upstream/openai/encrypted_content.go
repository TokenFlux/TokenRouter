// 平台续接报文只处理每轮输入，账号选择、会话缓存与入站重试由调用方拥有。
package openai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/tidwall/gjson"
)

func OpenAIEncryptedContentDigest(encrypted string) string {
	sum := sha256.Sum256([]byte(encrypted))
	return hex.EncodeToString(sum[:])
}

// OpenAIEncryptedLineageItemType 限定 lineage 覆盖的项类型，与剥离端
// wire.SanitizeEncryptedReasoningInputItem 能处理的类型对称；其他类型即使携带
// encrypted_content 也不收摘要，避免记入永远剥不掉的摘要后每轮空转解码。
func OpenAIEncryptedLineageItemType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "reasoning", "compaction", "compaction_summary":
		return true
	default:
		return false
	}
}

// CollectOpenAIEncryptedContentDigestsRaw 收集 input 中 lineage 覆盖类型所携
// 密文的摘要。payload 须遵守 replay 所有权不变式（零拷贝视图解析）。
func CollectOpenAIEncryptedContentDigestsRaw(payload []byte) []string {
	if len(payload) == 0 {
		return nil
	}
	input := gjson.Get(OpenAIWSPayloadStringView(payload), "input")
	if !input.Exists() {
		return nil
	}
	var digests []string
	appendItem := func(item gjson.Result) {
		if !OpenAIEncryptedLineageItemType(item.Get("type").String()) {
			return
		}
		encrypted := item.Get("encrypted_content")
		if encrypted.Type == gjson.String && encrypted.String() != "" {
			digests = append(digests, OpenAIEncryptedContentDigest(encrypted.String()))
		}
	}
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			appendItem(item)
			return true
		})
		return digests
	}
	if input.IsObject() {
		appendItem(input)
	}
	return digests
}

// StripOpenAIInvalidEncryptedContentItems 剥离 input 中摘要命中 invalid 的密文
// 项，剥离语义与 trimOpenAIEncryptedReasoningItems 对齐：reasoning 仅剥
// encrypted_content（连带 content:null 清理与空骨架删除），compaction/
// compaction_summary 整项删除。返回被改写的项数。
func StripOpenAIInvalidEncryptedContentItems(reqBody map[string]any, invalid map[string]struct{}) int {
	if len(reqBody) == 0 || len(invalid) == 0 {
		return 0
	}
	inputValue, has := reqBody["input"]
	if !has {
		return 0
	}
	stripped := 0
	stripItem := func(item any) (next any, changed bool, keep bool) {
		inputItem, ok := item.(map[string]any)
		if !ok {
			return item, false, true
		}
		encrypted, ok := inputItem["encrypted_content"].(string)
		if !ok || encrypted == "" {
			return item, false, true
		}
		if _, hit := invalid[OpenAIEncryptedContentDigest(encrypted)]; !hit {
			return item, false, true
		}
		return wire.SanitizeEncryptedReasoningInputItem(item)
	}
	switch input := inputValue.(type) {
	case []any:
		filtered := input[:0]
		for _, item := range input {
			nextItem, changed, keep := stripItem(item)
			if changed {
				stripped++
			}
			if !keep {
				continue
			}
			filtered = append(filtered, nextItem)
		}
		if stripped == 0 {
			return 0
		}
		if len(filtered) == 0 {
			delete(reqBody, "input")
			return stripped
		}
		reqBody["input"] = filtered
		return stripped
	case map[string]any:
		nextItem, changed, keep := stripItem(input)
		if !changed {
			return 0
		}
		if !keep {
			delete(reqBody, "input")
			return 1
		}
		nextMap, ok := nextItem.(map[string]any)
		if !ok {
			return 0
		}
		reqBody["input"] = nextMap
		return 1
	default:
		return 0
	}
}

// OpenAIRawPayloadHasInvalidEncryptedContent 探测 raw payload 是否携带命中
// invalid 摘要的密文项；零命中的常态路径不做任何改写。payload 须遵守 replay
// 所有权不变式（零拷贝视图解析）。
func OpenAIRawPayloadHasInvalidEncryptedContent(payload []byte, invalid map[string]struct{}) bool {
	if len(payload) == 0 || len(invalid) == 0 {
		return false
	}
	input := gjson.Get(OpenAIWSPayloadStringView(payload), "input")
	if !input.Exists() {
		return false
	}
	hit := false
	checkItem := func(item gjson.Result) bool {
		encrypted := item.Get("encrypted_content")
		if encrypted.Type != gjson.String || encrypted.String() == "" {
			return false
		}
		_, matched := invalid[OpenAIEncryptedContentDigest(encrypted.String())]
		return matched
	}
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			if checkItem(item) {
				hit = true
				return false
			}
			return true
		})
		return hit
	}
	if input.IsObject() {
		return checkItem(input)
	}
	return false
}

// StripOpenAIInvalidEncryptedContentFromReplayItems 对 replay 历史序列做同款
// 剥离。有改动时返回新头数组，命中项以重写后的新正文整体替换（遵守 replay
// 所有权不变式，原正文不被修改）；未命中时原样返回。
func StripOpenAIInvalidEncryptedContentFromReplayItems(items []json.RawMessage, invalid map[string]struct{}) ([]json.RawMessage, int) {
	if len(items) == 0 || len(invalid) == 0 {
		return items, 0
	}
	hit := false
	for _, item := range items {
		encrypted := gjson.Get(OpenAIWSPayloadStringView(item), "encrypted_content")
		if encrypted.Type != gjson.String || encrypted.String() == "" {
			continue
		}
		if _, matched := invalid[OpenAIEncryptedContentDigest(encrypted.String())]; matched {
			hit = true
			break
		}
	}
	if !hit {
		return items, 0
	}
	stripped := 0
	next := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		var decoded map[string]any
		if err := wirejson.DecodeUseNumber(item, &decoded); err != nil {
			next = append(next, item)
			continue
		}
		encrypted, ok := decoded["encrypted_content"].(string)
		if !ok || encrypted == "" {
			next = append(next, item)
			continue
		}
		if _, matched := invalid[OpenAIEncryptedContentDigest(encrypted)]; !matched {
			next = append(next, item)
			continue
		}
		nextItem, changed, keep := wire.SanitizeEncryptedReasoningInputItem(decoded)
		if !changed {
			next = append(next, item)
			continue
		}
		stripped++
		if !keep {
			continue
		}
		rebuilt, err := wirejson.Marshal(nextItem)
		if err != nil {
			next = append(next, item)
			stripped--
			continue
		}
		next = append(next, json.RawMessage(rebuilt))
	}
	if stripped == 0 {
		return items, 0
	}
	return next, stripped
}

// StripOpenAIInvalidEncryptedContentRaw 是 raw JSON 版剥离：先零成本探测，命中
// 才解码改写重编码。返回改写后的 payload 与剥离项数；未命中时原样返回。
func StripOpenAIInvalidEncryptedContentRaw(payload []byte, invalid map[string]struct{}) ([]byte, int, error) {
	if !OpenAIRawPayloadHasInvalidEncryptedContent(payload, invalid) {
		return payload, 0, nil
	}
	var decoded map[string]any
	if err := wirejson.DecodeUseNumber(payload, &decoded); err != nil {
		return payload, 0, err
	}
	stripped := StripOpenAIInvalidEncryptedContentItems(decoded, invalid)
	if stripped == 0 {
		return payload, 0, nil
	}
	rebuilt, err := wirejson.Marshal(decoded)
	if err != nil {
		return payload, 0, err
	}
	return rebuilt, stripped, nil
}
