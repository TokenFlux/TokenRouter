#!/usr/bin/env python3
"""创建并推送带变更记录的 TokenRouter release tag。"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path


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
        sys.stderr.write(f"{command} 失败：\n{result.stderr or result.stdout}\n")
        sys.exit(result.returncode)
    return result


def ensure_git_repo() -> None:
    result = run_git(["rev-parse", "--show-toplevel"], check=False)
    if result.returncode != 0:
        sys.stderr.write("当前目录不是 Git 仓库，请在 TokenRouter 仓库根目录运行。\n")
        sys.exit(1)


def ensure_clean_tracked_tree() -> None:
    result = run_git(["status", "--porcelain"], check=True)
    # 只拦截已跟踪文件的变更，未跟踪文件不会进入 release tag 指向的提交。
    dirty_tracked = [
        line
        for line in result.stdout.splitlines()
        if line and not line.startswith("?? ")
    ]
    if dirty_tracked:
        sys.stderr.write("存在已跟踪文件的未提交改动，先处理后再发布：\n")
        sys.stderr.write("\n".join(dirty_tracked) + "\n")
        sys.exit(1)


def fetch_origin_tags() -> None:
    run_git(["fetch", "origin", "--tags"], check=True)


def ensure_branch_aligned_with_upstream() -> None:
    branch = run_git(["branch", "--show-current"], check=True).stdout.strip()
    if not branch:
        return

    # 有 upstream 时必须先对齐，避免把新 tag 打在本地旧提交或未推送提交上。
    upstream = run_git(["rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"], check=False)
    if upstream.returncode != 0:
        return

    counts = run_git(["rev-list", "--left-right", "--count", f"{upstream.stdout.strip()}...HEAD"], check=True)
    behind, ahead = (int(part) for part in counts.stdout.split())
    if behind or ahead:
        sys.stderr.write(
            f"当前分支 {branch} 和 upstream {upstream.stdout.strip()} 未对齐"
            f"（behind={behind}, ahead={ahead}），先同步分支后再发布。\n"
        )
        sys.exit(1)


def parse_version_tags(raw_tags: str) -> list[VersionTag]:
    tags: list[VersionTag] = []
    for tag in raw_tags.splitlines():
        match = TAG_RE.match(tag.strip())
        if not match:
            continue
        # 仅使用标准语义版本 tag，忽略其他历史或临时 tag。
        major, minor, patch = (int(part) for part in match.groups())
        tags.append(VersionTag(tag.strip(), major, minor, patch))
    return tags


def latest_version_tag() -> VersionTag:
    # 本地可能残留 upstream 标签，只有 origin 的标签能作为本仓库的发布基准。
    result = run_git(["ls-remote", "--tags", "--refs", "origin"], check=True)
    names = [line.split("\t", 1)[1].removeprefix("refs/tags/") for line in result.stdout.splitlines()]
    tags = parse_version_tags("\n".join(names))
    if not tags:
        sys.stderr.write("origin 没有形如 vMAJOR.MINOR.PATCH 的 tag，无法确定发布基准。\n")
        sys.exit(1)
    return max(tags, key=lambda tag: tag.key)


def parse_requested_version(raw_version: str) -> VersionTag:
    """解析用户指定的版本，并统一为带 v 前缀的 tag。"""
    version = raw_version.strip()
    if version.startswith("v"):
        version = version[1:]

    match = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)", version)
    if not match:
        raise argparse.ArgumentTypeError(
            "版本必须是 MAJOR.MINOR.PATCH 格式，例如 0.2.0（可带 v 前缀）"
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
    parser = argparse.ArgumentParser(
        description="准备或发布带变更记录的 TokenRouter 版本。"
    )
    parser.add_argument(
        "--prepare",
        action="store_true",
        help="只执行发布前检查并输出版本范围，不创建或推送 tag。",
    )
    parser.add_argument(
        "--notes-file",
        metavar="PATH",
        help="Release 变更记录文件；使用 - 时从标准输入读取。发布时必须提供。",
    )
    parser.add_argument(
        "--version",
        metavar="VERSION",
        type=parse_requested_version,
        help="指定发布版本，例如 0.2.0 或 v0.2.0；省略时自动递增 patch。",
    )
    parser.add_argument("--expected-head", help="编写说明时核对的完整提交 SHA，发布时必须提供。")
    parser.add_argument("--expected-previous-tag", help="编写说明时核对的上一版本 tag，发布时必须提供。")
    args = parser.parse_args()
    if args.prepare and args.notes_file:
        parser.error("--prepare 和 --notes-file 不能同时使用")
    if not args.prepare and not args.notes_file:
        parser.error("发布时必须提供 --notes-file PATH；使用 - 可从标准输入读取")
    if not args.prepare and (not args.expected_head or not args.expected_previous_tag):
        parser.error("发布时必须提供 --expected-head 和 --expected-previous-tag，防止说明与发布范围不一致")
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
            f"指定版本 {requested_version.name} 必须高于当前最高版本 {previous.name}。\n"
        )
        raise SystemExit(1)

    next_tag = next_version.name
    head = run_git(["rev-parse", "HEAD"], check=True).stdout.strip()
    previous_commit = run_git(["rev-parse", f"refs/tags/{previous.name}^{{commit}}"], check=True).stdout.strip()
    # 分叉标签不能直接定义线性升级范围，交由调用方查明目标版本线。
    ancestry = run_git(["merge-base", "--is-ancestor", previous_commit, head], check=False)
    if ancestry.returncode != 0:
        sys.stderr.write(f"origin 最高版本 {previous.name} 不是当前提交的祖先，无法确定发布范围。\n")
        raise SystemExit(1)

    if tag_exists_locally(next_tag):
        sys.stderr.write(f"本地已存在 tag {next_tag}，不会覆盖。\n")
        raise SystemExit(1)
    if tag_exists_on_origin(next_tag):
        sys.stderr.write(f"远端已存在 tag {next_tag}，不会覆盖。\n")
        raise SystemExit(1)

    count = run_git(["rev-list", "--count", f"{previous_commit}..{head}"], check=True)
    return ReleaseContext(previous, next_tag, head, int(count.stdout.strip()), previous_commit)


def ensure_expected_context(context: ReleaseContext, expected_head: str, expected_previous_tag: str) -> None:
    """核对说明采用的提交和版本基准，阻止发布期间的范围漂移。"""
    if context.head != expected_head or context.previous.name != expected_previous_tag:
        sys.stderr.write("发布范围已变化，请重新 prepare 并补全变更说明后再发布。\n")
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
        sys.stderr.write(f"读取 Release 变更记录失败：{error}\n")
        raise SystemExit(1) from error

    notes = raw_notes.strip()
    if not notes:
        sys.stderr.write("Release 变更记录不能为空。\n")
        raise SystemExit(1)

    lines = notes.splitlines()
    first_heading = next((index for index, line in enumerate(lines) if line.startswith("## ")), None)
    # GoReleaser 会补齐产品引言、安装和文档区块；tag 正文只保留摘要与变更分类。
    if first_heading is None or not "\n".join(lines[:first_heading]).strip():
        sys.stderr.write("Release 变更记录必须先写摘要，再写至少一个二级分类标题。\n")
        raise SystemExit(1)
    if not any(line.startswith("- ") for line in lines[first_heading + 1 :]):
        sys.stderr.write("Release 变更记录的分类下必须至少包含一条列表项。\n")
        raise SystemExit(1)

    forbidden_headings = {"## 📥 Installation", "## 📚 Documentation"}
    if forbidden_headings.intersection(line.strip() for line in lines):
        sys.stderr.write("不要在变更记录中重复安装或文档区块，GoReleaser 会自动添加。\n")
        raise SystemExit(1)
    return notes


def create_annotated_tag(context: ReleaseContext, notes: str) -> None:
    version = context.next_tag.removeprefix("v")
    tag_message = f"TokenRouter {version}\n\n{notes}\n"
    # 默认 cleanup 会把 Markdown 标题当作 Git 注释删除，必须逐字保留完整正文。
    run_git(
        ["tag", "-a", "--cleanup=verbatim", context.next_tag, context.head, "-F", "-"],
        check=True,
        input_text=tag_message,
    )

    # workflow 读取 annotated tag 的正文；推送前确认正文没有被 Git 格式化丢失。
    tag_type = run_git(["cat-file", "-t", context.next_tag], check=True).stdout.strip()
    tag_body = run_git(
        ["tag", "--list", "--format=%(contents:body)", context.next_tag],
        check=True,
    ).stdout.strip()
    if tag_type != "tag" or tag_body != notes:
        run_git(["tag", "-d", context.next_tag], check=False)
        sys.stderr.write("annotated tag 校验失败，已删除本地新 tag。\n")
        raise SystemExit(1)


def main() -> int:
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
    create_annotated_tag(context, notes)
    push = run_git(["push", "origin", context.next_tag], check=False)
    if push.returncode != 0:
        run_git(["tag", "-d", context.next_tag], check=False)
        sys.stderr.write(f"推送 {context.next_tag} 失败，已删除本地新 tag：\n")
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
