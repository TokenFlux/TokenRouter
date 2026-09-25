package httpapi

func newOpenAIWSV2TestConfig() *wsFixtureOptions {
	options := &wsFixtureOptions{}
	options.WS.Enabled = true
	options.WS.OAuthEnabled = true
	options.WS.APIKeyEnabled = true
	options.WS.ResponsesWebsocketsV2 = true
	options.WS.StickyResponseIDTTLSeconds = 3600
	return options
}
