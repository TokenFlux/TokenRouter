// Package provider verifies plugin packages, supervises native child processes,
// and serves authenticated host callbacks over the plugin transport.
//
// Start here:
//   - process_supervisor.go owns generation admission, restart, and shutdown.
//   - package_files.go owns verified local packages and retained release files.
//   - host_server.go installs callback permissions, rate limits, and drain tracking.
//
// File groups:
//   - index_client.go fetches bounded signed catalogs under explicit trust policies.
//   - process_*.go handles child commands, platform process rules, resources, and bounded logs.
//   - package_*.go handles uploads, downloads, verification, and development directories.
//   - host_*.go binds capabilities, translates RPCs, observes writes, and tracks actual callback return.
//   - grpc_plugin.go implements the go-plugin handshake and broker service connection.
package provider
