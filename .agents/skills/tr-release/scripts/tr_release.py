#!/usr/bin/env python3
"""Create and push a TokenRouter release tag with release notes."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

import sdk_release

# tr_release.py prepares fixed gateway release ranges and publishes their reviewed
# annotated tags. The previous release comes from origin, while the annotation
# is written from the exact notes file reviewed for the selected commit. Local
# tags and changes made after review cannot silently redefine that release range.
#
# SDK publication is a prerequisite checked through sdk_release before the host
# tag is created. The check rereads public package state; afterward this script
# rechecks the working tree and gateway range because verification may take long
# enough for another local edit or remote release to invalidate the preparation.


TAG_RE = re.compile(r"^v(\d+)\.(\d+)\.(\d+)$")


@dataclass(frozen=True)
class VersionTag:
    name: str
    major: int
    minor: int
    patch: int

    @property
    def key(self) -> tuple[int, int, int]:
        return (self.major, self.minor, self.patch)


@dataclass(frozen=True)
class ReleaseContext:
    previous: VersionTag
    next_tag: str
    head: str
    commit_count: int
    previous_commit: str


def run_git(
    args: list[str],
    check: bool = True,
    input_text: str | None = None,
) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(
        ["git", *args],
        check=False,
        input=input_text,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if check and result.returncode != 0:
        command = "git " + " ".join(args)
        sys.stderr.write(f"{command} failed:\n{result.stderr or result.stdout}\n")
        sys.exit(result.returncode)
    return result


def ensure_git_repo() -> None:
    result = run_git(["rev-parse", "--show-toplevel"], check=False)
    if result.returncode != 0:
        sys.stderr.write("The current directory is not a Git repository. Run from the TokenRouter repository root.\n")
        sys.exit(1)


def ensure_clean_tracked_tree() -> None:
    result = run_git(["status", "--porcelain"], check=True)
    # Untracked files are outside the commit targeted by the release tag.
    dirty_tracked = [
        line
        for line in result.stdout.splitlines()
        if line and not line.startswith("?? ")
    ]
    if dirty_tracked:
        sys.stderr.write("Resolve uncommitted changes to tracked files before publishing:\n")
        sys.stderr.write("\n".join(dirty_tracked) + "\n")
        sys.exit(1)


def fetch_origin_tags() -> None:
    run_git(["fetch", "origin", "--tags"], check=True)


def ensure_branch_aligned_with_upstream() -> None:
    branch = run_git(["branch", "--show-current"], check=True).stdout.strip()
    if not branch:
        return

    # Match upstream before tagging to reject stale or unpushed local commits.
    upstream = run_git(["rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"], check=False)
    if upstream.returncode != 0:
        return

    counts = run_git(["rev-list", "--left-right", "--count", f"{upstream.stdout.strip()}...HEAD"], check=True)
    behind, ahead = (int(part) for part in counts.stdout.split())
    if behind or ahead:
        sys.stderr.write(
            f"Branch {branch} differs from upstream {upstream.stdout.strip()}"
            f" (behind={behind}, ahead={ahead}). Synchronize the branch before publishing.\n"
        )
        sys.exit(1)


def parse_version_tags(raw_tags: str) -> list[VersionTag]:
    tags: list[VersionTag] = []
    for tag in raw_tags.splitlines():
        match = TAG_RE.match(tag.strip())
        if not match:
            continue
        # Use standard semantic-version tags as release candidates.
        major, minor, patch = (int(part) for part in match.groups())
        tags.append(VersionTag(tag.strip(), major, minor, patch))
    return tags


def latest_version_tag() -> VersionTag:
    # Choose the baseline from origin because local tags may include upstream releases.
    result = run_git(["ls-remote", "--tags", "--refs", "origin"], check=True)
    names = [line.split("\t", 1)[1].removeprefix("refs/tags/") for line in result.stdout.splitlines()]
    tags = parse_version_tags("\n".join(names))
    if not tags:
        sys.stderr.write("Origin has no vMAJOR.MINOR.PATCH tag from which to determine the release baseline.\n")
        sys.exit(1)
    return max(tags, key=lambda tag: tag.key)


def parse_requested_version(raw_version: str) -> VersionTag:
    """Parse the requested version and normalize it to a v-prefixed tag."""
    version = raw_version.strip()
    if version.startswith("v"):
        version = version[1:]

    match = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)", version)
    if not match:
        raise argparse.ArgumentTypeError(
            "Version must use MAJOR.MINOR.PATCH, such as 0.2.0, with an optional v prefix"
        )

    major, minor, patch = (int(part) for part in match.groups())
    return VersionTag(f"v{major}.{minor}.{patch}", major, minor, patch)


def tag_exists_locally(tag: str) -> bool:
    result = run_git(["rev-parse", "-q", "--verify", f"refs/tags/{tag}"], check=False)
    return result.returncode == 0


def tag_exists_on_origin(tag: str) -> bool:
    result = run_git(["ls-remote", "--exit-code", "--tags", "origin", tag], check=False)
    return result.returncode == 0


def parse_args() -> argparse.Namespace:
    """parse_args parses preparation and publication arguments with mandatory review inputs.

    Preparation remains read-only with respect to tags. Publication requires both
    the fixed gateway range and a completed SDK review before any Git mutation.
    """
    parser = argparse.ArgumentParser(
        description="Prepare or publish a TokenRouter version with release notes."
    )
    parser.add_argument(
        "--prepare",
        action="store_true",
        help="Run pre-release checks and print the version range without creating or pushing a tag.",
    )
    parser.add_argument(
        "--notes-file",
        metavar="PATH",
        help="Release notes file; use - to read standard input. Required for publication.",
    )
    parser.add_argument(
        "--version",
        metavar="VERSION",
        type=parse_requested_version,
        help="Release version, such as 0.2.0 or v0.2.0. Defaults to the next patch version.",
    )
    parser.add_argument("--expected-head", help="Full commit SHA reviewed for the notes. Required for publication.")
    parser.add_argument("--sdk-review", metavar="PATH", help="Reviewed SDK JSON; required before publishing the gateway tag.")
    parser.add_argument(
        "--expected-previous-tag",
        help="Previous version tag reviewed for the notes. Required for publication.",
    )
    args = parser.parse_args()
    if args.prepare and args.notes_file:
        parser.error("--prepare and --notes-file cannot be used together")
    if not args.prepare and not args.notes_file:
        parser.error("publication requires --notes-file PATH; use - to read standard input")
    if not args.prepare and (not args.expected_head or not args.expected_previous_tag):
        parser.error(
            "publication requires --expected-head and --expected-previous-tag "
            "to match the notes to the release range"
        )
    if not args.prepare and not args.sdk_review:
        parser.error("publication requires --sdk-review PATH with verified SDK release decisions")
    return args


def build_release_context(requested_version: VersionTag | None = None) -> ReleaseContext:
    previous = latest_version_tag()
    next_version = requested_version or VersionTag(
        f"v{previous.major}.{previous.minor}.{previous.patch + 1}",
        previous.major,
        previous.minor,
        previous.patch + 1,
    )
    if requested_version and requested_version.key <= previous.key:
        sys.stderr.write(
            f"Requested version {requested_version.name} must be higher than the latest version {previous.name}.\n"
        )
        raise SystemExit(1)

    next_tag = next_version.name
    head = run_git(["rev-parse", "HEAD"], check=True).stdout.strip()
    previous_commit = run_git(["rev-parse", f"refs/tags/{previous.name}^{{commit}}"], check=True).stdout.strip()
    # A divergent tag requires the caller to establish the intended release line.
    ancestry = run_git(["merge-base", "--is-ancestor", previous_commit, head], check=False)
    if ancestry.returncode != 0:
        sys.stderr.write(
            f"The latest origin version {previous.name} is not an ancestor of the current commit; "
            "the release range is undefined.\n"
        )
        raise SystemExit(1)

    if tag_exists_locally(next_tag):
        sys.stderr.write(f"Local tag {next_tag} already exists; refusing to overwrite it.\n")
        raise SystemExit(1)
    if tag_exists_on_origin(next_tag):
        sys.stderr.write(f"Remote tag {next_tag} already exists; refusing to overwrite it.\n")
        raise SystemExit(1)

    count = run_git(["rev-list", "--count", f"{previous_commit}..{head}"], check=True)
    return ReleaseContext(previous, next_tag, head, int(count.stdout.strip()), previous_commit)


def ensure_expected_context(context: ReleaseContext, expected_head: str, expected_previous_tag: str) -> None:
    """Reject changes to the commit and version baseline reviewed for the notes."""
    if context.head != expected_head or context.previous.name != expected_previous_tag:
        sys.stderr.write(
            "The release range has changed. Run --prepare again and update the notes before publishing.\n"
        )
        raise SystemExit(1)


def print_context(context: ReleaseContext, mode: str) -> None:
    print(f"mode={mode}")
    print(f"previous_tag={context.previous.name}")
    print(f"new_tag={context.next_tag}")
    print(f"commit={context.head}")
    print(f"commit_count={context.commit_count}")
    print(f"previous_commit={context.previous_commit}")
    print(f"change_range={context.previous_commit}..{context.head}")


def read_release_notes(source: str) -> str:
    try:
        raw_notes = sys.stdin.read() if source == "-" else Path(source).read_text(encoding="utf-8")
    except OSError as error:
        sys.stderr.write(f"Failed to read release notes: {error}\n")
        raise SystemExit(1) from error

    notes = raw_notes.strip()
    if not notes:
        sys.stderr.write("Release notes must not be empty.\n")
        raise SystemExit(1)

    lines = notes.splitlines()
    first_heading = next((index for index, line in enumerate(lines) if line.startswith("## ")), None)
    # GoReleaser adds the product, installation, and documentation sections around the change notes.
    if first_heading is None or not "\n".join(lines[:first_heading]).strip():
        sys.stderr.write("Release notes need a summary followed by at least one level-two category heading.\n")
        raise SystemExit(1)
    if not any(line.startswith("- ") for line in lines[first_heading + 1 :]):
        sys.stderr.write("Release notes need at least one list item under their categories.\n")
        raise SystemExit(1)

    forbidden_headings = {"## 📥 Installation", "## 📚 Documentation"}
    if forbidden_headings.intersection(line.strip() for line in lines):
        sys.stderr.write(
            "GoReleaser adds installation and documentation sections; remove them from the change notes.\n"
        )
        raise SystemExit(1)
    return notes


def create_annotated_tag(context: ReleaseContext, notes: str) -> None:
    version = context.next_tag.removeprefix("v")
    tag_message = f"TokenRouter {version}\n\n{notes}\n"
    # Verbatim cleanup preserves Markdown headings that Git would otherwise strip as comments.
    run_git(
        ["tag", "-a", "--cleanup=verbatim", context.next_tag, context.head, "-F", "-"],
        check=True,
        input_text=tag_message,
    )

    # The workflow reads the annotated tag body, so verify it survived Git formatting before pushing.
    tag_type = run_git(["cat-file", "-t", context.next_tag], check=True).stdout.strip()
    tag_body = run_git(
        ["tag", "--list", "--format=%(contents:body)", context.next_tag],
        check=True,
    ).stdout.strip()
    if tag_type != "tag" or tag_body != notes:
        run_git(["tag", "-d", context.next_tag], check=False)
        sys.stderr.write("Annotated tag validation failed. The new local tag has been deleted.\n")
        raise SystemExit(1)


def main() -> int:
    """main publishes only a clean, fixed gateway commit whose SDK prerequisites are verified.

    SDK checks are read-only and may fail on incomplete publication or authentication.
    Such failures leave the gateway tag absent so the operator can resume safely.
    """
    args = parse_args()
    ensure_git_repo()
    ensure_clean_tracked_tree()
    fetch_origin_tags()
    ensure_branch_aligned_with_upstream()

    context = build_release_context(args.version)
    if args.prepare:
        print_context(context, "prepare")
        return 0

    ensure_expected_context(context, args.expected_head, args.expected_previous_tag)
    notes = read_release_notes(args.notes_file)
    try:
        review = json.loads(Path(args.sdk_review).read_text(encoding="utf-8"))
        sdk_release.check_review(review, context.head)
    except (sdk_release.ReleaseError, OSError, json.JSONDecodeError, TypeError) as error:
        sys.stderr.write(f"SDK publication prerequisite failed: {error}\n")
        return 1
    # Public SDK verification may outlive concurrent edits or another release;
    # reject their changed context before creating any local gateway tag.
    ensure_clean_tracked_tree()
    ensure_branch_aligned_with_upstream()
    context = build_release_context(args.version)
    ensure_expected_context(context, args.expected_head, args.expected_previous_tag)
    create_annotated_tag(context, notes)
    push = run_git(["push", "origin", context.next_tag], check=False)
    if push.returncode != 0:
        run_git(["tag", "-d", context.next_tag], check=False)
        sys.stderr.write(f"Pushing {context.next_tag} failed. The new local tag has been deleted:\n")
        sys.stderr.write(push.stderr or push.stdout)
        return push.returncode

    print_context(context, "publish")
    print("remote=origin")
    print("tag_type=annotated")
    print("release_notes=true")
    print("pushed=true")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
