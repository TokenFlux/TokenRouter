"""Verify release ranges and annotations in isolated Git repositories with temporary local remotes."""

from __future__ import annotations

import contextlib
import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import tr_release

# test_tr_release.py checks gateway release ranges against isolated local origins.
# Its assertions protect fixed source commits, annotation contents, and the SDK
# prerequisite while keeping all fixture tags and pushes outside the real project.
# No test starts a production release workflow.


class ReleaseRangeTests(unittest.TestCase):
    def setUp(self) -> None:
        self.original_cwd = Path.cwd()
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.addCleanup(os.chdir, self.original_cwd)
        self.root = Path(self.temp.name)
        self.remote = self.root / "origin.git"
        self.work = self.root / "work"
        self.work.mkdir()
        os.chdir(self.work)
        self.git("init", "--bare", str(self.remote))
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Release test")
        self.git("config", "user.email", "release@example.invalid")
        self.git("config", "commit.gpgsign", "false")
        self.git("config", "tag.gpgsign", "false")
        self.git("remote", "add", "origin", str(self.remote))
        self.base = self.commit("Baseline behavior")
        self.git("tag", "-a", "v0.1.9", "-m", "Previous version")
        self.git("push", "origin", "v0.1.9")
        self.target = self.commit("Target behavior")

    def git(self, *args: str) -> str:
        return subprocess.run(
            ["git", *args], check=True, text=True, capture_output=True
        ).stdout.strip()

    def commit(self, content: str) -> str:
        (self.work / "behavior.txt").write_text(content, encoding="utf-8")
        self.git("add", "behavior.txt")
        self.git("commit", "-m", content)
        return self.git("rev-parse", "HEAD")

    def test_local_upstream_tags_do_not_change_origin_release_range(self) -> None:
        # A higher local tag from an upstream fetch must leave the origin release range unchanged.
        self.git("tag", "v9.0.0", self.base)
        tr_release.fetch_origin_tags()
        context = tr_release.build_release_context()
        self.assertEqual(context.previous.name, "v0.1.9")
        self.assertEqual(context.next_tag, "v0.1.10")
        self.assertEqual(context.previous_commit, self.base)
        self.assertEqual(context.head, self.target)
        self.assertEqual(context.commit_count, 1)

    def test_fixed_range_and_annotation_keep_reviewed_target(self) -> None:
        context = tr_release.build_release_context()
        self.commit("New commit after review")
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            tr_release.print_context(context, "prepare")
        self.assertIn(f"change_range={self.base}..{self.target}", output.getvalue())
        notes = "Support credential rotation.\n\n## New features\n\n- Rotate credentials while retaining key settings."
        tr_release.create_annotated_tag(context, notes)
        self.assertEqual(self.git("rev-parse", f"{context.next_tag}^{{commit}}"), self.target)
        self.assertEqual(
            self.git("tag", "--list", "--format=%(contents:body)", context.next_tag),
            notes,
        )

    def test_moved_head_rejects_stale_notes(self) -> None:
        reviewed = tr_release.build_release_context()
        self.commit("New feature")
        current = tr_release.build_release_context()
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            tr_release.ensure_expected_context(current, reviewed.head, reviewed.previous.name)
        self.assertFalse(tr_release.tag_exists_locally(current.next_tag))

    def test_new_origin_release_rejects_stale_notes(self) -> None:
        reviewed = tr_release.build_release_context()
        self.git("tag", "v0.1.10", self.base)
        self.git("push", "origin", "v0.1.10")
        current = tr_release.build_release_context()
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            tr_release.ensure_expected_context(current, reviewed.head, reviewed.previous.name)
        self.assertFalse(tr_release.tag_exists_locally(current.next_tag))

    def test_non_ancestor_origin_tag_is_rejected(self) -> None:
        self.git("checkout", "-b", "other", self.base)
        self.commit("Another release line")
        self.git("tag", "v0.2.0")
        self.git("push", "origin", "v0.2.0")
        self.git("checkout", "main")
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            tr_release.build_release_context()

    def test_local_tags_cannot_replace_missing_origin_tags(self) -> None:
        self.git("push", "origin", ":refs/tags/v0.1.9")
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            tr_release.build_release_context()

    def test_explicit_version_compares_only_origin_versions(self) -> None:
        self.git("tag", "v9.0.0")
        version = tr_release.parse_requested_version("0.2.0")
        context = tr_release.build_release_context(version)
        self.assertEqual(context.next_tag, "v0.2.0")
        tr_release.ensure_expected_context(context, self.target, "v0.1.9")
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            tr_release.build_release_context(tr_release.parse_requested_version("v0.1.9"))

    def test_cli_requires_reviewed_range_before_any_git_operation(self) -> None:
        script = Path(tr_release.__file__).resolve()
        result = subprocess.run(
            ["python3", str(script), "--notes-file", "-"],
            input="Summary\n\n## New features\n\n- New capability\n",
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("--expected-head", result.stderr)
        self.assertFalse(tr_release.tag_exists_locally("v0.1.10"))

    def test_incomplete_sdk_publication_prevents_gateway_tag(self) -> None:
        """An incomplete SDK prerequisite must leave the gateway tag absent.

        The test invokes the actual release entrypoint with a fixed reviewed range
        and injects a failed read-only SDK check before any tag creation.
        """
        notes = self.root / "notes.md"
        notes.write_text("Summary\n\n## Improvements\n\n- Verify SDK publication.\n", encoding="utf-8")
        review = self.root / "sdk.json"
        review.write_text("{}", encoding="utf-8")
        arguments = ["tr_release.py", "--notes-file", str(notes), "--sdk-review", str(review),
                     "--expected-head", self.target, "--expected-previous-tag", "v0.1.9"]
        with patch("sys.argv", arguments), contextlib.redirect_stderr(io.StringIO()):
            with patch.object(tr_release.sdk_release, "check_review", side_effect=tr_release.sdk_release.ReleaseError("not published")):
                self.assertEqual(tr_release.main(), 1)
        self.assertFalse(tr_release.tag_exists_locally("v0.1.10"))

    def test_sdk_verification_cannot_publish_a_subsequently_changed_head(self) -> None:
        """A commit arriving during SDK verification must invalidate the gateway review.

        The entrypoint rechecks its source after the SDK check returns and leaves
        the gateway tag absent instead of publishing the newly arrived commit.
        """
        notes = self.root / "notes.md"
        notes.write_text("Summary\n\n## Improvements\n\n- Verify SDK publication.\n", encoding="utf-8")
        review = self.root / "sdk.json"
        review.write_text("{}", encoding="utf-8")
        arguments = ["tr_release.py", "--notes-file", str(notes), "--sdk-review", str(review),
                     "--expected-head", self.target, "--expected-previous-tag", "v0.1.9"]
        with patch("sys.argv", arguments), contextlib.redirect_stderr(io.StringIO()):
            with patch.object(tr_release.sdk_release, "check_review", side_effect=lambda *_: self.commit("Concurrent edit")):
                with self.assertRaises(SystemExit):
                    tr_release.main()
        self.assertFalse(tr_release.tag_exists_locally("v0.1.10"))


if __name__ == "__main__":
    unittest.main()
