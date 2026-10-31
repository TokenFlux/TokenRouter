package main

import (
	"fmt"
	"io"
	"os"
)

// main.go dispatches the plugin publisher's command-line tools. Commands share
// manifest, catalog, and package validation with the host rather than keeping
// separate acceptance rules. Diagnostics go to stderr, and successful commands
// print only their public artifact paths, digests, or public keys.

// main executes one publisher command and reports failures through its exit status.
func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run dispatches one command while leaving process exit ownership to main.
// Tests supply their own arguments and output writer. Missing or unknown
// commands return usage errors without modifying files.
func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: trplugin validate|pack|keygen|index")
	}
	switch args[0] {
	case "validate":
		return validateCommand(args[1:], output)
	case "pack":
		return packCommand(args[1:], output)
	case "keygen":
		return keygenCommand(args[1:], output)
	case "index":
		return indexCommand(args[1:], output)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
