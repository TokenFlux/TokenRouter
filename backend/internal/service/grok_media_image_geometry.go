package service

func applyGrokImagineImageGeometry(body []byte) ([]byte, error) {
	return grokMediaCodec().ApplyGrokImagineImageGeometry(body)
}

func grokImagineAspectRatioFromSize(size string) string {
	return grokMediaCodec().GrokImagineAspectRatioFromSize(size)
}
