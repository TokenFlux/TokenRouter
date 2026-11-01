# TokenRouter plugin index

This repository registers public plugin releases and publishes a signed catalog at
`https://raw.githubusercontent.com/TokenFlux/tokenrouter-plugins/dist/v1/index.json`.
The host uses those static files; it does not call the GitHub API at runtime.

## Provision the index

The project owner creates this repository and publishes the Go SDK version selected
in both workflows. Generate a signing key with `trplugin keygen --out index.private-key`,
store its private value as the Actions secret `INDEX_SIGNING_KEY`, and configure its
printed public value in the host's `plugin.OfficialIndexKeys`. Keep the private file
outside Git. Enable Actions and require CODEOWNERS review on the protected main branch.

Build runs after a main-branch push, hourly, or on manual dispatch. It verifies all
new release archives, retains first-accepted hashes in `v1/state.json`, signs the
exact `v1/index.json` bytes, and commits the complete generation to the dist branch.
Rejected releases appear in the Actions summary and remain excluded from the index.

## Fork an independent index

Update CODEOWNERS and publisher registrations for your maintainers. Set `INDEX_NAME`
to your index name and provide your own signing secret. Add the dist URL and public
key in TokenRouter's index settings. If you deliberately want unsigned publication,
set the repository variable `INDEX_UNSIGNED=true` and leave the host public-key field
empty. Missing signing credentials do not automatically select unsigned publication.

An invalid existing signature is rejected even by an unpinned custom source. Switching
to unsigned mode removes the old signature from the published directory. Deleting a
source does not uninstall its accepted plugins or delete their business data.

## Acceptance and moderation

Releases need public, reviewable source; meaningful permission declarations; bounded
financial, entitlement, and email operations; and no obfuscated bundles. Native
releases include Linux amd64 and arm64 archives, an exact `plugin.json` asset, and
`checksums.txt`. See [CONTRIBUTING.md](CONTRIBUTING.md) for registration steps.

Use `yanked` entries with a reason to withdraw a release. Set `delisted: true` to hide
a plugin from new installs. Existing installations remain inspectable. Removing a
release or replacing its assets does not rewrite a previously accepted artifact hash.
