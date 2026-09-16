// 旧字段入口委托 gateway/media 唯一请求投影。
package service

import (
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	nativeupstream "github.com/TokenFlux/TokenRouter/internal/upstream"
)

func nativeImageRequestView(value *OpenAIImagesRequest) *nativeupstream.ImageRequest {
	return gatewaymedia.NativeImageRequest(value)
}
