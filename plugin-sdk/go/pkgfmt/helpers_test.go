package pkgfmt

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

// validArchive builds a minimal package with real manifest and asset bytes.
// The synthetic binary is never executed; extraction tests inspect only its
// declared presence, permissions, and integrity metadata.
func validArchive(t *testing.T) ([]byte, []byte) {
	t.Helper()
	raw := []byte(`{"schemaVersion":1,"id":"test.plugin","version":"1.0.0","apiVersion":1,"minHostVersion":"0.5.0","name":{"en":"Test"},"description":{"en":"Test plugin"},"publisher":{"name":"Test"},"license":"MIT","backend":{"executable":"bin/plugin"},"frontend":{"entry":"frontend/index.js","pages":[]}}`)
	var result bytes.Buffer
	gz := gzip.NewWriter(&result)
	w := tar.NewWriter(gz)
	for name, data := range map[string][]byte{"plugin.json": raw, "bin/plugin": []byte("binary"), "frontend/index.js": []byte("console.log('test')")} {
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o777, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes(), raw
}
