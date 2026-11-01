#!/usr/bin/env python3
"""Inspect SDK release baselines and verify a reviewed gateway release prerequisite."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

# sdk_release.py binds SDK change reviews to public releases and a fixed host
# commit. Go module tags and npm metadata establish separate baselines because
# either SDK can have been released before the next gateway release. The report
# includes every changed SDK path; the reviewer supplies impact decisions rather
# than treating commit subjects or directory names as proof of compatibility.
#
# Verification rereads remote state after publication. It accepts a previously
# completed SDK only at the reviewed version and source commit, rejects stale
# reviews, and checks public package availability before the gateway tag can be
# created. This script never publishes, retags, or retries a registry mutation.

# These identities select the existing public SDKs; version parsing deliberately
# rejects stable major releases above zero under the project's current policy.
PACKAGE = "@tokenflux/tokenrouter-plugin-sdk"
MODULE = "github.com/TokenFlux/TokenRouter/plugin-sdk/go"
REGISTRY = "https://registry.npmjs.org"
VERSION = re.compile(r"(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)")
SHA = re.compile(r"[0-9a-f]{40}")


class ReleaseError(ValueError):
    """ReleaseError identifies an unresolved prerequisite without allowing publication side effects."""


def command(*args: str, env: dict[str, str] | None = None) -> str:
    """command runs one bounded read command and returns its standard output.

    Nonzero exits and timeouts abort verification. Registry failures never become
    an empty version list that could accidentally authorize a duplicate publication.
    """
    # Bound each read so an unavailable registry cannot stall verification indefinitely.
    try:
        result = subprocess.run(args, text=True, capture_output=True, env=env, timeout=60)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ReleaseError(f"Cannot complete {args[0]} inspection: {error}") from error
    if result.returncode:
        raise ReleaseError(f"{args[0]} inspection failed: {result.stderr.strip() or result.stdout.strip()}")
    return result.stdout.strip()


def version_key(value: str) -> tuple[int, int, int]:
    """version_key parses a stable SDK version while enforcing the zero-major policy.

    Callers pass unprefixed versions from tags or npm. Prerelease identifiers and
    leading zeroes are rejected instead of silently selecting another release line.
    """
    if not isinstance(value, str) or not VERSION.fullmatch(value):
        raise ReleaseError(f"Invalid stable SDK version: {value!r}")
    parts = tuple(int(part) for part in value.split('.'))
    if parts[0] != 0:
        raise ReleaseError("SDK versions must remain 0.x.y")
    return parts


def next_version(previous: str, decision: str) -> str:
    """next_version calculates the selected independent SDK version from an explicit review decision.

    Skip retains the published version, patch advances compatible fixes, and minor
    advances additions or incompatible changes without ever raising the major number.
    """
    _, minor, patch = version_key(previous)
    if decision == "skip":
        return previous
    if decision == "patch":
        return f"0.{minor}.{patch + 1}"
    if decision == "minor":
        return f"0.{minor + 1}.0"
    raise ReleaseError("SDK decision must be skip, patch, or minor")


def repository() -> str:
    """repository resolves the explicit GitHub destination from origin without consulting gh defaults.

    The publishing skill verifies this destination against the user's request.
    Unsupported remotes fail instead of allowing an upstream repository to be selected.
    """
    origin = command("git", "remote", "get-url", "origin")
    match = re.fullmatch(
        r"(?:https://github\.com/|git@github\.com:)([\w.-]+/[\w.-]+?)(?:\.git)?", origin
    )
    if not match:
        raise ReleaseError("Origin must identify the intended GitHub repository")
    return match[1]


def remote_tags() -> dict[str, str]:
    """remote_tags reads origin tag commits, preferring peeled annotated targets over tag objects.

    Only origin supplies release identities. Local tags cannot substitute for a
    missing remote release or override a different commit observed on the server.
    """
    refs = {}
    for line in command("git", "ls-remote", "--tags", "origin").splitlines():
        sha, ref = line.split("\t", 1)
        refs[ref.removeprefix("refs/tags/")] = sha
    return {name: refs.get(name + "^{}", sha) for name, sha in refs.items() if not name.endswith("^{}")}


def commit_at(sha: str, head: str) -> str:
    """commit_at verifies that an exact source commit is available and precedes the release target.

    Missing objects require an explicit fetch before preparation. Divergent history
    is rejected because its diff cannot establish the intended SDK upgrade baseline.
    """
    if not isinstance(sha, str) or not SHA.fullmatch(sha):
        raise ReleaseError("SDK source must be a full Git commit SHA")
    if command("git", "rev-parse", f"{sha}^{{commit}}") != sha:
        raise ReleaseError("SDK source is not a commit")
    command("git", "merge-base", "--is-ancestor", sha, head)
    return sha


def npm_metadata(version: str) -> dict:
    """npm_metadata reads one exact public npm release without using the mutable latest dist-tag.

    The selected record must include source identity and SHA-512 integrity. Missing
    records, processing publications, and registry errors all remain unmet prerequisites.
    """
    version_key(version)
    data = json.loads(command(
        "npm", "view", f"{PACKAGE}@{version}", "--json", f"--registry={REGISTRY}"
    ))
    if not isinstance(data, dict) or data.get("name") != PACKAGE or data.get("version") != version:
        raise ReleaseError("npm did not return the requested SDK release")
    if not isinstance(data.get("gitHead"), str) or not SHA.fullmatch(data["gitHead"]):
        raise ReleaseError("npm SDK release has no verifiable gitHead")
    dist = data.get("dist")
    if not isinstance(dist, dict) or not isinstance(dist.get("integrity"), str):
        raise ReleaseError("npm SDK release has no SHA-512 integrity")
    if not re.fullmatch(r"sha512-[A-Za-z0-9+/]+={0,2}", dist["integrity"]):
        raise ReleaseError("npm SDK release has no SHA-512 integrity")
    return data


def published_baselines(tags: dict[str, str], head: str) -> dict:
    """published_baselines resolves each SDK's latest stable version independently of gateway releases.

    npm's original 0.1.0 publication predates Web source tags and uses its gitHead.
    Every later Web version requires the corresponding source tag; an incomplete
    prior publication must be finished instead of becoming a reason to bump again.

    A registry's latest dist-tag is not an ordering guarantee, so stable versions
    are selected from its complete version list. Every source must precede the
    target commit before its paths can establish an upgrade review.
    """
    go_versions = [
        name.removeprefix("plugin-sdk/go/v") for name in tags
        if name.startswith("plugin-sdk/go/v")
        and VERSION.fullmatch(name.removeprefix("plugin-sdk/go/v"))
    ]
    versions = json.loads(command("npm", "view", PACKAGE, "versions", "--json", f"--registry={REGISTRY}"))
    if not isinstance(versions, list):
        raise ReleaseError("npm did not return SDK version history")
    web_versions = [value for value in versions if isinstance(value, str) and VERSION.fullmatch(value)]
    if not go_versions or not web_versions:
        raise ReleaseError("Both SDKs need an existing published baseline")
    go_version = max(go_versions, key=version_key)
    web_version = max(web_versions, key=version_key)
    metadata = npm_metadata(web_version)
    web_commit = commit_at(metadata["gitHead"], head)
    web_tag = tags.get(f"plugin-sdk/web/v{web_version}")
    # Only the original npm publication predates source tags; later missing tags
    # indicate interrupted publication rather than another legacy version exception.
    if web_tag != web_commit and not (web_version == "0.1.0" and web_tag is None):
        raise ReleaseError("Finish or reconcile the existing npm publication's Web source tag before continuing")
    return {
        "go": {"baseline_version": go_version,
               "baseline_commit": commit_at(tags[f"plugin-sdk/go/v{go_version}"], head)},
        "web": {"baseline_version": web_version, "baseline_commit": web_commit,
                "integrity": metadata["dist"]["integrity"]},
    }


def changed_paths(sdk: str, baseline: str, head: str) -> list[str]:
    """changed_paths lists every SDK path changed between the recorded commits, including deletions.

    Rename detection is disabled so both old and new paths receive review. This
    inventory deliberately retains tests and formatting changes for explicit exclusion.
    """
    raw = command(
        "git", "diff", "--name-only", "--no-renames", "-z", baseline, head,
        "--", f"plugin-sdk/{sdk}/"
    )
    return sorted(path for path in raw.split("\0") if path)


def prepare() -> dict:
    """prepare creates a complete review skeleton for the current commit and public baselines.

    No files, tags, or publications are changed. Paths with changes remain pending
    until a reviewer records their impact and independently chooses each SDK increment.
    """
    head = command("git", "rev-parse", "HEAD")
    baselines = published_baselines(remote_tags(), head)
    result = {"head": head, "repository": repository()}
    for sdk, baseline in baselines.items():
        paths = changed_paths(sdk, baseline["baseline_commit"], head)
        result[sdk] = {
            **baseline, "decision": "pending" if paths else "skip",
            "version": baseline["baseline_version"],
            "reason": "" if paths else "No SDK changes since its published source commit.",
            "changes": [{"path": path, "impact": "pending", "reason": ""} for path in paths],
        }
    return result


def check_review(review: dict, head: str) -> None:
    """check_review verifies that reviewed SDK changes are already published before tagging the host.

    Review decisions bind a baseline, every changed path, the expected version,
    and the final gateway commit. Fresh origin and npm observations must match
    those decisions, including successful portions of an interrupted publication.
    A newer concurrent release invalidates the record instead of being silently
    adopted. Integrity comes from the reviewed local Web tarball for a new release,
    or from the accepted baseline when no Web publication is required.
    """
    if not isinstance(review, dict) or review.get("head") != head:
        raise ReleaseError("SDK review belongs to a different target commit")
    repo = repository()
    if review.get("repository") != repo:
        raise ReleaseError("SDK review belongs to a different origin repository")
    tags = remote_tags()
    latest = published_baselines(tags, head)
    for sdk in ("go", "web"):
        item = review.get(sdk, {})
        if not isinstance(item, dict):
            raise ReleaseError(f"{sdk}: review entry must be an object")
        previous = item.get("baseline_version", "")
        decision = item.get("decision", "")
        selected = next_version(previous, decision)
        if (item.get("version") != selected
                or not isinstance(item.get("reason"), str) or not item["reason"].strip()):
            raise ReleaseError(f"{sdk}: missing rationale or incorrect selected version")
        baseline = commit_at(item.get("baseline_commit", ""), head)
        if sdk == "go":
            original = tags.get(f"plugin-sdk/go/v{previous}")
        else:
            original = npm_metadata(previous)["gitHead"]
        if original != baseline:
            raise ReleaseError(f"{sdk}: published baseline changed")
        changes = item.get("changes", [])
        paths = changed_paths(sdk, baseline, head)
        if not isinstance(changes, list) or any(
            not isinstance(change, dict) or not isinstance(change.get("path"), str)
            for change in changes
        ):
            raise ReleaseError(f"{sdk}: changes must contain path review objects")
        if sorted(change["path"] for change in changes) != paths:
            raise ReleaseError(f"{sdk}: review must cover every changed path exactly once")
        for change in changes:
            if (change.get("impact") not in ("publish", "none")
                    or not isinstance(change.get("reason"), str) or not change["reason"].strip()):
                raise ReleaseError(f"{sdk}: every changed path needs an impact decision and reason")
        has_impact = any(change["impact"] == "publish" for change in changes)
        if has_impact != (decision != "skip"):
            raise ReleaseError(f"{sdk}: publication decision contradicts reviewed changes")
        expected_commit = head if has_impact else baseline
        if (latest[sdk]["baseline_version"] != selected
                or latest[sdk]["baseline_commit"] != expected_commit):
            raise ReleaseError(f"{sdk}: required publication is missing, stale, or conflicts with a newer version")
        if sdk == "go":
            verify_go(selected, repo)
        else:
            package = json.loads(command("git", "show", f"{head}:plugin-sdk/web/package.json"))
            if package.get("version") != selected or package.get("name") != PACKAGE:
                raise ReleaseError("Gateway source package version differs from the reviewed Web SDK")
            verify_web(selected, expected_commit, item.get("integrity", ""))


def verify_go(version: str, repo: str) -> None:
    """verify_go checks the Go SDK Release and downloads its exact public module version.

    A tag alone cannot complete gateway acceptance. The Release must be public
    and the module must be available through the public proxy with checksum verification.
    """
    tag = f"plugin-sdk/go/v{version}"
    release = json.loads(command("gh", "release", "view", tag, "--repo", repo,
                                 "--json", "tagName,isDraft,isPrerelease"))
    if release.get("tagName") != tag or release.get("isDraft") or release.get("isPrerelease"):
        raise ReleaseError("Go SDK Release has not completed stable publication")
    env = {**os.environ, "GOWORK": "off", "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org",
           "GONOPROXY": "none", "GONOSUMDB": "none", "GOPRIVATE": ""}
    module = json.loads(command("go", "mod", "download", "-json", f"{MODULE}@v{version}", env=env))
    if module.get("Error") or module.get("Version") != f"v{version}" or not module.get("Sum"):
        raise ReleaseError("Go SDK module is not publicly available with a checksum")


def verify_web(version: str, commit: str, integrity: str) -> None:
    """verify_web compares npm source and integrity against the reviewed package artifact.

    The committed package version must also match, preventing a source tag from
    claiming a different version than the package actually published by npm.
    """
    metadata = npm_metadata(version)
    package = json.loads(command("git", "show", f"{commit}:plugin-sdk/web/package.json"))
    if metadata["gitHead"] != commit or metadata["dist"]["integrity"] != integrity:
        raise ReleaseError("npm SDK source or package integrity differs from the reviewed artifact")
    if package.get("version") != version or package.get("name") != PACKAGE:
        raise ReleaseError("Published Web source has a different package identity")


def main() -> int:
    """main inspects SDK releases or validates a saved review without performing publication.

    The caller stores preparation output outside tracked files. Authentication or
    publication errors remain visible and require resolution before another check.
    """
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--prepare", action="store_true", help="Print the SDK review skeleton as JSON")
    mode.add_argument("--check", metavar="REVIEW", help="Verify completed SDK publication against a reviewed JSON file")
    args = parser.parse_args()
    try:
        if args.prepare:
            print(json.dumps(prepare(), indent=2, ensure_ascii=False))
        else:
            review = json.loads(Path(args.check).read_text(encoding="utf-8"))
            check_review(review, command("git", "rev-parse", "HEAD"))
            print("SDK publication prerequisites verified")
    except (ReleaseError, OSError, json.JSONDecodeError, TypeError) as error:
        print(f"SDK release check failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
