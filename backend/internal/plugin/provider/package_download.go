package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pkgfmt"
)

// package_download.go retrieves bounded plugin archives and verifies their
// compressed identity before extraction. HTTPS is enforced on initial requests
// and redirects, while an optional configured prefix applies only to GitHub
// artifact hosts. Proxy credentials never enter public error metadata.

// DownloadOptions configures administrator-selected artifact transfer and proxy routing.
type DownloadOptions struct {
	URLPrefix string // optional GitHub artifact mirror prefix
	ProxyURL  string // optional HTTP HTTPS or SOCKS5 proxy
}

// downloader owns a bounded client and the permitted GitHub URL transformation.
type downloader struct {
	client *http.Client // bounded HTTPS request client
	prefix string       // configured artifact URL prefix
}

// contextReader stops copying local archive bytes after its request is canceled.
type contextReader struct {
	ctx    context.Context // archive operation cancellation context
	reader io.Reader       // underlying archive byte source
}

// newDownloader validates transfer options and constructs an isolated HTTP transport.
// The client permits only HTTPS destinations, including redirects. Download limits
// apply to response bytes independently of Content-Length or transfer encoding.
func newDownloader(options DownloadOptions) (*downloader, error) {
	if options.URLPrefix != "" && !index.HTTPSURL(options.URLPrefix) {
		return nil, fmt.Errorf("plugin download prefix must use HTTPS")
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("plugin downloads require an HTTP transport")
	}
	transport := base.Clone()
	if options.ProxyURL != "" {
		proxy, err := url.Parse(options.ProxyURL)
		if err != nil || proxy.Host == "" || (proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5") {
			return nil, fmt.Errorf("invalid plugin download proxy")
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	client := &http.Client{Transport: transport, Timeout: 120 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if !index.HTTPSURL(request.URL.String()) || len(via) >= 10 {
			return fmt.Errorf("plugin download redirect rejected")
		}
		return nil
	}}
	return &downloader{client: client, prefix: options.URLPrefix}, nil
}

// archive copies an uploaded or indexed archive into a caller-owned staging file.
// The accepted digest is checked before unpacking. Upload resolution must still
// match plugin identity, version, and manifest digest from the installation request.
func (p *PackageFiles) archive(ctx context.Context, ref plugin.PackageRef, destination string) error {
	var source io.ReadCloser
	switch ref.Source {
	case plugin.SourceUpload:
		upload, err := p.ResolveUpload(ctx, ref.UploadToken)
		if err != nil {
			return err
		}
		if upload.Snapshot.Manifest.ID != ref.PluginID || upload.Snapshot.Version != ref.Version || upload.Snapshot.ManifestSHA256 != ref.ManifestSHA256 {
			return plugin.ErrPackageInvalid
		}
		source, err = os.Open(filepath.Join(p.root, "staging", "uploads", ref.UploadToken, "package.tar.gz"))
		if err != nil {
			return plugin.ErrPackageMissing.WithCause(err)
		}
	case plugin.SourceIndex:
		response, err := p.download.get(ctx, ref.Artifact.URL)
		if err != nil {
			return plugin.ErrDownloadFailed.WithCause(err)
		}
		source = response.Body
	default:
		return plugin.ErrPackageInvalid
	}
	defer func() { _ = source.Close() }()
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(&contextReader{ctx: ctx, reader: source}, pkgfmt.MaxArchiveBytes+1))
	if err != nil {
		return plugin.ErrDownloadFailed.WithCause(err)
	}
	if count > pkgfmt.MaxArchiveBytes || count != ref.Artifact.Size {
		return plugin.ErrPackageInvalid.WithCause(fmt.Errorf("archive size does not match accepted metadata"))
	}
	if hex.EncodeToString(hash.Sum(nil)) != ref.Artifact.SHA256 {
		return plugin.ErrChecksumMismatch
	}
	return file.Close()
}

// get opens one HTTPS artifact response with the configured GitHub prefix policy.
// Non-success responses are closed before returning. Successful callers own the
// response body and enforce the archive size limit while consuming its contents.
func (d *downloader) get(ctx context.Context, location string) (*http.Response, error) {
	if !index.HTTPSURL(location) {
		return nil, fmt.Errorf("plugin downloads require HTTPS")
	}
	parsed, _ := url.Parse(location)
	host := strings.ToLower(parsed.Hostname())
	if d.prefix != "" && (host == "github.com" || host == "objects.githubusercontent.com") {
		location = d.prefix + location
	}
	if !index.HTTPSURL(location) {
		return nil, fmt.Errorf("invalid prefixed plugin URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	response, err := d.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK || response.ContentLength > pkgfmt.MaxArchiveBytes {
		_ = response.Body.Close()
		return nil, fmt.Errorf("plugin download returned status %d or exceeded the size limit", response.StatusCode)
	}
	return response, nil
}

// Read checks cancellation before forwarding one read to the archive source.
func (r *contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
