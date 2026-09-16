package service

func buildGrokCompactRequestBody(body []byte) ([]byte, error) {
	return grokBodyCodec().BuildGrokCompactRequestBody(body)
}

func convertGrokResponseToOpenAICompact(body []byte) ([]byte, error) {
	return grokBodyCodec().ConvertGrokResponseToOpenAICompact(body)
}
