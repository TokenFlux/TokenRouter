#!/usr/bin/env python3
"""Select pre-push checks from changes relative to a Git base."""
import argparse
import os
from pathlib import Path
import subprocess
import sys
import time

# check_changed.py selects pre-push checks from committed and working-tree changes.
# Git supplies a common comparison base for every pushed reference, while checks
# execute against the current checkout. Go package selection uses existing
# directories because a deleted package cannot be linted or tested directly.
# Frontend static checks cover the full frontend whenever any frontend file changes.
#
# Dependency and configuration changes run the complete frontend test suite.
# Source changes select related tests, with deletions selecting the full suite
# because their dependency graph may already be absent from the working tree.
# Backend locale changes retain their frontend related-test path, and pure Go
# changes require no Node or pnpm installation. Architecture scans rerun for
# backend or policy changes because their inputs include another Go module's files.

# ROOT locates checkout-relative commands; Git's zero object and empty tree identify
# deleted references and repositories without an existing comparison branch.
ROOT = Path(__file__).resolve().parent.parent
ZERO_OID = '0' * 40
EMPTY_TREE = '4b825dc642cb6eb9a060e54bf8d69288fbee4904'


def git(*args):
    """git executes a checkout command and returns its trimmed output."""
    return subprocess.check_output(['git', *args], cwd=ROOT, text=True).strip()


def resolves(rev):
    """resolves reports whether a revision names an available local commit."""
    return subprocess.run(['git', 'rev-parse', '--verify', '--quiet', rev + '^{commit}'],
                          cwd=ROOT, stdout=subprocess.DEVNULL).returncode == 0


def default_base(rev, remote='origin'):
    """default_base finds the comparison ancestor for a local revision.

    The upstream and remote default branches take precedence over remote main.
    Repositories without those references compare against the empty tree.
    """
    for candidate in ('@{upstream}', remote + '/HEAD', remote + '/main'):
        if resolves(candidate):
            return git('merge-base', rev, candidate)
    # A new repository has no prior branch state; comparing its files against the
    # empty tree still includes every tracked change in the initial push.
    return EMPTY_TREE


def push_base(lines, remote):
    """push_base computes one comparison ancestor for all pushed references.

    Deleted local references contribute no checks, and a push containing only
    deletions returns None. Existing remote commits provide the base when they
    resolve locally; new references use their default branch ancestor.

    Multiple reference updates share their oldest common base so the resulting
    diff includes every pushed change. An empty-tree base dominates because an
    initial repository push needs every tracked file checked.
    """
    bases = []
    for line in lines:
        _, local_oid, _, remote_oid = line.split()
        if local_oid == ZERO_OID:
            continue
        if remote_oid != ZERO_OID and resolves(remote_oid):
            bases.append(remote_oid)
        else:
            bases.append(default_base(local_oid, remote))
    if not bases:
        return None
    if EMPTY_TREE in bases:
        return EMPTY_TREE
    # Several pushed branches can have different remote tips. Their common ancestor
    # keeps the single working-tree comparison broad enough to cover each update.
    return git('merge-base', '--octopus', *bases)


def changed_files(base):
    """changed_files lists committed, working-tree, and untracked paths from base.

    Deleted paths remain in the result so callers can select downstream checks.
    Rename detection is disabled to include both sides of a move.
    """
    files = set()
    for args in (['diff', '--name-only', '--no-renames', '-z', base],
                 ['ls-files', '--others', '--exclude-standard', '-z']):
        output = subprocess.check_output(['git', *args], cwd=ROOT)
        files.update(os.fsdecode(name) for name in output.split(b'\0') if name)
    return sorted(files)


def go_test_packages(directories):
    """go_test_packages selects changed packages available under default build tags.

    Integration-only packages remain covered by the caller's lint command and CI.
    An empty directory list returns an empty selection without invoking Go.
    """
    if not directories:
        return []
    output = subprocess.check_output(
        ['go', 'list', '-e', '-f', '{{if or .GoFiles .TestGoFiles .XTestGoFiles}}{{.Dir}}{{end}}',
         *['./' + d for d in directories]],
        cwd=ROOT / 'backend', text=True)
    return ['./' + os.path.relpath(d, ROOT / 'backend') for d in output.split()]


