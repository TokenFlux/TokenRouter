package googleforward

import (
	"io"
	"net/http"

	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

func (s *Gemini) geminiResponseAdapter(c *attempt) *gemininative.ResponseAdapter {
	return &gemininative.ResponseAdapter{Options: gemininative.ResponseOptions{
		GoogleError: func(status int, message string) error { return c.GoogleError(status, message) },

		ReadBody: func(body io.Reader) ([]byte, error) {
			return c.ReadBody(body, s.Options.ResponseReadLimit)
		},
		WriteHeaders:    func(dst, src http.Header) { c.WriteHeaders(dst, src, s.HeaderFilter) },
		DebugHeaders:    s.Options.Configured && s.Options.DebugHeaders,
		HasHeaderFilter: s.HeaderFilter != nil,

		ObserveImages: func(body []byte) { c.observeImages(body) },
		ReverseTools:  func(body []byte) []byte { return c.reverseTools(body) },
		ClaudeError:   func(status int, kind, message string) error { return c.ClaudeError(status, kind, message) },
		ChatError: func(status int, kind, message string) error {
			return c.ChatError(status, kind, message)
		},
		CompatError: func(protocol gemininative.OpenAICompatProtocol, status int, kind, message string) error {
			return c.GeminiOpenAICompatError(protocol, status, kind, message)
		},
	}}
}
