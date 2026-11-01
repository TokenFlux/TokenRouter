package provider

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// process_command.go creates child commands with a deliberately small environment.
// Host credentials and configuration variables are never copied wholesale into
// the child. The process receives only runtime paths, timezone, advisory memory
// settings, plugin identity, and an explicitly configured outbound HTTP proxy.
// go-plugin adds its own ephemeral handshake variables when it starts the command.

// processCommand prepares disposable directories and an isolated child environment.
// Executable and state paths must be absolute. Platform setup assigns Unix
// process groups and Linux parent-death behavior before go-plugin starts the command.
func processCommand(spec plugin.ProcessSpec, options plugin.RuntimeOptions) (*exec.Cmd, error) {
	if !filepath.IsAbs(spec.Executable) || !filepath.IsAbs(spec.StateDir) {
		return nil, fmt.Errorf("plugin executable and state paths must be absolute")
	}
	temporary := filepath.Join(spec.StateDir, "tmp")
	if err := os.MkdirAll(temporary, 0o700); err != nil {
		return nil, err
	}
	command := exec.Command(spec.Executable)
	command.Dir = spec.StateDir
	if runtime.GOOS == "windows" {
		systemRoot := os.Getenv("SystemRoot")
		command.Env = []string{"SystemRoot=" + systemRoot, "PATH=" + filepath.Join(systemRoot, "System32"), "TEMP=" + temporary, "TMP=" + temporary}
	} else {
		command.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + spec.StateDir, "TMPDIR=" + temporary, "LANG=C.UTF-8", "TZ=" + options.Timezone}
	}
	command.Env = append(command.Env, "GOMEMLIMIT="+strconv.Itoa(spec.MemoryMiB)+"MiB", "TR_PLUGIN_ID="+spec.PluginID, "TR_PLUGIN_STATE_DIR="+spec.StateDir)
	if options.HTTPProxy != "" {
		command.Env = append(command.Env, "HTTP_PROXY="+options.HTTPProxy, "HTTPS_PROXY="+options.HTTPProxy, "NO_PROXY=localhost,127.0.0.1")
	}
	configureProcess(command)
	return command, nil
}