def plan(files, base, go_packages=go_test_packages):
    """plan maps changed paths to ordered working-directory and command pairs.

    Go changes select package checks and both committed-range and working-tree
    formatting. Architecture source and backend changes always rerun the scan
    without cached test results. Frontend changes run all static checks, then related tests for
    existing source paths or the full suite for configuration and deleted files.
    Backend locale paths independently select the frontend tests importing them.

    The plugin SDK is an independent module, so backend tests do not discover
    all of its packages. SDK changes select its own checks and the host consumers;
    frontend SDK tests compile a minimal Vue consumer against the public types.

    Keeping language selection here lets pure Go pushes run without frontend
    tools. Dependency installs precede frontend checks, so those checks inspect
    the dependency graph recorded by the working tree's frozen lockfile.
    """
    commands = [('.', ['git', 'diff', '--check', base])]
    sdk_web = any(f.startswith('plugin-sdk/web/')
                  or f == 'plugin-sdk/prettier.config.mjs' for f in files)
    sdk_go = any(f.startswith('plugin-sdk/go/') for f in files)
    go_files = [f for f in files if f.startswith('backend/') and f.endswith('.go')]
    go_deps = any(f in ('backend/go.mod', 'backend/go.sum', 'plugin-sdk/go/go.mod',
                       'plugin-sdk/go/go.sum') for f in files)
    if go_files or go_deps or sdk_go:
        commands.append(('.', ['python3', 'tools/format_go.py', '--check', '--base', base]))
        commands.append(('.', ['python3', 'tools/format_go.py', '--check']))
    if go_files or go_deps or any(f.startswith('tools/architecture/') for f in files):
        # Architecture reads backend sources outside its Go module. Disable result
        # caching so an unchanged checker cannot conceal new dependency violations.
        commands.append(('tools/architecture', ['env', 'GOWORK=off', 'go', 'test', '-count=1', './...']))
    if go_files or go_deps:
        if go_deps:
            lint_targets = test_packages = ['./...']
        else:
            # Deleted package directories cannot be passed to Go tools. CI checks
            # remaining packages that may still import those removed package paths.
            directories = sorted({os.path.dirname(f)[len('backend/'):] or '.' for f in go_files
                                  if (ROOT / os.path.dirname(f)).is_dir()})
            lint_targets = ['./' + d for d in directories]
            test_packages = go_packages(directories)
        if lint_targets:
            commands.append(('backend', ['bash', '../tools/golangci-lint.sh', 'run', '--build-tags=integration',
                                         '--new-from-rev=' + base, *lint_targets]))
        if test_packages:
            commands.append(('backend', ['go', 'test', *test_packages]))

    if sdk_go:
        commands.append(('plugin-sdk/go', ['bash', '../../tools/golangci-lint.sh', 'run', './...']))
        commands.append(('plugin-sdk/go', ['go', 'test', './...']))
        if not go_files and not go_deps:
            # SDK contracts are consumed by the host runtime and assembly even
            # when no backend file changes, so compile and test those consumers.
            commands.append(('backend', ['go', 'test', './internal/plugin/...', './internal/app']))
    if sdk_web:
        commands.append(('.', ['make', 'prepare-plugin-sdk-web']))
        commands.append(('.', ['pnpm', '--dir', 'plugin-sdk/web', 'run', 'format:check']))
        commands.append(('.', ['pnpm', '--dir', 'plugin-sdk/web', 'run', 'typecheck']))
        commands.append(('.', ['pnpm', '--dir', 'plugin-sdk/web', 'test']))
    if any(f.startswith('.agents/skills/tr-release/scripts/') for f in files):
        commands.append(('.', ['python3', '-m', 'unittest', 'discover', '-s',
                               '.agents/skills/tr-release/scripts', '-p', 'test_*.py']))

    frontend = [f for f in files if f.startswith('frontend/')]
    locale = [f for f in files if f.startswith('backend/internal/pkg/locale/') and f.endswith('.json')]
    if frontend or locale:
        pnpm = ['pnpm', '--dir', 'frontend']
        config = [f for f in frontend if not f.startswith(('frontend/src/', 'frontend/public/'))]
        dependencies = {'frontend/package.json', 'frontend/pnpm-lock.yaml', 'frontend/pnpm-workspace.yaml'}
        if dependencies.intersection(frontend):
            commands.append(('.', [*pnpm, 'install', '--frozen-lockfile']))
        if frontend:
            commands.append(('.', [*pnpm, 'run', 'lint:check']))
            commands.append(('.', [*pnpm, 'run', 'typecheck']))
            commands.append(('.', [*pnpm, 'run', 'test:tools']))
        deleted = any(not (ROOT / f).is_file() for f in frontend)
        if config or deleted:
            # A removed source path may be absent from Vitest's module graph. The
            # complete suite checks consumers that related-file selection could miss.
            commands.append(('.', [*pnpm, 'run', 'test:run']))
        else:
            related = [os.path.relpath(ROOT / f, ROOT / 'frontend') for f in frontend + locale]
            if related:
                commands.append(('.', [*pnpm, 'exec', 'vitest', 'related', '--run', '--passWithNoTests', *related]))

    if any(f.startswith('deploy/') or f.startswith('tools/goreleaser') for f in files):
        commands.append(('.', ['make', 'test-scripts']))
    if any((f.startswith('tools/') and f.endswith('.py')) or f.startswith(('tools/declorder/', '.githooks/'))
           for f in files):
        commands.append(('.', ['make', 'test-tools']))
    return commands


def run(commands):
    """run executes selected commands in order and returns their exit status.

    The first failed command stops the sequence so later output cannot obscure
    its failure. Successful completion reports elapsed wall-clock seconds.
    """
    started = time.monotonic()
    for directory, args in commands:
        print('==> ' + ('' if directory == '.' else '(' + directory + ') ') + ' '.join(args), flush=True)
        code = subprocess.call(args, cwd=ROOT / directory)
        if code:
            print('快检失败。修复后重新推送；确需跳过时使用 git push --no-verify。', file=sys.stderr)
            return code
    print(f'快检通过，用时 {time.monotonic() - started:.0f} 秒。完整检查由 CI 执行。', flush=True)
    return 0


def main():
    """main resolves the requested comparison base and executes selected checks.

    Pre-push input containing only deleted references succeeds without running
    commands. Git and operating-system failures propagate to the command-level handler.
    """
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', help='比较基准，默认取上游分支的共同祖先')
    parser.add_argument('--pre-push', metavar='REMOTE', help='由 pre-push hook 调用，从 stdin 读取待推送引用')
    args = parser.parse_args()
    if args.pre_push:
        base = push_base(sys.stdin.read().splitlines(), args.pre_push)
        if base is None:
            return 0
    else:
        base = args.base or default_base('HEAD')
    files = changed_files(base)
    print(f'基准 {base[:12]}，改动 {len(files)} 个文件', flush=True)
    return run(plan(files, base))


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (OSError, subprocess.CalledProcessError) as error:
        print('快检未完成: ' + str(error), file=sys.stderr)
        sys.exit(1)
