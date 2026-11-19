#!/usr/bin/env python3
"""Validate Project Doc indexes, Markdown links, and @project-doc code anchors."""
import argparse
from dataclasses import dataclass
import os
from pathlib import Path
import re
import subprocess
import sys

# check_docs.py checks the structural integrity of the repository documentation
# library under docs/ and the @project-doc anchors embedded in source files.
# It enforces four invariants required for safe refactoring: every anchor resolves
# to an indexed library document and unique snake_case section ID, every relative
# Markdown link and fragment resolves inside docs/, every Project Doc file outside
# guides/ and legal/ has exactly one canonical index entry, and section IDs are
# unique within each file.
#
# Parsing intentionally skips fenced code blocks and inline code spans so examples
# inside prose do not generate false positives. Anchor scanning reads tracked files
# from Git when available and falls back to walking the tree in standalone test
# directories. Optional --stats output summarizes paragraph length density for
# readability audits without affecting the exit status.

# ROOT locates repository-relative documentation paths; SKIP_DIRS prevents
# fallback tree scans from descending into dependency or metadata directories.
ROOT = Path(__file__).resolve().parent.parent
SKIP_DIRS = {'.git', 'node_modules', '.agents', '__pycache__', 'dist', 'bin'}
EXCLUDED_DOC_PREFIXES = ('docs/guides/', 'docs/legal/')

# Regular expressions extract stable HTML anchors, Markdown headings, relative
# links, and comment-line @project-doc markers with snake_case identifiers.
ANCHOR_MARKER_RE = re.compile(r'^\s*(?://|#|--|<!--|/\*|\*)\s*@project-doc\s+([^\s`"\')>]+)')
HTML_ID_RE = re.compile(r'<a\s+id="([^"]+)"\s*>\s*</a>')
SECTION_ID_RE = re.compile(r'^[a-z][a-z0-9_]*$')
HEADING_RE = re.compile(r'^#{1,6}\s+(.+?)\s*$')
MD_LINK_RE = re.compile(r'!?\[([^\]]*)\]\(([^)]+)\)')
INLINE_CODE_RE = re.compile(r'`[^`\n]*`')
SLUG_STRIP_RE = re.compile(r'[^\w\s-]', re.UNICODE)
WHITESPACE_RE = re.compile(r'\s+')
BINARY_EXTENSIONS = {
    '.png', '.jpg', '.jpeg', '.gif', '.webp', '.ico', '.svg', '.avif', '.bmp',
    '.woff', '.woff2', '.ttf', '.otf', '.pdf', '.zip', '.gz', '.tar', '.wasm',
}


@dataclass(frozen=True)
class DocInfo:
    """DocInfo stores parsed anchor IDs, heading slugs, and links for one file."""

    # rel_path uses POSIX separators relative to the repository root for matching
    # against @project-doc targets and canonical index entries.
    rel_path: str
    section_ids: tuple[str, ...]  # unique HTML anchor IDs declared in file order
    duplicate_ids: tuple[str, ...]  # repeated HTML anchor IDs reported as errors
    invalid_ids: tuple[str, ...]  # HTML anchor IDs violating snake_case naming rules
    heading_slugs: frozenset[str]  # GitHub-compatible slugs derived from Markdown headings
    links: tuple[tuple[int, str], ...]  # (1-based line number, raw link target) pairs


def heading_slug(text):
    """heading_slug converts a Markdown heading into a GitHub-compatible fragment."""
    cleaned = INLINE_CODE_RE.sub(lambda match: match.group(0).strip('`'), text)
    cleaned = cleaned.strip().lower()
    cleaned = SLUG_STRIP_RE.sub('', cleaned)
    return WHITESPACE_RE.sub('-', cleaned).strip('-')


