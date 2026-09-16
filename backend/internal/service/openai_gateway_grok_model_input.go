package service

func sanitizeGrokResponsesModelInput(body []byte) ([]byte, error) {
	return grokBodyCodec().SanitizeGrokResponsesModelInput(body)
}
