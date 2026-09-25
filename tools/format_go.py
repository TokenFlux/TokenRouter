#!/usr/bin/env python3
"""筛选改动的手写 Go 文件，使用项目 golangci-lint 配置格式化。"""

import argparse
import os
from pathlib import Path
import re
import subprocess
import sys


GENERATED_HEADER = re.compile(rb"(?m)^// Code generated .* DO NOT EDIT\.\r?$")
PACKAGE_LINE = re.compile(rb"(?m)^package[ \t]")


def git_output(root: Path, *args: str) -> bytes:
    # 使用 NUL 分隔文件名，保留路径中的空格和换行。
    return subprocess.check_output(["git", "-C", str(root), *args])


def changed_go_files(root: Path, base: str | None = None) -> list[Path]:
    # 分别收集暂存、未暂存和未跟踪文件，避免两层差异相互抵消。
    paths = set()
    commands = (
        ("diff", "--name-only", "--diff-filter=ACMR", "-z"),
        ("diff", "--cached", "--name-only", "--diff-filter=ACMR", "-z"),
        ("ls-files", "--others", "--exclude-standard", "-z"),
    )
    if base is not None:
        # CI 工作区通常是干净的，必须显式比较基准提交与 HEAD。
        commands = (("diff", "--name-only", "--diff-filter=ACMR", "-z", base, "HEAD", "--"),)
    for args in commands:
        paths.update(filter(None, git_output(root, *args).split(b"\0")))

    files = []
    for raw_path in sorted(paths):
        path = root / os.fsdecode(raw_path)
        if path.suffix != ".go" or path.is_symlink() or not path.is_file():
            continue
        if {"vendor", "node_modules"}.intersection(path.relative_to(root).parts):
            continue
        # 按 Go 标准生成标记过滤，手写的 Ent schema 不会因目录名被排除。
        source = path.read_bytes()
        header = PACKAGE_LINE.split(source, maxsplit=1)[0]
        if GENERATED_HEADER.search(header):
            continue
        files.append(path)
    return files


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="仅检查，发现格式差异时返回失败")
    parser.add_argument("--base", help="比较指定 Git 基准与 HEAD，用于 CI 中已提交的改动")
    args = parser.parse_args()
    root = Path(os.fsdecode(subprocess.check_output(
        ["git", "rev-parse", "--show-toplevel"]
    )).rstrip("\n"))
    files = changed_go_files(root, args.base)
    if not files:
        print("没有需要格式化的手写 Go 文件。")
        return 0

    # 所有格式规则只读取现有配置，工具版本由统一入口校验。
    command = [
        "bash", str(root / "tools/golangci-lint.sh"),
        "fmt", "--config", str(root / "backend/.golangci.yml"),
    ]
    if args.check:
        command.append("--diff")

    different = False
    # 分批传参，避免大量变更超出系统命令行长度限制。
    for start in range(0, len(files), 100):
        result = subprocess.run(
            [*command, *map(str, files[start:start + 100])],
            cwd=root, check=False,
        )
        if result.returncode != 0:
            if not args.check or result.returncode != 1:
                return result.returncode
            different = True
    if args.check and different:
        print("请运行 make fmt-go-changed，并检查、重新暂存格式化后的文件。", file=sys.stderr)
        return 1
    print(f"已{'检查' if args.check else '格式化'} {len(files)} 个手写 Go 文件。")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, subprocess.CalledProcessError) as error:
        print(f"Go 格式化失败：{error}", file=sys.stderr)
        sys.exit(1)
