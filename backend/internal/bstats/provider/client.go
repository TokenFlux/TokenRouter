package provider

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/host"

	"github.com/TokenFlux/TokenRouter/internal/bstats"
)

// client.go collects the explicitly allowed system fields and sends bStats
// reports using the MetricsBase.java v3.2.1 wire protocol. Its fixed endpoint
// identifies TokenRouter's registered server implementation; application data
// and credentials are never inputs to this adapter. SimplePie chart encoding
// follows the same upstream release, whose MIT license is retained in ../LICENSE.

const (
	// These protocol values match the registered project and upstream Java client release.
	reportURL      = "https://bStats.org/api/v2/data/server-implementation"
	serviceID      = 35120
	metricsVersion = "3.2.1"
	// Bounded requests and responses keep an unavailable metrics endpoint from delaying application shutdown indefinitely.
	requestTimeout   = 10 * time.Second
	maxResponseBytes = 1 << 20
)

// Client implements the fixed bStats protocol with cancellable bounded requests.
type Client struct {
	version  string                                      // TokenRouter build version
	client   *http.Client                                // dedicated metrics client
	endpoint string                                      // fixed production destination
	log      func(string, ...any)                        // optional diagnostic logger
	platform func(context.Context) (platformData, error) // allowed system field collector
}

// platformData contains only the operating-system fields accepted by bStats.
type platformData struct {
	OSName    string `json:"osName"`    // canonical operating system
	OSVersion string `json:"osVersion"` // operating system version
	OSArch    string `json:"osArch"`    // runtime CPU architecture
	CoreCount int    `json:"coreCount"` // available logical CPUs
}

// report is the complete whitelist for one outbound metrics document.
type report struct {
	platformData               // allowed system observations
	ServerUUID     string      `json:"serverUUID"`     // persisted random identifier
	MetricsVersion string      `json:"metricsVersion"` // reference protocol version
	Service        serviceData `json:"service"`        // registered service statistics
}

// serviceData associates custom version charts with the registered project.
type serviceData struct {
	ID           int        `json:"id"`           // registered project identifier
	CustomCharts []pieChart `json:"customCharts"` // collected version distributions
}

// pieChart preserves the official SimplePie envelope and value representation.
type pieChart struct {
	ChartID string   `json:"chartId"` // registered chart identifier
	Data    pieValue `json:"data"`    // simple pie observation
}

// pieValue represents one string category in a SimplePie observation.
type pieValue struct {
	Value string `json:"value"` // observed version string
}

// NewClient constructs the production transport without collecting or sending data.
// The client uses system proxy settings and a ten-second request timeout. Its
// closed connections require no persistent pool cleanup during application shutdown.
func NewClient(version string, log func(string, ...any)) *Client {
	return &Client{
		version: version,
		client: &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				Proxy:              http.ProxyFromEnvironment,
				DisableKeepAlives:  true,
				DisableCompression: true,
			},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				// A metrics redirect must not forward the instance identifier to an
				// unrelated destination; the configured official endpoint remains the only recipient.
				return http.ErrUseLastResponse
			},
		},
		endpoint: reportURL,
		log:      log,
		platform: collectPlatform,
	}
}

// Submit collects one whitelisted report and sends its compressed JSON body.
// The caller supplies a durably stored UUID and committed logging controls.
// Cancellation interrupts collection and HTTP, and all failures return without
// retries. Optional payload logging includes the UUID; response logging occurs
// only after a successful bounded response read.
func (c *Client) Submit(ctx context.Context, id string, config bstats.Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	platform, err := c.platform(ctx)
	if err != nil {
		return fmt.Errorf("collect bStats system information: %w", err)
	}
	charts := make([]pieChart, 0, 2)
	for _, chart := range []pieChart{
		{ChartID: "tokenrouter_version", Data: pieValue{Value: c.version}},
		{ChartID: "go_version", Data: pieValue{Value: runtime.Version()}},
	} {
		// SimplePie omits empty observations rather than recording an empty
		// category, matching the upstream custom-chart contract for missing versions.
		if chart.Data.Value != "" {
			charts = append(charts, chart)
		}
	}
	payload, err := json.Marshal(report{
		platformData:   platform,
		ServerUUID:     id,
		MetricsVersion: metricsVersion,
		Service:        serviceData{ID: serviceID, CustomCharts: charts},
	})
	if err != nil {
		return fmt.Errorf("encode bStats report: %w", err)
	}
	if config.LogSentData && c.log != nil {
		c.log("Sent bStats metrics data: %s", payload)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		return fmt.Errorf("compress bStats report: %w", err)
	}
	// Closing the gzip writer finishes its checksum and trailer before the
	// request computes Content-Length from the complete compressed byte sequence.
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish bStats compression: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.endpoint, bytes.NewReader(compressed.Bytes()),
	)
	if err != nil {
		return err
	}
	request.Close = true
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Connection", "close")
	request.Header.Set("Content-Encoding", "gzip")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Metrics-Service/1")
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("send bStats report: %w", err)
	}
	// Closing is best effort after the response is consumed or rejected; a
	// close error cannot turn an accepted report into a retryable submission.
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("bStats returned HTTP %d", response.StatusCode)
	}
	// Read one extra byte to distinguish a complete bounded response from a
	// larger response; oversized endpoint output must not become an unbounded log entry.
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read bStats response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("bStats response exceeds %d bytes", maxResponseBytes)
	}
	if config.LogResponseStatusText && c.log != nil {
		c.log("Sent data to bStats and received response: %s", body)
	}
	return nil
}

// collectPlatform reads system versions without collecting hostnames or addresses.
// Linux reports its kernel version as the Java client does; macOS reports the
// product version. Unsupported or failed system queries return errors to the caller.
func collectPlatform(ctx context.Context) (platformData, error) {
	platform, _, version, err := host.PlatformInformationWithContext(ctx)
	if err != nil {
		return platformData{}, err
	}
	name := runtime.GOOS
	// bStats groups operating systems using Java's canonical names. Normalize
	// Go platform names at this boundary so macOS and BSD avoid the Other bucket.
	switch runtime.GOOS {
	case "linux":
		name = "Linux"
		version, err = host.KernelVersionWithContext(ctx)
	case "darwin":
		name = "Mac OS X"
	case "windows":
		name = strings.TrimPrefix(platform, "Microsoft ")
	case "freebsd":
		name = "FreeBSD"
	case "openbsd":
		name = "OpenBSD"
	case "netbsd":
		name = "NetBSD"
	}
	if err != nil {
		return platformData{}, err
	}
	return platformData{
		OSName:    name,
		OSVersion: version,
		OSArch:    runtime.GOARCH,
		CoreCount: runtime.NumCPU(),
	}, nil
}
