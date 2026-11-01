"""Verify SDK release review decisions against isolated Git and registry fixtures."""

from __future__ import annotations

import copy
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import sdk_release

# test_sdk_release.py exercises public-release prerequisites without publishing
# packages or contacting a real registry. A temporary bare origin supplies real
# Git history, while npm, Go proxy, and GitHub Release observations are explicit
# fixtures so partial completion and conflicts can be reproduced deterministically.


class SDKReleaseTests(unittest.TestCase):
    """SDKReleaseTests verifies separate baselines, review coverage, and publication recovery."""

    def setUp(self):
        """setUp creates an isolated repository and deterministic public package observations.

        Cleanup restores the original directory after every case. Registry and
        GitHub commands are intercepted, while Git exercises a real local remote.
        """
        original = Path.cwd()
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.addCleanup(os.chdir, original)
        self.root = Path(temporary.name)
        self.work = self.root / "work"
        self.work.mkdir()
        os.chdir(self.work)
        self.run_command = sdk_release.command
        self.run_command("git", "init", "--bare", str(self.root / "origin.git"))
        self.run_command("git", "init", "-b", "main")
        self.run_command("git", "config", "user.name", "SDK release test")
        self.run_command("git", "config", "user.email", "sdk@example.invalid")
        self.run_command("git", "config", "commit.gpgsign", "false")
        self.run_command("git", "config", "tag.gpgsign", "false")
        self.run_command("git", "remote", "add", "origin", str(self.root / "origin.git"))
        self.write("plugin-sdk/go/sdk/runtime.go", "package sdk\n")
        self.web_version("0.1.0")
        self.base = self.commit()
        self.tag("plugin-sdk/go/v0.1.0", self.base)
        self.npm = {"0.1.0": self.metadata("0.1.0", self.base)}
        self.addCleanup(patch.stopall)
        patch.object(sdk_release, "command", side_effect=self.inspect).start()
        patch.object(sdk_release, "repository", return_value="TokenFlux/Test").start()

    def write(self, path, content):
        """write creates a fixture file under this test's temporary repository."""
        target = self.work / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")

    def web_version(self, version):
        """web_version sets the fixture's committed npm identity for a selected release."""
        self.write("plugin-sdk/web/package.json", json.dumps({"name": sdk_release.PACKAGE, "version": version}))

    def commit(self):
        """commit records fixture changes and returns their immutable source identity."""
        self.run_command("git", "add", ".")
        self.run_command("git", "commit", "-m", "test: update SDK fixture")
        return self.run_command("git", "rev-parse", "HEAD")

    def tag(self, name, head):
        """tag publishes a fixture tag only to this test's local bare origin."""
        self.run_command("git", "tag", "-a", name, head, "-m", "SDK fixture release")
        self.run_command("git", "push", "origin", name)

    def metadata(self, version, head):
        """metadata constructs one deterministic npm version record with source and integrity."""
        return {"name": sdk_release.PACKAGE, "version": version, "gitHead": head,
                "dist": {"integrity": "sha512-YXJ0aWZhY3Q="}}

    def inspect(self, *args, **kwargs):
        """inspect intercepts external package observations while preserving actual Git commands.

        Unexpected commands fail the test rather than reaching the network. Exact
        version reads distinguish a missing publication from an empty registry history.
        """
        if args[0] == "npm":
            if args[3] == "versions":
                return json.dumps(list(self.npm))
            version = args[2].removeprefix(sdk_release.PACKAGE + "@")
            if version not in self.npm:
                raise sdk_release.ReleaseError("publication is not visible")
            return json.dumps(self.npm[version])
        if args[0] == "gh":
            self.assertIn("--repo", args)
            self.assertIn("TokenFlux/Test", args)
            return json.dumps({"tagName": args[3], "isDraft": False, "isPrerelease": False})
        if args[0] == "go":
            self.assertEqual(kwargs["env"]["GOPROXY"], "https://proxy.golang.org")
            return json.dumps({"Version": args[-1].split("@")[-1], "Sum": "h1:fixture"})
        self.assertEqual(args[0], "git")
        return self.run_command(*args, **kwargs)

    def reviewed(self, decisions=None):
        """reviewed fills the explicit review decisions used by a test scenario.

        Each changed path receives a rationale; this models the skill's semantic
        review without pretending the release helper can infer behavioral compatibility.
        """
        decisions = decisions or {}
        report = sdk_release.prepare()
        for sdk in ("go", "web"):
            item = report[sdk]
            item["decision"] = decisions.get(sdk, "skip")
            item["version"] = sdk_release.next_version(item["baseline_version"], item["decision"])
            item["reason"] = "Reviewed the source and consumer impact for this scenario."
            for change in item["changes"]:
                change["impact"] = "none" if item["decision"] == "skip" else "publish"
                change["reason"] = "Verified fixture impact against the published source."
        return report

    def publish(self, report, sdk):
        """publish records a completed fixture publication without accessing external services."""
        version = report[sdk]["version"]
        self.tag(f"plugin-sdk/{sdk}/v{version}", report["head"])
        if sdk == "web":
            self.npm[version] = self.metadata(version, report["head"])
            report[sdk]["integrity"] = self.npm[version]["dist"]["integrity"]

    def test_unchanged_sdks_reuse_their_independent_public_baselines(self):
        """An unrelated gateway commit must not cause either SDK to be republished.

        The legacy npm version remains usable through its recorded gitHead without
        manufacturing a retrospective Web source tag.
        """
        self.write("gateway.txt", "new gateway behavior")
        self.commit()
        report = self.reviewed()
        self.assertEqual(report["go"]["changes"], [])
        self.assertEqual(report["web"]["changes"], [])
        sdk_release.check_review(report, report["head"])

    def test_go_only_change_requires_publication_before_gateway_acceptance(self):
        """A Go implementation fix requires its patch release before the gateway.

        Publishing that SDK satisfies the same review on retry while the unchanged
        Web version remains accepted at its original source commit.
        """
        self.write("plugin-sdk/go/sdk/runtime.go", "package sdk\nconst Fixed = true\n")
        self.commit()
        report = self.reviewed({"go": "patch"})
        with self.assertRaises(sdk_release.ReleaseError):
            sdk_release.check_review(report, report["head"])
        self.publish(report, "go")
        sdk_release.check_review(report, report["head"])

    def test_web_only_change_checks_version_source_and_local_artifact_integrity(self):
        """A Web release must match its committed package version and reviewed artifact.

        A registry record with different integrity cannot satisfy the prerequisite,
        even when its version and source tag otherwise match.
        """
        self.web_version("0.1.1")
        self.write("plugin-sdk/web/src/index.ts", "export const fixed = true")
        self.commit()
        report = self.reviewed({"web": "patch"})
        self.publish(report, "web")
        sdk_release.check_review(report, report["head"])
        report["web"]["integrity"] = "sha512-ZGlmZmVyZW50"
        with self.assertRaisesRegex(sdk_release.ReleaseError, "integrity"):
            sdk_release.check_review(report, report["head"])

    def test_partial_dual_publication_resumes_without_bumping_completed_sdk(self):
        """Independent Go minor and Web patch releases can complete at different times.

        The gateway remains blocked after only one publication and accepts the
        original reviewed versions after both have completed.
        """
        self.write("plugin-sdk/go/sdk/runtime.go", "package sdk\nconst Added = true\n")
        self.web_version("0.1.1")
        self.commit()
        report = self.reviewed({"go": "minor", "web": "patch"})
        self.publish(report, "go")
        with self.assertRaises(sdk_release.ReleaseError):
            sdk_release.check_review(report, report["head"])
        self.assertEqual(sdk_release.prepare()["go"]["changes"], [])
        self.publish(report, "web")
        sdk_release.check_review(report, report["head"])

    def test_test_docs_and_formatting_changes_can_be_explicitly_excluded(self):
        """Changes without delivered behavior can be excluded after semantic review.

        The complete file inventory remains present, including a formatting-only
        source edit, so exclusions cannot conceal an unexamined path.
        """
        self.write("plugin-sdk/go/sdk/runtime_test.go", "package sdk\n")
        self.write("plugin-sdk/web/tests/README.md", "Developer test instructions")
        self.write("plugin-sdk/go/sdk/runtime.go", "package sdk\n\n")
        self.commit()
        report = self.reviewed()
        self.assertEqual(len(report["go"]["changes"]), 2)
        sdk_release.check_review(report, report["head"])
        report["go"]["changes"].pop()
        with self.assertRaisesRegex(sdk_release.ReleaseError, "every changed path"):
            sdk_release.check_review(report, report["head"])

    def test_unreviewed_or_contradictory_changes_block_release(self):
        """Pending paths and conflicting skip decisions cannot satisfy the host prerequisite.

        The review must say whether every change affects publication and explain
        why the selected increment follows from those decisions.
        """
        self.write("plugin-sdk/go/sdk/new.go", "package sdk\n")
        self.commit()
        report = sdk_release.prepare()
        with self.assertRaises(sdk_release.ReleaseError):
            sdk_release.check_review(report, report["head"])
        report = self.reviewed()
        report["go"]["changes"][0]["impact"] = "publish"
        with self.assertRaisesRegex(sdk_release.ReleaseError, "contradicts"):
            sdk_release.check_review(report, report["head"])

    def test_source_drift_invalidates_the_review(self):
        """A new target commit invalidates previously reviewed SDK evidence.

        Verification must reject that review before adopting fresh package observations
        or allowing the gateway to use notes for an older source snapshot.
        """
        report = self.reviewed()
        self.write("gateway.txt", "changed after review")
        head = self.commit()
        with self.assertRaisesRegex(sdk_release.ReleaseError, "target commit"):
            sdk_release.check_review(report, head)

    def test_unpublished_package_version_cannot_be_excluded_as_cosmetic(self):
        """A prepared Web version must not be shipped under a skip decision.

        The gateway's package metadata must match the accepted published version,
        even if a reviewer incorrectly excludes the version edit as cosmetic.
        """
        self.web_version("0.1.1")
        self.commit()
        report = self.reviewed()
        with self.assertRaisesRegex(sdk_release.ReleaseError, "Gateway source package version"):
            sdk_release.check_review(report, report["head"])

    def test_newer_remote_sdk_conflicts_with_selected_version(self):
        """A concurrent SDK release must not be silently adopted by an older review.

        Even if it points at the same commit, its higher version changes the
        independently reviewed publication decision and requires reconciliation.
        """
        report = self.reviewed()
        self.tag("plugin-sdk/go/v0.1.1", report["head"])
        with self.assertRaisesRegex(sdk_release.ReleaseError, "conflicts"):
            sdk_release.check_review(report, report["head"])

    def test_missing_or_untraceable_baselines_are_rejected(self):
        """A missing SDK baseline cannot be replaced by a guessed release version.

        An npm record without source identity must likewise stop preparation instead
        of comparing the SDK with the most recent gateway tag.
        """
        self.npm["0.1.0"].pop("gitHead")
        with self.assertRaisesRegex(sdk_release.ReleaseError, "gitHead"):
            sdk_release.prepare()
        self.npm.clear()
        with self.assertRaisesRegex(sdk_release.ReleaseError, "baseline"):
            sdk_release.prepare()

    def test_missing_go_origin_tag_cannot_use_a_local_tag(self):
        """Only origin tags may establish a Go SDK publication baseline.

        A locally retained tag after remote deletion does not prove the public
        module exists and must not enable preparation.
        """
        self.run_command("git", "push", "origin", ":refs/tags/plugin-sdk/go/v0.1.0")
        with self.assertRaisesRegex(sdk_release.ReleaseError, "baseline"):
            sdk_release.prepare()

    def test_published_npm_without_new_source_tag_requires_completion(self):
        """A visible new npm version with an absent source tag is partially complete.

        Preparation reports the incomplete publication rather than selecting another
        version and republishing content already accepted by npm.
        """
        self.web_version("0.1.1")
        head = self.commit()
        self.npm["0.1.1"] = self.metadata("0.1.1", head)
        with self.assertRaisesRegex(sdk_release.ReleaseError, "Finish or reconcile"):
            sdk_release.prepare()

    def test_zero_major_and_exact_increment_are_enforced(self):
        """The SDK version policy never advances to a stable major above zero.

        Review records also cannot choose arbitrary increments that disagree with
        their patch or minor decision.
        """
        for version in ("1.0.0", "0.01.0", "0.1.0-rc.1"):
            with self.subTest(version=version), self.assertRaises(sdk_release.ReleaseError):
                sdk_release.version_key(version)
        self.assertEqual(sdk_release.next_version("0.9.8", "minor"), "0.10.0")
        report = self.reviewed()
        report["go"]["version"] = "1.0.0"
        with self.assertRaises(sdk_release.ReleaseError):
            sdk_release.check_review(report, report["head"])

    def test_registry_errors_and_processing_results_do_not_allow_publication(self):
        """Registry transport failures and unparseable processing responses are not success.

        The helper performs no publication retry and cannot convert those observations
        into an empty or accepted version history.
        """
        with patch.object(sdk_release, "command", side_effect=sdk_release.ReleaseError("registry unavailable")):
            with self.assertRaises(sdk_release.ReleaseError):
                sdk_release.npm_metadata("0.1.0")
        with patch.object(sdk_release, "command", return_value="null"):
            with self.assertRaises(sdk_release.ReleaseError):
                sdk_release.npm_metadata("0.1.0")

    def test_wrong_source_tag_or_package_identity_is_rejected(self):
        """Source tags and npm manifests must identify the same immutable release.

        A source tag on an older commit or a package identity mismatch cannot
        establish compatibility merely because the requested version exists.
        """
        self.web_version("0.1.1")
        head = self.commit()
        self.npm["0.1.1"] = self.metadata("0.1.1", head)
        self.tag("plugin-sdk/web/v0.1.1", self.base)
        with self.assertRaises(sdk_release.ReleaseError):
            sdk_release.prepare()
        bad = copy.deepcopy(self.npm["0.1.0"])
        bad["name"] = "unrelated-package"
        with patch.object(sdk_release, "command", return_value=json.dumps(bad)):
            with self.assertRaises(sdk_release.ReleaseError):
                sdk_release.npm_metadata("0.1.0")


if __name__ == "__main__":
    unittest.main()
