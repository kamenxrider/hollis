#!/usr/bin/env python3
"""Check local Markdown links and GitHub-style heading anchors, offline."""
from collections import Counter
from html import unescape
from pathlib import Path
import re
import sys
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]


def prose(text):
    """Ignore fenced examples, including illustrative absolute file links."""
    lines, fence = [], None
    for line in text.splitlines():
        marker = re.match(r"^\s{0,3}(`{3,}|~{3,})", line)
        if marker:
            if fence is None:
                fence = marker[1][0]
            elif marker[1][0] == fence:
                fence = None
            lines.append("")
        else:
            lines.append(line if fence is None else "")
    return "\n".join(lines)


def anchors(path):
    content = prose(path.read_text())
    result = set(re.findall(r'<a\s+(?:id|name)=["\']([^"\']+)', content))
    counts = Counter()
    for match in re.finditer(r"^ {0,3}#{1,6}\s+(.+?)\s*#*\s*$", content, re.M):
        title = re.sub(r"<[^>]*>", "", match[1])
        title = re.sub(r"\[([^]]+)\]\([^)]*\)", r"\1", title)
        slug = re.sub(r"[^\w\- ]", "", unescape(title).lower()).replace(" ", "-")
        suffix = counts[slug]
        result.add(slug + (f"-{suffix}" if suffix else ""))
        counts[slug] += 1
    return result


def check(paths):
    errors, checked, cache = [], 0, {}
    for path in paths:
        content = prose(path.read_text())
        definitions = {m[1].casefold(): m[2] for m in re.finditer(
            r"^\s*\[([^]]+)\]:\s*<?([^>\s]+)>?", content, re.M)}
        links = [(m.start(), m[1]) for m in re.finditer(
            r"!?\[[^\]\n]*\]\(\s*(<[^>]+>|[^\s)]+)(?:\s+[^)]*)?\)", content)]
        links += [(m.start(), definitions[m[2].casefold()]) for m in re.finditer(
            r"\[([^]\n]+)\]\[([^]\n]+)\]", content) if m[2].casefold() in definitions]
        links += [(m.start(), m[1]) for m in re.finditer(
            r'(?:href|src|srcset)=["\']([^"\']+)["\']', content)]
        for offset, target in links:
            parsed = urlsplit(target.strip("<>"))
            if parsed.scheme or parsed.netloc:
                continue
            destination = (path.parent / unquote(parsed.path)).resolve() if parsed.path else path
            checked += 1
            reason = None
            if not destination.exists():
                reason = "missing target"
            elif parsed.fragment and destination.suffix == ".md":
                if destination not in cache:
                    cache[destination] = anchors(destination)
                if unquote(parsed.fragment) not in cache[destination]:
                    reason = "missing anchor"
            if reason:
                line = content.count("\n", 0, offset) + 1
                errors.append(f"{path.relative_to(ROOT)}:{line}: {reason}: {target}")
    return checked, errors


def main():
    paths = [ROOT / "README.md", ROOT / "CONTRIBUTING.md",
             *sorted((ROOT / "docs").glob("*.md")), ROOT / "docs/releases/v0.7.0.md",
             *sorted((ROOT / "plugins/hollis").rglob("*.md"))]
    checked, errors = check(paths)
    for error in errors:
        print(error)
    print(f"Checked {checked} local links in {len(paths)} documents; {len(errors)} errors.")
    return bool(errors)


if __name__ == "__main__":
    sys.exit(main())
