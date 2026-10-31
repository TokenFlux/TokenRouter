package index

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// TestVerifyAuthenticatesOriginalBytes exercises signing, key rotation, and
// tampering detection. Equivalent JSON with different whitespace must fail
// because signatures authenticate downloaded bytes rather than parsed meaning.
func TestVerifyAuthenticatesOriginalBytes(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schemaVersion":1}`)
	sig, err := Sign(data, priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(data, sig, []ed25519.PublicKey{other, pub}); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		data, sig []byte
		keys      []ed25519.PublicKey
	}{
		{append(data, ' '), sig, []ed25519.PublicKey{pub}},
		{data, sig, []ed25519.PublicKey{other}},
		{data, []byte("invalid"), []ed25519.PublicKey{pub}},
		{data, sig, nil},
		{data, sig, []ed25519.PublicKey{{1}}},
	} {
		if err := Verify(check.data, check.sig, check.keys); err == nil {
			t.Fatal("untrusted document accepted")
		}
	}
	if _, err := Sign(data, nil); err == nil {
		t.Fatal("invalid private key accepted")
	}
}

// TestParseValidatesCatalogAndEmbeddedManifest exercises the public catalog
// decoder with real nested declarations. Bad identities, unsafe URLs, unknown
// fields, and missing integrity values must be rejected before package selection.
func TestParseValidatesCatalogAndEmbeddedManifest(t *testing.T) {
	m := &manifest.Manifest{SchemaVersion: 1, ID: "test.plugin", Version: "1.0.0", APIVersion: 1, MinHostVersion: "0.5.0", Name: manifest.LocalizedText{"en": "Test"}, Description: manifest.LocalizedText{"en": "Test plugin"}, Frontend: &manifest.Frontend{Entry: "frontend/index.js"}}
	d := Document{SchemaVersion: 1, Name: "Test", GeneratedAt: time.Now().UTC(), Plugins: []Plugin{{ID: m.ID, Repository: "https://github.com/example/plugin", Categories: []string{"tools"}, Versions: []Version{{Version: m.Version, PublishedAt: time.Now().UTC(), Manifest: m, ManifestSHA256: strings.Repeat("a", 64), Artifacts: map[string]Artifact{"any": {URL: "https://example.com/plugin.tar.gz", SHA256: strings.Repeat("b", 64), Size: 100}}}}}}}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{`"schemaVersion":1`, `"schemaVersion":2`}, {`"id":"test.plugin"`, `"id":"Bad.plugin"`}, {`https://example.com/plugin.tar.gz`, `http://example.com/plugin.tar.gz`}, {strings.Repeat("a", 64), "invalid"}, {`"minHostVersion":"0.5.0"`, `"minHostVersion":"0.5"`}, {`"size":100`, `"size":0`}, {`"name":"Test"`, `"name":"Test","unknown":true`}} {
		bad := strings.ReplaceAll(string(data), pair[0], pair[1])
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("accepted replacement %q", pair)
		}
	}
}
