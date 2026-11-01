package plugin

import "context"

// http.go defines bounded HTTP contribution values independently of transport
// libraries. The panel adapter selects a declared route, authenticates its caller,
// filters headers, and owns the request deadline. The process adapter translates
// these values to RPC messages and returns safe host errors for transport failures.

// HTTPCaller carries verified panel identity, negotiated language, and private capability authorization.
type HTTPCaller struct {
	UserID   int64  // authenticated panel account identifier
	Username string // current account display username
	Role     string // authenticated panel account role
	Locale   string // host-negotiated request language
	// Grant is sent only inside the authenticated process RPC and must never enter browser headers, response payloads, or general request logging.
	Grant string `json:"-"`
}

// HTTPRequest contains the filtered request delivered to one declared plugin endpoint.
type HTTPRequest struct {
	Method   string            // original HTTP method token
	Path     string            // canonical plugin-relative request path
	RawQuery string            // original encoded query string
	Headers  map[string]string // copied allowed request headers
	Body     []byte            // bounded original request bytes
	Caller   *HTTPCaller       // optional verified panel caller
	ClientIP string            // host-resolved original client address
}

// HTTPResponse contains a plugin reply before public header and content validation.
type HTTPResponse struct {
	Status  int               // plugin-supplied HTTP status code
	Headers map[string]string // plugin-supplied response header values
	Body    []byte            // plugin-supplied buffered response bytes
}

// HTTPTransport exchanges buffered contribution messages without exposing RPC call options.
type HTTPTransport interface {
	// Exchange returns one plugin response or a safe transport failure.
	// The caller retains the process lease until return. Deadlines map to
	// ErrPluginTimeout and other RPC failures to ErrPluginRequestFailed.
	Exchange(context.Context, *HTTPRequest) (*HTTPResponse, error)
}