def iter_non_code_lines(text):
    """iter_non_code_lines yields 1-based line numbers outside fenced code blocks.

    Both backtick and tilde fences are tracked by matching fence character and
    minimum length so nested Markdown examples remain ignored until closed.
    """
    fence_char = None
    fence_len = 0
    for line_no, line in enumerate(text.splitlines(), 1):
        stripped = line.lstrip()
        if fence_char is None:
            if stripped.startswith('```') or stripped.startswith('~~~'):
                marker = stripped[0]
                count = len(stripped) - len(stripped.lstrip(marker))
                if count >= 3:
                    fence_char = marker
                    fence_len = count
                    continue
            yield line_no, line
        else:
            if stripped.startswith(fence_char * fence_len) and not stripped.strip(fence_char):
                fence_char = None
                fence_len = 0


def parse_markdown_doc(root, path):
    """parse_markdown_doc extracts anchors, heading slugs, and links from one doc.

    Duplicate and non-snake_case section IDs are recorded separately so callers can
    report every violation in one pass while still checking incoming references.
    """
    rel_path = path.relative_to(root).as_posix()
    text = path.read_text(encoding='utf-8')
    seen_ids = set()
    section_ids = []
    duplicate_ids = []
    invalid_ids = []
    slug_counts = {}
    heading_slugs = set()
    links = []

    for line_no, line in iter_non_code_lines(text):
        for match in HTML_ID_RE.finditer(line):
            section_id = match.group(1)
            if not SECTION_ID_RE.match(section_id):
                invalid_ids.append(section_id)
            if section_id in seen_ids:
                duplicate_ids.append(section_id)
            else:
                seen_ids.add(section_id)
                section_ids.append(section_id)

        heading_match = HEADING_RE.match(line)
        if heading_match:
            base_slug = heading_slug(heading_match.group(1))
            if base_slug:
                count = slug_counts.get(base_slug, 0)
                slug_counts[base_slug] = count + 1
                slug = base_slug if count == 0 else f'{base_slug}-{count}'
                heading_slugs.add(slug)

        # Strip inline code spans before matching links so syntax examples in prose
        # do not trigger false missing-target errors.
        scrubbed = INLINE_CODE_RE.sub('', line)
        for link_match in MD_LINK_RE.finditer(scrubbed):
            if link_match.group(0).startswith('!'):
                continue
            target = link_match.group(2).strip()
            if target:
                links.append((line_no, target))

    return DocInfo(
        rel_path=rel_path,
        section_ids=tuple(section_ids),
        duplicate_ids=tuple(duplicate_ids),
        invalid_ids=tuple(invalid_ids),
        heading_slugs=frozenset(heading_slugs),
        links=tuple(links),
    )


def list_scan_files(root):
    """list_scan_files returns candidate repository files for @project-doc scanning.

    Git ls-files is preferred so ignored build outputs and untracked local plans do
    not fail repository validation. Non-Git directories fall back to os.walk.
    """
    try:
        output = subprocess.check_output(
            ['git', 'ls-files', '-z'],
            cwd=root,
            stderr=subprocess.DEVNULL,
        )
    except (OSError, subprocess.CalledProcessError):
        # Standalone temporary test trees and source archives lack Git metadata;
        # falling back to os.walk keeps validation working outside a checkout.
        files = []
        for dirpath, dirnames, filenames in os.walk(root):
            dirnames[:] = sorted(d for d in dirnames if d not in SKIP_DIRS)
            for name in sorted(filenames):
                full_path = Path(dirpath) / name
                if full_path.suffix.lower() not in BINARY_EXTENSIONS:
                    files.append(full_path)
        return files

    files = []
    for raw_name in output.split(b'\0'):
        if not raw_name:
            continue
        rel = os.fsdecode(raw_name)
        if rel.startswith('.agents/'):
            continue
        full_path = root / rel
        if full_path.is_file() and full_path.suffix.lower() not in BINARY_EXTENSIONS:
            files.append(full_path)
    return files


def resolve_relative_link(doc_rel_path, href):
    """resolve_relative_link normalizes a Markdown link relative to doc_rel_path.

    External URLs, mailto links, and bare scheme links return None so callers skip
    network resources while still validating same-file fragments and repo paths.
    """
    target = href.split()[0].strip('<>')
    if target.startswith(('http://', 'https://', 'mailto:', 'tel:')):
        return None
    if target.startswith('#'):
        return doc_rel_path, target[1:]
    if '#' in target:
        path_part, fragment = target.split('#', 1)
    else:
        path_part, fragment = target, None
    base_dir = os.path.dirname(doc_rel_path)
    resolved = os.path.normpath(os.path.join(base_dir, path_part)).replace(os.sep, '/')
    return resolved, fragment


