# Plugin distribution seeds

`index-repo/` contains the registration, verification, signing, and publication
workflow for `TokenFlux/tokenrouter-plugins`. `plugin-repo/` contains a standalone
check-in implementation for the template repository; replace its example identity
before publishing the official `tokenflux.checkin` example.

The project owner creates the official index, plugin template, and check-in GitHub
repositories, provisions the index signing key, and publishes the Go/npm SDKs.
No private signing material belongs in these directories. Add the public key to
`backend/internal/plugin/indexes.go` only after the owner supplies its public value.
An empty official key list makes the host reject built-in index verification.

Initialize and commit dependency lockfiles after selecting published SDK versions.
A local build using workspace replacements checks source behavior but cannot prove
that an external template clone or GitHub Actions run can resolve unpublished SDKs.
