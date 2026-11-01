# SDK prerequisites for a TokenRouter release

Read this reference before preparing a gateway release. Publishing TokenRouter
includes checking both SDKs and publishing any unshipped delivery changes first.
Requests to edit this skill or its tests do not authorize real publication.
The skill executes SDK publication directly; no SDK release workflow is assumed.

## Establish independent baselines

Follow the repository's release checks, confirm origin and the intended destination,
and fetch origin tags. Keep reports, notes, and package archives outside the tracked
tree. Never read or print npm credential files or signing secrets.

```bash
git fetch origin --tags
python3 .agents/skills/tr-release/scripts/sdk_release.py --prepare > "$sdk_review_file"
```

The JSON report records `head`, `repository`, and separate `go` and `web` objects.
Go uses the highest stable module tag on origin. Web uses the highest stable npm
version, its `gitHead`, and its source tag. The existing Web 0.1.0 has no source tag;
its recorded npm gitHead is the baseline. Do not backfill or republish that version.
Later versions require `plugin-sdk/web/v<version>` at the npm source commit.

These baselines deliberately differ from the preceding gateway tag. An SDK may
already have been published during another task or a partially completed release.
If a baseline is missing, divergent, or untraceable, resolve it before continuing.
An npm version without its required source tag is an incomplete publication: finish
or reconcile that publication, then prepare again instead of inventing a new version.

## Review changes and choose versions

Read the complete diff from each `baseline_commit` to `head` under its SDK directory,
including renamed and deleted files. The helper includes all paths and does not infer
semantic impact. Review the actual delivered code, declarations, dependencies, package
exports, generator outputs, and build results; commit labels alone are insufficient.

For every entry in `changes`, retain `path` and set `impact` to `publish` or `none`,
with a nonempty `reason` explaining the observation. Pure tests, minimal fixtures,
formatting, and development documentation do not require publication. Changes to
development dependencies or lockfiles need review of their effect on published
output; they are not automatically a new runtime dependency. Conversely, changed
licenses, shipped usage contracts, exports, or build output may affect delivery
without changing a runtime function. Inspect mixed files rather than excluding a
whole package.json because some edits concern test scripts.

Set each SDK object's `decision`, `version`, and `reason`:

| Decision | When | Version from 0.x.y |
| --- | --- | --- |
| `skip` | Every change has no delivery impact, or there are no changes | Keep 0.x.y |
| `patch` | Compatible fixes, including applicable dependency or packaging fixes | 0.x.(y+1) |
| `minor` | Added capabilities or incompatible changes | 0.(x+1).0 |

Major must remain zero. Go and Web are independent; never bump one only to match the
other. Describe incompatible changes and migrations explicitly. SDK package versions
do not automatically change manifest `apiVersion` or host-version requirements.

Do not reduce interface removals to cosmetic cleanup. When removing examples,
distinguish documented SDK exports from demonstration packages and establish whether
any supported consumer depends on the removed surface before choosing an increment.

## Prepare the final source commit

Check npm identity and publishing access before creating new SDK tags. Authentication
and any required second factor remain the user's responsibility; retain existing MFA.

For a Web publication, update its package.json version. Update the backend's Go SDK
requirement when publishing Go, retaining the local workspace replacement. Update
main-repository version references where needed. Do not automatically edit the
independent template, check-in, or index repositories, or publish plugin releases.

A requested gateway release authorizes the necessary version preparation commit.
Follow repository formatting, staging, push, and CI rules. Keep unrelated work out
of that commit. Once the preparation is pushed and CI passes, run gateway preparation
and SDK preparation again at the final SHA. Reapply reviewed impact decisions to the
fresh report, inspect added version metadata, and save the completed JSON. Do not
reuse a report with an older head or silently discard new changed paths.

The report's `head` must equal the final gateway release commit. Every new SDK tag
must point there. Finish gateway change coverage and notes for that final range.

## Publish and verify only the required SDKs

Before each mutation, recheck the final HEAD, clean tracked tree, origin destination,
selected version, and relevant remote tag or npm state. A remote tag already at the
reviewed SHA can be reused; a different SHA or a newer concurrent version requires
reconciliation. Never force a tag or replace an existing package version.

For Go, run the SDK checks and host consumer checks, then create and push the annotated
`plugin-sdk/go/v<version>` tag at the reviewed commit. Create its GitHub Release with
`gh release create`, explicit `--repo`, `--verify-tag`, `--latest=false`, and a reviewed
English `--notes-file`. Explain SDK changes and any migration requirements. A prior
successful tag push with a missing Release requires completing that Release, not
another version bump. Preserve the gateway's existing Latest release selection.

For Web, run its frozen install, format/type checks, tests, and build. Create a tarball
outside the repository using `npm pack --json --pack-destination` from plugin-sdk/web.
Inspect its complete file list: include only the intended dist files, license, README,
and package metadata, never tests, fixtures, node_modules, or credentials. Record the
pack result's SHA-512 `integrity` in the report's `web` object. For a skipped Web release,
retain the baseline integrity already present in the report.

Create and push the annotated `plugin-sdk/web/v<version>` source tag at the reviewed
commit. From the same clean plugin-sdk/web directory, run `npm publish . --dry-run
--json --ignore-scripts --access public --registry=https://registry.npmjs.org` and
compare its package integrity with the inspected tarball. Then publish that directory
with the same options except `--dry-run`. Directory publication lets npm populate
gitHead from the checkout; publishing a bare tarball does not add that source metadata.
All builds must already be complete, and `--ignore-scripts` prevents publication
lifecycle scripts from changing the reviewed output. Recheck HEAD and tracked
cleanliness immediately before publication, and verify npm's final integrity and
gitHead afterward. Use the current npm login flow when needed; if npm requests a
security-key or other human verification, stop at that step and resume after it
completes. Never ask for credentials in chat.

Run the read-only acceptance check before gateway publication:

```bash
python3 .agents/skills/tr-release/scripts/sdk_release.py --check "$sdk_review_file"
```

This check verifies source identities and selected versions, the Go SDK's non-draft
Release and public module download, and npm source, package version, and integrity.
The gateway publisher runs the same check again. Only after it succeeds may the
gateway tag be created with `--sdk-review "$sdk_review_file"`.

## Interrupted publication and reporting

Keep the reviewed JSON, tarball, notes, and observed outcomes until the complete
gateway release is verified. If one SDK succeeds and the other fails, retain the
successful release and resume the incomplete one using the same reviewed versions.
Do not remove public tags, unpublish packages, or bump a successful SDK again.

After an npm processing response or unknown outcome, inspect the exact version
before retrying anything. Poll read-only registry observations for up to five minutes
with intervals no longer than 30 seconds. If it remains unresolved, report the state
and stop; neither a timeout nor a temporarily absent version proves that publication
failed. Likewise, inspect origin after an uncertain tag push. A confirmed failed
attempt can be retried only after fixing its cause and verifying no publication exists.

When public SDK delivery is still propagating or authentication is unfinished, leave
the gateway tag absent. On resume, reconcile public observations with the retained
review; generate a new review only when the source or baseline genuinely changed.
Return each SDK's previous and selected versions, publish/skip reasons, release or
registry links, and verification results alongside the normal gateway release report.
