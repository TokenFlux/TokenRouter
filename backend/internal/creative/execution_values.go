// 创作任务独占输出数量、大小与结果归一化规则。
package creative

import "github.com/TokenFlux/TokenRouter/internal/upstream"

const CreativeMaxOutputBytes = 32 << 20

func CreativePlatformImageModel(platform, model string) bool {
	switch platform {
	case PlatformOpenAI:
		return upstream.IsGPTImageGenerationModel(model)
	case PlatformGrok:
		return upstream.IsGrokImageGenerationModel(model)
	case PlatformGemini:
		return IsCreativeGeminiImageModel(model)
	default:
		return false
	}
}

// NormalizeCreativeOutputs 对执行器输出做后处理：
// 单张大小上限、固定取一张、sha256 去重。
func NormalizeCreativeOutputs(outputs []CreativeOutput) ([]CreativeOutput, error) {
	if len(outputs) == 0 {
		return nil, CreativeNonRetryableError("provider returned no image output")
	}
	seen := make(map[string]struct{}, len(outputs))
	out := make([]CreativeOutput, 0, len(outputs))
	for _, output := range outputs {
		if len(output.Bytes) == 0 {
			continue
		}
		if len(output.Bytes) > CreativeMaxOutputBytes {
			return nil, CreativeNonRetryableError("creative output %d exceeds size limit %d bytes", output.Index, CreativeMaxOutputBytes)
		}
		sum := Sha256Hex(output.Bytes)
		if _, ok := seen[sum]; ok {
			continue
		}
		seen[sum] = struct{}{}
		out = append(out, output)
	}
	if len(out) == 0 {
		return nil, CreativeNonRetryableError("provider returned no usable image output")
	}
	if len(out) > 1 {
		out = out[:1]
	}
	for index := range out {
		out[index].Index = index
	}
	return out, nil
}
