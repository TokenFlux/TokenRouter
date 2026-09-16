package service

func convertGrokResponseToOpenAICompact(body []byte) ([]byte, error) {
	return grokBodyCodec().ConvertGrokResponseToOpenAICompact(body)
}
