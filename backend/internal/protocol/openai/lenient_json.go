package openai

import "fmt"

const jsonUTF8BOMLen = 3
const maxDecompressedBodySize = 64 << 20

// BodyLimitError 表示规范化后的请求超过允许大小，由 HTTP 入口映射为原错误类型。
type BodyLimitError struct{ Limit int64 }

func (e *BodyLimitError) Error() string {
	return fmt.Sprintf("normalized JSON body exceeds %d bytes", e.Limit)
}

// NormalizeLenientJSONRequestBody 转义部分异常 OpenAI 兼容客户端
// 直接写入 JSON 字符串的控制字节，并限制规范化后的最大大小。
func NormalizeLenientJSONRequestBody(body []byte, maxNormalizedBytes int64) ([]byte, error) {
	if maxNormalizedBytes <= 0 {
		maxNormalizedBytes = maxDecompressedBodySize
	}

	body = trimUTF8BOM(body)
	if len(body) == 0 {
		return body, nil
	}
	if int64(len(body)) > maxNormalizedBytes {
		return nil, &BodyLimitError{Limit: maxNormalizedBytes}
	}

	var out []byte
	inString := false
	escaped := false
	for i, b := range body {
		if inString && isJSONControlByte(b) {
			if out == nil {
				capHint := len(body) + 6
				if int64(capHint) > maxNormalizedBytes {
					capHint = int(maxNormalizedBytes)
				}
				out = make([]byte, 0, capHint)
				out = append(out, body[:i]...)
			}
			if int64(len(out)+6) > maxNormalizedBytes {
				return nil, &BodyLimitError{Limit: maxNormalizedBytes}
			}
			out = appendJSONUnicodeEscape(out, b)
			escaped = false
			continue
		}

		switch {
		case escaped:
			escaped = false
		case inString && b == '\\':
			escaped = true
		case b == '"':
			inString = !inString
		}

		if out != nil {
			if int64(len(out)+1) > maxNormalizedBytes {
				return nil, &BodyLimitError{Limit: maxNormalizedBytes}
			}
			out = append(out, b)
		}
	}
	if out != nil {
		return out, nil
	}
	return body, nil
}

// trimUTF8BOM 移除 JSON 请求体开头的 UTF-8 BOM。
func trimUTF8BOM(body []byte) []byte {
	if len(body) >= jsonUTF8BOMLen && body[0] == 0xef && body[1] == 0xbb && body[2] == 0xbf {
		return body[jsonUTF8BOMLen:]
	}
	return body
}

// isJSONControlByte 判断字节是否需要在 JSON 字符串中转义。
func isJSONControlByte(b byte) bool {
	return b < 0x20 || b == 0x7f
}

// appendJSONUnicodeEscape 把控制字节追加为 \u00xx 形式的 JSON 转义。
func appendJSONUnicodeEscape(dst []byte, b byte) []byte {
	const hex = "0123456789abcdef"
	return append(dst, '\\', 'u', '0', '0', hex[b>>4], hex[b&0x0f])
}
