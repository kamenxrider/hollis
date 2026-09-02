#!/usr/bin/env python3
"""Assert a directory contains exactly N .shortcut bridge files.

Used by CI so profile checks do not depend on `ls`/`wc -l` output
formats (padded counts, ls aliases/replacements):

    python3 scripts/count_bridges.py bridges/ 4
"""

import glob
import os
import sys


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: count_bridges.py <dir> <expected-count>", file=sys.stderr)
        return 2
    directory, expected = sys.argv[1], int(sys.argv[2])
    files = sorted(glob.glob(os.path.join(directory, "*.shortcut")))
    if len(files) != expected:
        print(
            f"FAIL: {directory} contains {len(files)} .shortcut file(s), want {expected}: {files}",
            file=sys.stderr,
        )
        return 1
    print(f"OK: {len(files)} .shortcut file(s) in {directory}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
