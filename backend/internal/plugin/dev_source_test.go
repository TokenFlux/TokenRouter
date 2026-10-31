package plugin

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// directoryFixture returns supplied package declarations without filesystem access.
type directoryFixture map[string]DirectoryPackage

// processFixture records development startup decisions without executing a child.
type processFixture struct {
	started []ProcessSpec // observed process startup specifications
}

// ReadDirectory returns the declaration selected by the configured fixture directory.
func (f directoryFixture) ReadDirectory(_ context.Context, directory string) (DirectoryPackage, error) {
	return f[directory], nil
}

// Replace records the selected process specification for later policy assertions.
func (f *processFixture) Replace(_ context.Context, spec ProcessSpec) error {
	f.started = append(f.started, spec)
	return nil
}

// Acquire reports unavailable because this policy fixture owns no process connection.
func (*processFixture) Acquire(context.Context, string) (*Lease, error) {
	return nil, ErrPluginUnavailable
}

// Stop accepts fixture cleanup without affecting real operating-system processes.
func (*processFixture) Stop(context.Context, string) error { return nil }

// ApplyConfig accepts fixture configuration without invoking any host callback transport.
func (*processFixture) ApplyConfig(context.Context, ProcessSpec) error { return nil }

// Status reports stopped because policy tests do not create native generations.
func (*processFixture) Status(string) Status { return Status{State: StateStopped} }

// StopAll completes fixture cleanup without starting or waiting for external work.
func (*processFixture) StopAll(context.Context) error { return nil }

// TestDevSourceRequiresBothSwitches checks the development directory admission gate.
// A configured directory alone cannot execute code when global plugins or
// explicit development mode is disabled by deployment configuration.
func TestDevSourceRequiresBothSwitches(t *testing.T) {
	for _, options := range []DevOptions{{Enabled: false, DevMode: true}, {Enabled: true, DevMode: false}} {
		processes := &processFixture{}
		source := NewDevSource(options, nil, processes, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err := source.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if source.started {
			t.Fatal("disabled development source started")
		}
		if err := source.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDevSourceSelectsNativeProcessesInIDOrder supplies reversed directory order
// and a static package. Only the first native plugin by ID may start, with
// declared permissions granted and executable checksum verification explicitly disabled.
func TestDevSourceSelectsNativeProcessesInIDOrder(t *testing.T) {
	reader := directoryFixture{
		"z": {Manifest: &manifest.Manifest{ID: "test.zulu", Version: "1.0.0", APIVersion: 1, Backend: &manifest.Backend{}}, Executable: "/fixture/z"},
		"a": {Manifest: &manifest.Manifest{ID: "test.alpha", Version: "1.0.0", APIVersion: 1, Backend: &manifest.Backend{}, Permissions: []string{"wallet.credit"}}, Executable: "/fixture/a"},
		"s": {Manifest: &manifest.Manifest{ID: "test.static", Version: "1.0.0", APIVersion: 1}},
	}
	processes := &processFixture{}
	source := NewDevSource(DevOptions{Enabled: true, DevMode: true, Directories: []string{"z", "s", "a"}, StateRoot: t.TempDir(), MaxRunning: 1}, reader, processes, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := source.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.done:
	case <-time.After(time.Second):
		t.Fatal("development loading did not finish")
	}
	if len(processes.started) != 1 || processes.started[0].PluginID != "test.alpha" {
		t.Fatalf("unexpected process selection: %+v", processes.started)
	}
	spec := processes.started[0]
	if spec.VerifyBinary || len(spec.Permissions) != 1 || spec.MemoryMiB != 256 || string(spec.ConfigJSON) != "{}" {
		t.Fatalf("incorrect development policy: %+v", spec)
	}
	if err := source.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
