// Composer 图片描述按原顺序逐张执行与累加，失败保留已有辅助用量。
package grok

import (
	"encoding/json"
	"fmt"
	"strings"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

func BridgeComposerImages(body []byte, describe func(string, int) (string, protocolopenai.ForwardUsage, error)) ([]byte, protocolopenai.ForwardUsage, bool, error) {
	codec := BodyCodec{}

	if !codec.ShouldBridgeGrokComposerImageInputs(body) {
		return body, protocolopenai.ForwardUsage{}, false, nil
	}

	var reqBody map[string]any
	if err := wirejson.DecodeUseNumber(body, &reqBody); err != nil {
		return body, protocolopenai.ForwardUsage{}, false, fmt.Errorf("parse grok composer image bridge request: %w", err)
	}

	imageURLs := codec.CollectGrokComposerImageURLs(reqBody)
	if len(imageURLs) == 0 {
		return body, protocolopenai.ForwardUsage{}, false, nil
	}

	descriptions := make([]string, 0, len(imageURLs))
	var bridgeUsage protocolopenai.ForwardUsage
	for index, imageURL := range imageURLs {
		description, usage, err := describe(imageURL, index+1)
		if err != nil {
			return body, bridgeUsage, false, err
		}
		descriptions = append(descriptions, description)
		protocolopenai.AddForwardUsage(&bridgeUsage, usage)
	}

	if !codec.RewriteGrokComposerImagesAsText(reqBody, descriptions) {
		return body, bridgeUsage, false, nil
	}
	bridgedBody, err := wirejson.Marshal(reqBody)
	if err != nil {
		return body, bridgeUsage, false, fmt.Errorf("serialize grok composer image bridge request: %w", err)
	}
	return bridgedBody, bridgeUsage, true, nil

}

func DecodeComposerDescription(body []byte) (string, protocolopenai.ForwardUsage, error) {
	var parsed protocolopenai.ResponsesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", protocolopenai.ForwardUsage{}, fmt.Errorf("parse grok composer image bridge response: %w", err)
	}
	description := strings.TrimSpace((BodyCodec{}).GrokResponsesOutputText(&parsed))
	if description == "" {
		return "", protocolopenai.CopyForwardUsage(parsed.Usage), fmt.Errorf("grok composer image bridge returned empty description")
	}
	return description, protocolopenai.CopyForwardUsage(parsed.Usage), nil
}
