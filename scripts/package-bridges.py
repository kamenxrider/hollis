#!/usr/bin/env python3
"""Build the model and image bridges into one release archive from repository source."""

import argparse
from pathlib import Path
import subprocess
import sys
import tempfile
import zipfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("outdir", type=Path)
    args = parser.parse_args()
    args.outdir.mkdir(parents=True, exist_ok=True)
    scripts = Path(__file__).resolve().parent
    bundles = {
        "hollis-bridges.zip": [
            f"AFM Bridge - {tier}.shortcut"
            for tier in ("Cloud", "Cloud Pro", "On-Device", "ChatGPT")
        ] + ["Hollis Image - Reference Input v2.shortcut"],
    }
    for name in bundles:
        if (args.outdir / name).exists() or (args.outdir / name).is_symlink():
            parser.error(f"refusing to overwrite {args.outdir / name}")
    with tempfile.TemporaryDirectory(prefix="hollis-bridges-") as directory:
        root = Path(directory)
        subprocess.run([sys.executable, str(scripts / "make-bridge.py"), "--os", "27", directory], check=True)
        subprocess.run([sys.executable, str(scripts / "make-image-bridge.py"), directory], check=True)
        for archive, names in bundles.items():
            # Explicit membership excludes signed imports and diagnostic Shortcuts.
            with zipfile.ZipFile(args.outdir / archive, "x") as output:
                for name in names:
                    entry = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
                    entry.compress_type = zipfile.ZIP_DEFLATED
                    entry.external_attr = 0o100644 << 16
                    output.writestr(entry, (root / name).read_bytes())
            print(f"wrote {args.outdir / archive}")


if __name__ == "__main__":
    main()
