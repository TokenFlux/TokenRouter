package httpapi

import (
	"bytes"
	"io"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
)

type auxiliaryHTTPRecorder struct {
	lastReq      *http.Request
	lastBody     []byte
	lastProxyURL string
	requests     []*http.Request
	bodies       [][]byte

	resp      *http.Response
	responses []*http.Response
	err       error

	lastTLSProfile *tlsfingerprint.Profile
}

func (u *auxiliaryHTTPRecorder) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	u.lastReq = req
	u.lastProxyURL = proxyURL
	if req != nil && req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		u.lastBody = b
		u.bodies = append(u.bodies, append([]byte(nil), b...))
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	u.requests = append(u.requests, req)
	if u.err != nil {
		return nil, u.err
	}
	if len(u.responses) > 0 {
		resp := u.responses[0]
		u.responses = u.responses[1:]
		return resp, nil
	}
	return u.resp, nil
}
func (u *auxiliaryHTTPRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.lastTLSProfile = profile
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}
