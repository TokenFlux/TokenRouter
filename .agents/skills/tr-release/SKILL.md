---
name: tr-release
description: Release TokenRouter at a requested version, or the next patch tag when no version is specified, publishing changed Go and Web SDKs first and writing English release notes with a collapsed Chinese translation. Use when the user invokes $tr-release or asks to publish a TokenRouter version, increment its release tag, populate the GitHub Release changelog, or verify the release workflow.
---

# TR Release

## Goal and scope

Publish the requested TokenRouter version, or increment the patch version when none is specified. Write release notes in English and cover every external change from the previous release to the target commit accurately. The workflow passes the annotated tag body to GoReleaser to produce the Release description. Complete the GitHub Release body with English visible by default and a collapsed Simplified Chinese translation at the very bottom, after the installation and documentation sections.

Before publishing the gateway, follow [SDK prerequisites](references/sdk-release.md) to compare Go and Web SDKs with their own latest published versions. Publish only unshipped delivery changes; keep independent `0.x.y` versions, using patch increments for compatible fixes and minor increments for additions or incompatible changes. Complete and verify required SDK publications before creating the gateway tag. The skill executes these steps directly, without an SDK publishing CI workflow.

For requests to revise the skill or review notes, complete that edit or review. Publishing a new version and modifying an existing Release each require the corresponding user request.

## Establish the repository and version range

1. Follow repository instructions from the TokenRouter root. Use the `project-doc` release route first and `humanizer` when writing notes.
2. Confirm the release destination with `git remote get-url origin` and derive `release_repo` from the URL (currently `TokenFlux/TokenRouter`). Pass `--repo "$release_repo"` to every `gh` command; a fork's default `gh` repository may be upstream. If origin differs from the user's intended destination, resolve that before continuing.
3. Complete SDK baseline discovery and change review using [SDK prerequisites](references/sdk-release.md). Make any necessary version preparation commit, push it, and wait for CI before fixing the final release SHA. Preserve completed SDK publication evidence when resuming an interrupted release. Run the preparation command at the final commit; it fetches and checks origin tags. Tag creation and pushing happen during publication:

```bash
python3 .agents/skills/tr-release/scripts/tr_release.py --prepare
# For a specified target version:
python3 .agents/skills/tr-release/scripts/tr_release.py --prepare --version 0.2.0
```

The script selects the highest `vMAJOR.MINOR.PATCH` from origin and ignores tags that exist only locally or upstream. An explicit version must be higher; both `0.2.0` and `v0.2.0` are accepted. Save the output values `previous_tag`, `previous_commit`, `new_tag`, `commit`, `commit_count`, and the fixed-SHA `change_range`.

4. Verify the published Release for `previous_tag` in the target repository. If the tag has no Release, publication has not succeeded, or it is not the actual previous release for that version line, investigate before describing the range as this update. The script already requires the baseline to be an ancestor of the target commit. Existing Release notes provide context; verify this release against its code.
5. Before drafting notes, complete [Change coverage and fact checking](references/release-notes.md). Build a coverage manifest from the fixed SHA range, inspect every commit and the final file diff, and map each change to a release entry or an evidence-based exclusion. Read truncated output in batches until complete. Sampling cannot establish full coverage.

## Write and review the notes

Complete the coverage manifest first, then use the structure below and keep categories with content. For configuration removal, incompatible APIs, billing changes, or migration requirements, place an evidence-based `## Upgrade notes` section before the feature categories.

```markdown
One sentence describing the most important user impact, risk, or reason to upgrade.

## New features

- Describe the capability, who can use it, and the result.

## Improvements

- Describe the prior problem and the effect of the improvement.

## Bug fixes

- Describe the failure scenario, impact, and corrected behavior.
```

Organize entries by capability and use case. Combine successive corrections to one capability while preserving independent capabilities, failures, and upgrade impacts. Prioritize completeness over length, with no fixed entry limit. Avoid compressing independent changes into "UI improvements" or "better stability." Classify additions, changes, and fixes against the previous release's actual behavior.

State scope, defaults, and effects supported by code and tests. Refactoring alone does not establish a performance gain; new tests do not establish a fix; new configuration does not establish that a feature is enabled by default. Investigate uncertain facts. Keep unverified user impacts pending until resolved. Security conclusions, downtime requirements, and rollback limits also need evidence.

GoReleaser supplies the Release title, product introduction, standard installation instructions, and documentation links. Keep the notes focused on changes. Commit SHAs and the internal coverage manifest stay outside the Release body. Exclude tests, mechanical refactoring, formatting, and developer-tool changes after confirming that they have no external impact. Commit prefixes and directories alone are insufficient grounds for exclusion.

