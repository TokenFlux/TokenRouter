package googleforward

import (
	"errors"
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

func (s *Antigravity) antigravityResponseAdapter(c *attempt) *antigravity.ResponseAdapter {
	opts := antigravity.ResponseOptions{
		ReverseTools: func(body []byte) []byte { return c.reverseTools(body) },
		ClaudeError:  func(status int, kind, message string) error { return c.ClaudeError(status, kind, message) },
		CompatError: func(status int, kind, message string) error {
			return c.AntigravityCompatError(status, kind, message)
		},
		MapCollectionError: func(err error) error { return c.MapAntigravityCollectionError(err) },
		Failover: func(body []byte) error {
			return &forwardcore.UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: body, RetryableOnSameAccount: true}
		},
		IsFailover:    func(err error) bool { var value *forwardcore.UpstreamFailoverError; return errors.As(err, &value) },
		MarkCommitted: func() { c.Commit() },
	}
	if s.Options.Configured {
		opts.MaxLineSize = s.Options.MaxLineSize
		opts.StreamDataIntervalTimeout = s.Options.StreamInterval
		opts.StreamKeepaliveInterval = s.Options.StreamKeepalive
	}
	return &antigravity.ResponseAdapter{Options: opts}
}

func (s *Antigravity) extractImageInputSize(body []byte) string {
	return s.antigravityResponseAdapter(nil).ExtractImageInputSize(body)
}

func (s *Antigravity) unwrapV1InternalResponse(body []byte) ([]byte, error) {
	return s.antigravityResponseAdapter(nil).UnwrapV1InternalResponse(body)
}
