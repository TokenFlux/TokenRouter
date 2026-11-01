"""test_validate.py checks selection of publisher-only registration edits.

Disposable Git histories and a recording CLI keep the workflow test offline. The
tests protect ownership review when no plugin file changed and verify that approval
and author data reach the released validator as separate arguments.
"""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class RegistrationSelectionTests(unittest.TestCase):
    """RegistrationSelectionTests exercises trusted workflow selection through real Git diffs."""

    def test_publisher_edit_selects_all_owned_plugins(self):
        """Change only an existing publisher's ownership file in a proposed branch.

        Every plugin under that publisher must reach submission validation, with the
        original author and trusted base retained as explicit independent arguments.
        """
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            proposed = root / "proposed"
            proposed.mkdir()
            subprocess.run(["git", "init", "-q", str(proposed)], check=True)
            for name in ("a", "b"):
                target = proposed / "plugins" / "example" / (name + ".yaml")
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text("id: example." + name + "\n")
            publisher = proposed / "publishers" / "example.yaml"
            publisher.parent.mkdir()
            publisher.write_text("github: [original]\n")
            subprocess.run(["git", "-C", str(proposed), "add", "."], check=True)
            commit = [
                "git", "-C", str(proposed), "-c", "user.name=Fixture",
                "-c", "user.email=fixture@example.invalid", "commit", "-qm",
            ]
            subprocess.run(commit + ["fixture"], check=True)
            base = subprocess.check_output(
                ["git", "-C", str(proposed), "rev-parse", "HEAD"], text=True,
            ).strip()
            publisher.write_text("github: [replacement]\n")
            subprocess.run(["git", "-C", str(proposed), "add", "."], check=True)
            subprocess.run(commit + ["change owner"], check=True)
            executable = root / "trplugin"
            executable.write_text(
                "#!/usr/bin/env python3\nimport json, os, sys\n"
                "open(os.environ['CAPTURE_ARGS'], 'w').write(json.dumps(sys.argv[1:]))\n"
            )
            executable.chmod(0o755)
            captured = root / "arguments.json"
            environment = dict(
                os.environ, BASE_SHA=base, PR_AUTHOR="original",
                APPROVED_PUBLISHER="true", CAPTURE_ARGS=str(captured),
            )
            environment["PATH"] = str(root) + os.pathsep + environment["PATH"]
            subprocess.run(
                ["python3", str(Path(__file__).with_name("validate.py")),
                 "--base", "trusted-base", "--proposed", str(proposed)],
                env=environment, check=True,
            )
            arguments = json.loads(captured.read_text())
            self.assertEqual(arguments[arguments.index("--plugins") + 1], "example.a,example.b")
            self.assertEqual(
                arguments[arguments.index("--base-registry") + 1], "trusted-base",
            )
            self.assertEqual(arguments[arguments.index("--actor") + 1], "original")
            self.assertIn("--allow-new-publisher", arguments)


if __name__ == "__main__":
    unittest.main()