Perform the reference document's two-way review: every external change has an entry, and every description traces to evidence at the fixed target commit. Save the final notes as a UTF-8 file and publish from that same file to preserve the reviewed detail. Briefly show the version range and notes before publication. Continue directly when publication is already authorized.

## Chinese translation and final layout

Keep the annotated tag and `--notes-file` in English. After GoReleaser finishes, assemble the final Release body from the verified English description and one translation block. Translate any generated non-English prose into English first, including the Simple Release notice when present. Keep commands, identifiers, version numbers, URLs, and their targets intact.

Translate the complete English description into Simplified Chinese, including the summary, upgrade notes, change categories, and generated installation and documentation sections. Match the order, meaning, conditions, and warnings in both languages. Translate link labels while preserving their targets, and copy executable code blocks verbatim.

Append the translation after all English content using this wrapper:

```html
<details>
<summary>中文翻译</summary>

完整的简体中文译文，保持英文正文的章节和条目顺序。

</details>
```

Leave a blank line before `<details>`, after `</summary>`, and before `</details>` so GitHub renders the Markdown inside. Omit the `open` attribute so the translation starts collapsed. The closing `</details>` is the last non-whitespace content in the Release description. GitHub's [collapsed-section documentation](https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/organizing-information-with-collapsed-sections) describes this syntax.

## Publish and verify

Pass the full SHA and previous tag recorded during preparation. If another release appears remotely or local HEAD changes, the script rejects the stale range and the notes need review again. Take the example variables from preparation output and point `notes_file` at the reviewed notes:

```bash
python3 .agents/skills/tr-release/scripts/tr_release.py \
  --expected-head "$release_commit" \
  --expected-previous-tag "$previous_tag" \
  --sdk-review "$sdk_review_file" \
  --notes-file "$notes_file"
```

When the user specifies a version, add `--version VERSION` to the same command. Otherwise, retain automatic patch incrementing. Use `--notes-file -` to provide the full notes through standard input. The SDK review must cover the final commit and pass public-delivery verification; missing, conflicting, or partially published SDKs prevent gateway tag creation.

1. Wait for the `Release` Actions run triggered by `new_tag` to succeed in the explicit target repository. Verify the tag and commit to distinguish this release from other runs on the same commit.
2. Read and save the generated body with `gh release view "$new_tag" --repo "$release_repo" --json body,url,tagName`. Compare the complete English notes block with the reviewed notes file, allowing only line-ending differences and whitespace at the block's edges. The summary, every category, and every list item must remain intact. If the block is missing, truncated, or different, restore it from the reviewed file while retaining the generated introduction and installation/documentation footer. If block boundaries are unclear, report the specific problem before editing.
3. Follow "Chinese translation and final layout" to assemble and review the full body. On a retry, replace the existing Chinese translation block in place so the body contains exactly one. Save the complete result as a separate UTF-8 file, `release_body_file`. Reread the remote body before editing; if it differs from the saved snapshot, reconcile the intervening edits before continuing. Apply the reviewed body with `gh release edit "$new_tag" --repo "$release_repo" --notes-file "$release_body_file"`. This finalization is part of an authorized publication; keep the existing tag fixed.
4. Reread the remote body and compare it with the complete reviewed file, allowing only line-ending differences and whitespace at the body's edges. Check that English is outside the translation block, the single Chinese block follows all English content, and its `<details>` tag has no `open` attribute. Review translation parity using the reference document. When a browser is available, also check that the translation starts collapsed and expands with rendered headings, lists, links, and code blocks. Report visual rendering as unverified if that check cannot run. If the saved body still differs, report the discrepancy and stop repeated overwrites.
5. Complete the other checks required by the repository's release route. Report unrun and failed checks accurately. Return the previous tag, new tag, commit SHA, Actions result, Release link, and full-body comparison and translation-layout results, plus each SDK's version decision and publication verification.

## Script constraints

- Before publication, tracked files must have no staged or unstaged changes. Untracked files stay outside the release commit. If an upstream exists, the current branch must match it.
- Create an annotated tag at the reviewed commit with title `TokenRouter MAJOR.MINOR.PATCH`, preserve the notes verbatim in its body, and push it to origin.
- Reject existing local or remote tags. Delete the newly created local tag if annotation validation or pushing fails.
- The script checks basic Markdown structure. The evidence manifest and two-way review establish factual accuracy and change coverage.
- A gateway release request includes necessary SDK version preparation commits; unrelated implementation changes require their own scope. Keep existing tags fixed. A successful push is one step in publication; complete release verification afterward.