def collect_indexed_docs(docs_by_path):
    """collect_indexed_docs maps each Project Doc path to its indexing files.

    Canonical membership comes from docs/index.md for root documents and from each
    category index.md for documents under that category directory.
    """
    members = {}
    root_index = docs_by_path.get('docs/index.md')
    if root_index is None:
        return members

    category_indexes = set()
    for _, href in root_index.links:
        resolved = resolve_relative_link(root_index.rel_path, href)
        if resolved is None:
            continue
        target_path, _ = resolved
        if not target_path.startswith('docs/') or not target_path.endswith('.md'):
            continue
        if target_path.endswith('/index.md') and target_path.count('/') == 2:
            category_indexes.add(target_path)
        elif target_path.count('/') == 1 and target_path != 'docs/index.md':
            members.setdefault(target_path, []).append(root_index.rel_path)

    for cat_index_path in sorted(category_indexes):
        cat_index = docs_by_path.get(cat_index_path)
        if cat_index is None:
            continue
        cat_dir = os.path.dirname(cat_index_path) + '/'
        for _, href in cat_index.links:
            resolved = resolve_relative_link(cat_index_path, href)
            if resolved is None:
                continue
            target_path, _ = resolved
            if target_path.startswith(cat_dir) and target_path != cat_index_path and target_path.endswith('.md'):
                members.setdefault(target_path, []).append(cat_index_path)

    return members


def check_repository(root):
    """check_repository validates docs indexes, links, IDs, and code anchors.

    Returns a sorted list of human-readable error strings. An empty list means all
    checked structural invariants hold across the repository.
    """
    errors = []
    docs_root = root / 'docs'
    if not docs_root.is_dir():
        return ['missing docs/ directory']

    doc_paths = sorted(docs_root.rglob('*.md'))
    docs_by_path = {}
    for path in doc_paths:
        info = parse_markdown_doc(root, path)
        docs_by_path[info.rel_path] = info
        for dup_id in info.duplicate_ids:
            errors.append(f'{info.rel_path}: duplicate section id "{dup_id}"')
        for bad_id in info.invalid_ids:
            errors.append(f'{info.rel_path}: invalid section id "{bad_id}" (must be snake_case)')

    indexed_members = collect_indexed_docs(docs_by_path)

    # Verify canonical Project Doc membership for files outside guides/ and legal/.
    for rel_path in sorted(docs_by_path):
        if rel_path.startswith(EXCLUDED_DOC_PREFIXES):
            continue
        if rel_path == 'docs/index.md' or (rel_path.endswith('/index.md') and rel_path.count('/') == 2):
            continue
        owners = indexed_members.get(rel_path, [])
        if not owners:
            errors.append(f'{rel_path}: not listed in docs/index.md or its category index.md')
        elif len(owners) > 1:
            errors.append(f'{rel_path}: listed in multiple canonical indexes ({", ".join(owners)})')

    # Verify all relative links and fragments inside docs/**/*.md.
    for rel_path, info in sorted(docs_by_path.items()):
        for line_no, href in info.links:
            resolved = resolve_relative_link(rel_path, href)
            if resolved is None:
                continue
            target_path, fragment = resolved
            target_full = root / target_path
            if not target_full.exists():
                errors.append(f'{rel_path}:{line_no}: broken link "{href}" -> "{target_path}"')
                continue
            if fragment and target_path.endswith('.md'):
                target_info = docs_by_path.get(target_path)
                if target_info is None:
                    target_info = parse_markdown_doc(root, target_full)
                if fragment not in target_info.section_ids and fragment.lower() not in target_info.heading_slugs:
                    errors.append(
                        f'{rel_path}:{line_no}: broken fragment "#{fragment}" in "{target_path}"'
                    )

    # Verify every @project-doc anchor in tracked source files.
    for scan_path in list_scan_files(root):
        rel_src = scan_path.relative_to(root).as_posix()
        try:
            content = scan_path.read_text(encoding='utf-8')
        except UnicodeDecodeError:
            # Non-UTF-8 files cannot contain valid text @project-doc markers;
            # skipping them prevents binary fixtures without known extensions from failing checks.
            continue
        # Markdown docs may discuss the @project-doc syntax itself outside code
        # fences; skip inline code spans when scanning Markdown files.
        is_markdown = scan_path.suffix.lower() == '.md'
        lines_iter = iter_non_code_lines(content) if is_markdown else enumerate(content.splitlines(), 1)
        for line_no, line in lines_iter:
            search_line = INLINE_CODE_RE.sub('', line) if is_markdown else line
            for match in ANCHOR_MARKER_RE.finditer(search_line):
                target = match.group(1)
                if '#' not in target:
                    errors.append(f'{rel_src}:{line_no}: @project-doc missing #section_id in "{target}"')
                    continue
                doc_rel, section_id = target.split('#', 1)
                if doc_rel not in docs_by_path:
                    errors.append(f'{rel_src}:{line_no}: @project-doc target file not found "{doc_rel}"')
                    continue
                if doc_rel not in indexed_members:
                    errors.append(f'{rel_src}:{line_no}: @project-doc target "{doc_rel}" is not an indexed Project Doc')
                if section_id not in docs_by_path[doc_rel].section_ids:
                    errors.append(
                        f'{rel_src}:{line_no}: @project-doc section id "{section_id}" not found in "{doc_rel}"'
                    )

    return sorted(errors)


