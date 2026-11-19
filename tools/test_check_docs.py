"""Unit tests for tools/check_docs.py documentation integrity validation."""
from pathlib import Path
import tempfile
import unittest

import check_docs

# test_check_docs.py verifies Project Doc validation against synthetic repository
# trees without depending on live repository documentation state. Each test builds
# a minimal docs/ layout inside a temporary directory and asserts that valid
# structures pass while broken anchors, duplicate IDs, missing fragments, and
# unindexed files fail with actionable diagnostics.


def write_text(base, rel_path, content):
    """write_text creates parent directories and writes UTF-8 content under base."""
    path = base / rel_path
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding='utf-8')
    return path


def create_minimal_repo(root):
    """create_minimal_repo populates a valid Project Doc tree and one source anchor."""
    write_text(
        root,
        'docs/index.md',
        '# Docs\n\n- [Overview](overview.md): summary.\n- [Arch](architecture/index.md): arch.\n',
    )
    write_text(
        root,
        'docs/overview.md',
        '# Overview\n\nSee [Flow](architecture/flow.md#request_flow).\n',
    )
    write_text(
        root,
        'docs/architecture/index.md',
        '# Arch\n\n- [Flow](flow.md): request flow.\n',
    )
    write_text(
        root,
        'docs/architecture/flow.md',
        '# Flow\n\n<a id="request_flow"></a>\n## Request flow\n\nDetails.\n',
    )
    write_text(
        root,
        'backend/main.go',
        '// @project-doc docs/architecture/flow.md#request_flow\npackage main\n',
    )


class CheckDocsTest(unittest.TestCase):
    """CheckDocsTest exercises anchor, link, section ID, and index validations."""

    def test_valid_repository_passes(self):
        """A minimal indexed library with valid links and anchors produces no errors.

        Confirms that code fences and inline code examples containing fake links or
        markers are ignored during validation.
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            create_minimal_repo(root)
            write_text(
                root,
                'docs/architecture/flow.md',
                '# Flow\n\n<a id="request_flow"></a>\n## Request flow\n\n'
                'Inline `@project-doc docs/missing.md#bad` and `[x](missing.md)`.\n\n'
                '```text\n@project-doc docs/missing.md#bad\n[x](missing.md)\n```\n',
            )
            write_text(
                root,
                'frontend/style.css',
                '/* @project-doc docs/architecture/flow.md#request_flow */\n'
                '/* Mentioning @project-doc markers in prose is ignored. */\n',
            )
            self.assertEqual(check_docs.check_repository(root), [])

    def test_broken_anchor_section_and_file_fail(self):
        """Invalid @project-doc targets report both missing files and missing IDs.

        Ensures source anchors cannot drift silently when sections or files move.
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            create_minimal_repo(root)
            write_text(
                root,
                'backend/main.go',
                '// @project-doc docs/architecture/flow.md#missing_id\n'
                '// @project-doc docs/architecture/absent.md#ok\n'
                'package main\n',
            )
            errors = check_docs.check_repository(root)
            self.assertEqual(len(errors), 2)
            self.assertTrue(any('missing_id' in err for err in errors))
            self.assertTrue(any('absent.md' in err for err in errors))

    def test_duplicate_and_invalid_section_ids_fail(self):
        """Duplicate or non-snake_case HTML section IDs are flagged per file.

        Guards anchor stability against colliding IDs and uppercase or hyphenated
        identifiers that violate the Project Doc protocol.
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            create_minimal_repo(root)
            write_text(
                root,
                'docs/architecture/flow.md',
                '# Flow\n\n<a id="request_flow"></a>\n## A\n\n'
                '<a id="request_flow"></a>\n## B\n\n'
                '<a id="Bad-ID"></a>\n## C\n',
            )
            errors = check_docs.check_repository(root)
            self.assertTrue(any('duplicate section id "request_flow"' in err for err in errors))
            self.assertTrue(any('invalid section id "Bad-ID"' in err for err in errors))

    def test_broken_relative_link_and_fragment_fail(self):
        """Relative Markdown links with missing files or unknown fragments fail.

        Confirms heading slugs and explicit HTML IDs are both accepted while
        renamed or deleted target headings are caught.
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            create_minimal_repo(root)
            write_text(
                root,
                'docs/overview.md',
                '# Overview\n\n'
                'OK heading: [Flow](architecture/flow.md#request-flow)\n'
                'Bad frag: [Flow](architecture/flow.md#no_such_section)\n'
                'Bad file: [Missing](architecture/missing.md)\n',
            )
            errors = check_docs.check_repository(root)
            self.assertEqual(len(errors), 2)
            self.assertTrue(any('no_such_section' in err for err in errors))
            self.assertTrue(any('missing.md' in err for err in errors))

    def test_unindexed_project_doc_fails_while_guides_excluded(self):
        """Unlisted docs under category directories fail, while guides/ is exempt.

        Preserves the repository boundary between indexed Project Doc entries and
        external user guides stored under docs/guides/.
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            create_minimal_repo(root)
            write_text(root, 'docs/architecture/orphan.md', '# Orphan\n')
            write_text(root, 'docs/guides/manual.md', '# Manual\n')
            errors = check_docs.check_repository(root)
            self.assertEqual(len(errors), 1)
            self.assertIn('docs/architecture/orphan.md', errors[0])


if __name__ == '__main__':
    unittest.main()
