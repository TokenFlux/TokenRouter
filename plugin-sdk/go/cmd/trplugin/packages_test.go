package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestPackAndValidateRejectsChangedArchive exercises the public CLI dispatch
// with a static plugin and its release checksum file. A changed archive must
// fail validation even before the extractor interprets the damaged contents.
func TestPackAndValidateRejectsChangedArchive(t *testing.T) {
	t.Setenv("GITHUB_REF_NAME", "")
	directory := t.TempDir()
	frontend := filepath.Join(directory, "frontend")
	if err := os.Mkdir(frontend, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schemaVersion":1,"id":"test.plugin","version":"1.0.0","apiVersion":1,"minHostVersion":"0.5.0","name":{"en":"Test"},"description":{"en":"Fixture plugin"},"frontend":{"entry":"frontend/index.js","pages":[]}}`)
	manifestPath := filepath.Join(directory, "plugin.json")
	if err := os.WriteFile(manifestPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frontend, "index.js"), []byte("console.log('fixture')"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(directory, "release")
	var output bytes.Buffer
	if err := run([]string{"pack", "--manifest", manifestPath, "--frontend", frontend, "--out", out}, &output); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(out, "test.plugin-1.0.0-any.tar.gz")
	if err := run([]string{"validate", archive}, &output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(archive, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"validate", archive}, &output); err == nil {
		t.Fatal("changed archive accepted")
	}
}