def collect_readability_stats(root):
    """collect_readability_stats reports paragraph density metrics for Project Doc."""
    docs_root = root / 'docs'
    rows = []
    for path in sorted(docs_root.rglob('*.md')):
        rel_path = path.relative_to(root).as_posix()
        if rel_path.startswith(EXCLUDED_DOC_PREFIXES) or rel_path.endswith('index.md'):
            continue
        text = path.read_text(encoding='utf-8')
        lines = text.splitlines()
        paragraphs = []
        current = []
        for _, line in iter_non_code_lines(text):
            stripped = line.strip()
            if not stripped or stripped.startswith(('#', '|', '>', '<a ')):
                if current:
                    paragraphs.append(' '.join(current))
                    current = []
            elif stripped.startswith(('- ', '* ')) or re.match(r'^\d+\.\s', stripped):
                if current:
                    paragraphs.append(' '.join(current))
                    current = []
                paragraphs.append(stripped)
            else:
                current.append(stripped)
        if current:
            paragraphs.append(' '.join(current))
        over_180 = sum(1 for para in paragraphs if len(para) >= 180)
        over_250 = sum(1 for para in paragraphs if len(para) >= 250)
        rows.append((rel_path, len(lines), over_180, over_250))
    return rows


def main(argv=None):
    """main parses CLI flags, checks the repository, and returns a process exit code."""
    parser = argparse.ArgumentParser(description='Validate Project Doc links, indexes, and code anchors.')
    parser.add_argument('--root', type=Path, default=ROOT, help='Repository root directory to validate.')
    parser.add_argument('--stats', action='store_true', help='Print paragraph density stats for Project Doc files.')
    args = parser.parse_args(argv)

    root = args.root.resolve()
    errors = check_repository(root)
    if args.stats:
        rows = collect_readability_stats(root)
        total_lines = sum(row[1] for row in rows)
        total_180 = sum(row[2] for row in rows)
        total_250 = sum(row[3] for row in rows)
        print(f'Project Doc stats: files={len(rows)} lines={total_lines} p>=180={total_180} p>=250={total_250}')
        for rel_path, line_count, over_180, over_250 in sorted(rows, key=lambda row: (-row[3], -row[2], row[0]))[:15]:
            print(f'  {rel_path:52s} lines={line_count:3d} p>=180={over_180:2d} p>=250={over_250:2d}')

    if errors:
        for err in errors:
            print(f'ERROR: {err}', file=sys.stderr)
        print(f'Documentation check failed with {len(errors)} error(s).', file=sys.stderr)
        return 1

    print('Documentation check passed.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
