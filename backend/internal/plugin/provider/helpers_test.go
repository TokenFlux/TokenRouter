package provider

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// packageArchive constructs a complete static archive without depending on local build artifacts.
// The fixture includes a valid contribution and original manifest bytes, allowing
// upload and recovery tests to compare real compressed and extracted identities.
func packageArchive(t *testing.T, version string) []byte {
	t.Helper()
	raw := []byte(`{"schemaVersion":1,"id":"test.plugin","version":"` + version + `","apiVersion":1,"minHostVersion":"0.5.0","name":{"en":"Test"},"description":{"en":"Test plugin"},"publisher":{"name":"Test"},"license":"MIT","frontend":{"entry":"frontend/index.js","pages":[]}}`)
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, entry := range []struct {
		name string // archive-relative file path
		data []byte // exact fixture file bytes
	}{{"plugin.json", raw}, {"frontend/index.js", []byte("globalThis.fixture = true;")}} {
		require.NoError(t, archive.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.data)), Typeflag: tar.TypeReg}))
		_, err := archive.Write(entry.data)
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())
	return buffer.Bytes()
}

// uploadReference converts accepted preview metadata into the storage contract under test.
func uploadReference(upload *plugin.Upload) plugin.PackageRef {
	return plugin.PackageRef{
		PluginID:       upload.Snapshot.Manifest.ID,
		Version:        upload.Snapshot.Version,
		Source:         plugin.SourceUpload,
		Platform:       upload.Platform,
		ManifestSHA256: upload.Snapshot.ManifestSHA256,
		Artifact:       upload.Snapshot.Artifacts[upload.Platform],
		UploadToken:    upload.Token,
	}
}
