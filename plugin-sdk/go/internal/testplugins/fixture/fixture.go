package fixture

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// fixture.go supplies deliberately failing plugin behaviors for runtime tests.
// The fixture executable selects one behavior family at startup, and crash
// requests choose their failure explicitly through a query parameter. None of
// these handlers are installed as an application feature or published example.
//
// Process-exit and uncooperative-handler cases exercise supervisor recovery,
// while oversized bodies and HTML exercise the host's response boundary. The
// memory fixture touches a bounded allocation so resource observations see real
// resident pages without starting an unlimited allocator. Environment inspection
// returns the child's own variables for tests that verify host-secret exclusion;
// production configuration is never read by this fixture package.

// Plugin implements one selected runtime failure fixture and its HTTP contribution.
type Plugin struct {
	kind string // selected fixture behavior family
}

// New constructs a fixture for crashy, slowstart, or envdump behavior.
func New(kind string) *Plugin { return &Plugin{kind: kind} }

// Init blocks only the slowstart fixture until its host deadline is canceled.
// Other fixtures become ready immediately. This makes startup timeout tests
// independent of an arbitrary sleep duration or a machine's scheduling speed.
func (p *Plugin) Init(ctx context.Context, _ *sdk.Env) error {
	if p.kind == "slowstart" {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

// Handler exposes the selected fixture's own request behavior for host tests.
func (p *Plugin) Handler() http.Handler { return http.HandlerFunc(p.serveHTTP) }

// serveHTTP performs the explicitly requested fault without touching host state.
// Process faults intentionally bypass SDK recovery, while ordinary handler
// panics remain recoverable. Tests must terminate the process after a loop fixture.
func (p *Plugin) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if p.kind == "envdump" {
		sdk.WriteJSON(w, http.StatusOK, os.Environ())
		return
	}
	switch r.URL.Query().Get("mode") {
	case "panic":
		panic("fixture handler panic")
	case "panic-process":
		// A separate goroutine deliberately escapes the unary recovery boundary, modeling an author-owned worker panic that terminates the plugin process.
		go func() { panic("fixture process panic") }()
		select {}
	case "exit":
		os.Exit(23)
	case "loop":
		select {}
	case "memory":
		// Touch one byte per page in a bounded 256 MiB allocation so RSS sampling observes committed pages rather than virtual address reservation.
		memory := make([]byte, 256<<20)
		for i := 0; i < len(memory); i += 4096 {
			memory[i] = 1
		}
		sdk.WriteJSON(w, http.StatusOK, map[string]int{"bytes": len(memory)})
		runtime.KeepAlive(memory)
	case "huge":
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("x", 6<<20)))
	case "html":
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>fixture</html>"))
	default:
		sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ready": true})
	}
}
