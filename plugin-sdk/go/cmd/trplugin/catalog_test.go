package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
)

// catalog_test.go verifies explicit signing policy during local catalog publication.
// An empty registry avoids external release discovery while exercising complete
// output replacement, detached signature verification, and the protection against
// silently downgrading a signed catalog when workflow credentials are missing.

// TestCatalogBuildRequiresExplicitUnsignedMode publishes signed bytes and then deliberately switches to unsigned output.
// Missing credentials preserve the old generation, while explicit unsigned mode removes its obsolete signature instead of leaving mismatched files.
func TestCatalogBuildRequiresExplicitUnsignedMode(t *testing.T) {
	root := t.TempDir()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(os.Mkdir(filepath.Join(root, "publishers"), 0o755))
	check(os.Mkdir(filepath.Join(root, "plugins"), 0o755))
	out := filepath.Join(root, "dist", "v1")
	args := []string{"--registry", root, "--state", filepath.Join(out, "state.json"), "--out", out}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	check(err)
	t.Setenv("INDEX_SIGNING_KEY", base64.StdEncoding.EncodeToString(private))
	check(buildCommand(args, io.Discard))
	document, err := os.ReadFile(filepath.Join(out, "index.json"))
	check(err)
	signature, err := os.ReadFile(filepath.Join(out, "index.json.sig"))
	check(err)
	check(index.Verify(document, signature, []ed25519.PublicKey{public}))
	t.Setenv("INDEX_SIGNING_KEY", "")
	if err := buildCommand(args, io.Discard); err == nil {
		t.Fatal("missing signing key accepted")
	}
	retained, err := os.ReadFile(filepath.Join(out, "index.json"))
	check(err)
	if string(document) != string(retained) {
		t.Fatal("failed build replaced the accepted document")
	}
	check(buildCommand(append(args, "--unsigned"), io.Discard))
	_, err = os.Stat(filepath.Join(out, "index.json.sig"))
	if !os.IsNotExist(err) {
		t.Fatalf("stale signature remains: %v", err)
	}
	_, err = os.Stat(filepath.Join(out, "state.json"))
	check(err)
}

// TestCatalogBuildPreservesOutputOnOversize checks both signed and explicitly
// unsigned publication. A document exceeding the host budget must leave the
// previously published JSON, signature, and ingestion state byte-for-byte unchanged.
func TestCatalogBuildPreservesOutputOnOversize(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"publishers", "plugins"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(root, "dist", "v1")
	args := []string{"--registry", root, "--state", filepath.Join(out, "state.json"), "--out", out}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("INDEX_SIGNING_KEY", base64.StdEncoding.EncodeToString(private))
	if err := buildCommand(args, io.Discard); err != nil {
		t.Fatal(err)
	}
	previous := make(map[string]string)
	for _, name := range []string{"index.json", "index.json.sig", "state.json"} {
		data, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		previous[name] = string(data)
	}
	oversized := append(args, "--name", strings.Repeat("x", index.MaxDocumentBytes))
	for _, unsigned := range []bool{false, true} {
		attempt := append([]string(nil), oversized...)
		if unsigned {
			attempt = append(attempt, "--unsigned")
		}
		if err := buildCommand(attempt, io.Discard); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized publication result: %v", err)
		}
		for name, want := range previous {
			data, err := os.ReadFile(filepath.Join(out, name))
			if err != nil || string(data) != want {
				t.Fatalf("oversized publication changed %s: %v", name, err)
			}
		}
	}
}
