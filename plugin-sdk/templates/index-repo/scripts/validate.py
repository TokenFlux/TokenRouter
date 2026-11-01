"""validate.py selects registration changes for the released submission validator.

Publisher edits also select every plugin under that prefix, so changing an ownership
file alone cannot bypass authorization against the trusted base. The proposed tree
supplies only file paths and YAML data; no script or plugin binary from it executes.
"""

import argparse
import os
from pathlib import Path
import subprocess


def main():
    """Validate changed registrations against trusted publisher ownership and releases.

    The workflow supplies author identity, the exact base commit, and a maintainer
    approval label through environment variables. Arguments always remain separate
    subprocess values, including filenames originating in a proposed repository.
    """
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", required=True)
    parser.add_argument("--proposed", required=True)
    args = parser.parse_args()
    root = Path(args.proposed)
    changed = subprocess.check_output(
        ["git", "-C", str(root), "diff", "--name-only", "--no-renames", "-z", os.environ["BASE_SHA"], "HEAD"],
    ).decode().split("\0")
    selected = set()
    publisher_changes = set()
    for name in changed:
        parts = Path(name).parts
        if len(parts) == 3 and parts[0] == "plugins" and name.endswith(".yaml"):
            selected.add(parts[1] + "." + Path(parts[2]).stem)
        if len(parts) == 2 and parts[0] == "publishers" and name.endswith(".yaml"):
            publisher_changes.add(Path(parts[1]).stem)
    for publisher in publisher_changes:
        for path in (root / "plugins" / publisher).glob("*.yaml"):
            selected.add(publisher + "." + path.stem)
    if not selected:
        if publisher_changes:
            raise SystemExit("Publisher registration changes must include at least one registered plugin.")
        print("No registration changes.")
        return
    command = ["trplugin", "index", "check-submission", "--registry", str(root), "--base-registry", args.base,
               "--actor", os.environ["PR_AUTHOR"], "--plugins", ",".join(sorted(selected))]
    if os.environ.get("APPROVED_PUBLISHER") == "true":
        command.append("--allow-new-publisher")
    subprocess.run(command, check=True)


if __name__ == "__main__":
    main()
