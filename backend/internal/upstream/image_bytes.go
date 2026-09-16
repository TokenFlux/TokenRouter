// 图片字节原语保留原 padding、MIME 回退和读取边界。
package upstream

import (
	"encoding/base64"
	"io"
)

// ReadLimitedBody 读取上游响应体，限制最大读取量，避免异常响应撑爆内存。
func ReadLimitedBody(body io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 64 << 20
	}
	return io.ReadAll(io.LimitReader(body, limit))
}

// DecodedImage 是 base64 解码后的图片字节与嗅探出的 MIME。
type DecodedImage struct {
	Bytes []byte
	Mime  string
}

// DecodeBase64Image 解码上游返回的 base64 图片并按魔数嗅探 MIME。
func DecodeBase64Image(raw string) (DecodedImage, error) {
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// 部分上游会省略 padding，按 RawStdEncoding 再试一次。
		data, err = base64.RawStdEncoding.DecodeString(raw)
		if err != nil {
			return DecodedImage{}, err
		}
	}
	mime := SniffImageMIME(data)
	if mime == "" {
		mime = "image/png"
	}
	return DecodedImage{Bytes: data, Mime: mime}, nil
}

// SniffImageMIME 按魔数识别 PNG/JPEG/WebP。
func SniffImageMIME(data []byte) string {
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}

// ImageOutput 是执行器返回的一张输出图片。
type ImageOutput struct {
	Index int
	Bytes []byte
	Mime  string
}
