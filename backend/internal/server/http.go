package server

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
	"golang.org/x/net/http2"

	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
)

// http.go constructs the Gin router and HTTP server from resolved server
// options.
// Generation streams and large uploads can outlive short HTTP calls. Server
// construction therefore limits header reads and idle connections while leaving
// whole-body and response deadlines to request owners. App's resolved options
// also supply an absolute body cap and optional cleartext HTTP/2 connection limits.

// ProviderSet supplies Wire with router construction and the HTTP listener
// configuration, using app's shared middleware runtime and resolved process options.
var ProviderSet = wire.NewSet(
	ProvideRouter,
	ProvideHTTPServer,
)

// ProvideRouter constructs the engine with recovery, proxy trust, and app's routes.
// Call before serving requests with a nonnil RouterRuntime; "release" changes
// Gin's process-wide mode, and invalid or absent proxy lists disable Gin forwarding trust.
func ProvideRouter(cfg Options, runtime *RouterRuntime) *gin.Engine {
	if cfg.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware2.Recovery())
	configureTrustedProxies(r, cfg)

	return SetupRouter(r, runtime)
}

// configureTrustedProxies applies an explicit peer list or disables Gin forwarding trust.
// Run before requests are served. Invalid lists are logged and replaced with nil;
// release mode also reports absent or explicitly empty configuration for operators.
func configureTrustedProxies(r *gin.Engine, cfg Options) {
	if cfg.TrustedProxiesConfigured {
		if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
			log.Printf("Failed to set trusted proxies: %v", err)
			// Gin may retain a valid prefix from the rejected list. Clear all peers
			// so a malformed configuration cannot leave partially enabled forwarding trust.
			_ = r.SetTrustedProxies(nil)
		}
		if len(cfg.TrustedProxies) == 0 && cfg.Mode == "release" {
			log.Printf("Warning: server.client_ip.trusted_proxies is explicitly empty; forwarded client IP trust is disabled")
		}
	} else {
		if err := r.SetTrustedProxies(nil); err != nil {
			log.Printf("Failed to disable trusted proxies: %v", err)
		}
		if cfg.Mode == "release" {
			log.Printf("Warning: server.client_ip.trusted_proxies is not configured; disabling the forwarded-IP compatibility switch will use direct peer addresses only")
		}
	}
}

// ProvideHTTPServer returns a server with header, idle, body, and H2C limits.
// Construct it before serving and supply H2C sizes that fit the adapter's uint32
// frame and int32 window fields. A positive body limit wraps the router; H2C
// configuration errors are logged before returning the server.
// @project-doc docs/operations/edge_security.md#http_ingress_limits
func ProvideHTTPServer(cfg Options, router *gin.Engine) *http.Server {
	httpHandler := http.Handler(router)
	server := &http.Server{
		Addr:           cfg.Address,
		Handler:        httpHandler,
		MaxHeaderBytes: cfg.MaxHeaderBytes,
		// Convert configured seconds into the header-read budget, which leaves
		// handler body consumption free to use its own deadline.
		ReadHeaderTimeout: time.Duration(cfg.ReadHeaderTimeout) * time.Second,
		// IdleTimeout also uses seconds and measures waiting between HTTP/1
		// requests, independently of the H2C adapter's configured idle budget.
		IdleTimeout: time.Duration(cfg.IdleTimeout) * time.Second,
		// Whole-body and response deadlines would apply to large uploads and
		// long generation streams. Their owners choose request-specific limits,
		// so ReadTimeout and WriteTimeout remain zero on the shared listener.
	}

	globalMaxSize := cfg.MaxRequestBodySize
	if globalMaxSize > 0 {
		// Wrap the complete router so body consumers inherit the absolute cap
		// even when their route also applies a narrower request-body limit.
		httpHandler = http.MaxBytesHandler(httpHandler, globalMaxSize)
		// Express the byte cap as MiB in this diagnostic.
		log.Printf("Global max request body size: %d bytes (%.2f MB)", globalMaxSize, float64(globalMaxSize)/(1<<20))
	}

	// The adapter accepts a separate H2C idle timeout while http.Server holds
	// the HTTP/1 value. Convert seconds before passing both connection policies.
	if cfg.H2C.Enabled {
		h2cConfig := cfg.H2C
		//nolint:staticcheck // HTTP/1 and h2c need separate idle timeouts; http.HTTP2Config has no separate IdleTimeout field.
		if err := http2.ConfigureServer(server, &http2.Server{
			MaxConcurrentStreams:         h2cConfig.MaxConcurrentStreams,
			IdleTimeout:                  time.Duration(h2cConfig.IdleTimeout) * time.Second,
			MaxReadFrameSize:             uint32(h2cConfig.MaxReadFrameSize),
			MaxUploadBufferPerConnection: int32(h2cConfig.MaxUploadBufferPerConnection),
			MaxUploadBufferPerStream:     int32(h2cConfig.MaxUploadBufferPerStream),
		}); err != nil {
			// Keep the returned server's Protocols unset on this error path. The
			// diagnostic reports configuration failure before explicit H2C opt-in occurs.
			log.Printf("Failed to configure HTTP/2 Cleartext (h2c): %v", err)
		} else {
			protocols := new(http.Protocols)
			protocols.SetHTTP1(true)
			protocols.SetUnencryptedHTTP2(true)
			server.Protocols = protocols
			log.Printf("HTTP/2 Cleartext (h2c) enabled: max_concurrent_streams=%d, idle_timeout=%ds, max_read_frame_size=%d, max_upload_buffer_per_connection=%d, max_upload_buffer_per_stream=%d",
				h2cConfig.MaxConcurrentStreams,
				h2cConfig.IdleTimeout,
				h2cConfig.MaxReadFrameSize,
				h2cConfig.MaxUploadBufferPerConnection,
				h2cConfig.MaxUploadBufferPerStream,
			)
		}
	}

	server.Handler = httpHandler
	return server
}
