// 尺寸探测只委托平台原生实现，原图片头读取上限不变。
package service

import nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

func detectOpenAIImageResultSize(encoded string) string {
	return nativeopenai.DetectOpenAIImageResultSize(encoded)
}
