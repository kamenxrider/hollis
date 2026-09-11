#!/usr/bin/env python3
"""Developer packaging: authenticate pinned runtime assets, then bundle both hosts.

Requires Python 3 and GitHub CLI on the build machine, never on an end user's Mac.
There is deliberately no option to bypass provenance verification.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import tempfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[2]
UA = "OpenAI File Downloader, XaiImageApiFetch/1.0"
TOP_FILES = {"plugin.json", "runtime.lock.json", "README.md", "LICENSE"}
SOURCE_ALLOWLIST = (
    '.claude-plugin/plugin.json',
    '.codex-plugin/plugin.json',
    'LICENSE',
    'README.md',
    'docs/compatibility.md',
    'docs/package.md',
    'docs/privacy.md',
    'docs/setup.md',
    'docs/troubleshooting.md',
    'docs/usage.md',
    'docs/validation.md',
    'examples/demo.md',
    'examples/plan.md',
    'plugin.json',
    'runtime.lock.json',
    'scripts/bridges.sh',
    'scripts/common.sh',
    'scripts/run.sh',
    'scripts/setup.sh',
    'skills/hollis-setup/SKILL.md',
    'skills/hollis/SKILL.md',
    'skills/hollis/references/gstack.md',
    'skills/hollis/references/workflows.md',
)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def read_json(path):
    return json.loads(path.read_text())


def validate_source(root=ROOT):
    kit = root / "plugins/hollis"
    for required in TOP_FILES:
        if not (kit / required).is_file() or (kit / required).is_symlink():
            raise ValueError(f"Missing or unsafe package file: {required}")
    portable = read_json(kit / "plugin.json")
    if portable.get("name") != "hollis" or not re.fullmatch(r"\d+\.\d+\.\d+", portable.get("version", "")):
        raise ValueError("Invalid portable plugin identity")
    for host in (".claude-plugin", ".codex-plugin"):
        manifest = read_json(kit / host / "plugin.json")
        if any(manifest.get(k) != portable[k] for k in ("name", "version")):
            raise ValueError("Host manifests disagree on plugin identity")
    for name in ("hollis", "hollis-setup"):
        skill = (kit / "skills" / name / "SKILL.md").read_text()
        if not skill.startswith("---\n") or f"\nname: {name}\n" not in skill or "\ndescription: " not in skill:
            raise ValueError(f"Invalid shared skill: {name}")
    for marketplace in (root / ".claude-plugin/marketplace.json", root / ".agents/plugins/marketplace.json"):
        data = read_json(marketplace)
        entry = next(p for p in data["plugins"] if p["name"] == "hollis")
        source = entry["source"]
        if (source if isinstance(source, str) else source.get("path")) != "./plugins/hollis":
            raise ValueError("Both host entries must address the same source package")
    lock = read_json(kit / "runtime.lock.json")
    version = lock["version"]
    if lock["schema_version"] not in (1, 2) or not re.fullmatch(r"\d+\.\d+\.\d+", version):
        raise ValueError("Invalid runtime lock")
    if lock["repository"] != "kamenxrider/hollis" or lock["source_ref"] != f"refs/tags/v{version}":
        raise ValueError("Unexpected runtime source")
    if lock["release_url"] != f"https://github.com/kamenxrider/hollis/releases/download/v{version}":
        raise ValueError("Unexpected download origin")
    if not re.fullmatch(r"[a-f0-9]{40}", lock["source_commit"]):
        raise ValueError("A full runtime commit is required")
    if lock["signer_workflow"] != "kamenxrider/hollis/.github/workflows/release.yml":
        raise ValueError("Unexpected signer workflow")
    for key, expected in (("binary", "hollis-darwin-arm64"), ("bridges", "hollis-bridges.zip")):
        if lock[key]["name"] != expected or not re.fullmatch(r"[a-f0-9]{64}", lock[key]["sha256"]):
            raise ValueError("Invalid locked asset")
    if lock["schema_version"] == 2:
        native = lock.get("native", {})
        if (native.get("name") != "hollis-native-darwin-arm64"
                or native.get("protocol_version") != 1
                or not re.fullmatch(r"[a-f0-9]{64}", native.get("sha256", ""))):
            raise ValueError("Invalid locked native helper")
    names = lock["bridge_files"]
    if len(names) != 5 or len(set(names)) != 5 or any(Path(n).name != n or not n.endswith(".shortcut") for n in names):
        raise ValueError("Expected five unique flat bridge filenames")
    return portable, lock


def asset_keys(lock):
    return ("binary", "bridges", "native") if lock["schema_version"] == 2 else ("binary", "bridges")


def check_asset(path, expected):
    if path.is_symlink() or not path.is_file() or digest(path) != expected:
        raise ValueError(f"Locked hash mismatch or unsafe asset: {path.name}")


def check_bridges(path, expected_names):
    with zipfile.ZipFile(path) as archive:
        if sorted(archive.namelist()) != sorted(expected_names):
            raise ValueError("Bridge ZIP must contain exactly the five locked files")
        for entry in archive.infolist():
            if entry.is_dir() or stat.S_ISLNK(entry.external_attr >> 16):
                raise ValueError("Bridge ZIP contains a non-regular entry")
            if not entry.file_size or entry.file_size > 2 * 1024 * 1024:
                raise ValueError("Unexpected bridge size")
            archive.read(entry)  # Verify CRC without extracting.


def authenticate(path, lock):
    result = subprocess.run([
        "gh", "attestation", "verify", str(path), "--repo", lock["repository"],
        "--signer-workflow", lock["signer_workflow"], "--source-ref", lock["source_ref"],
        "--source-digest", lock["source_commit"], "--signer-digest", lock["source_commit"],
        "--deny-self-hosted-runners", "--format", "json",
    ], text=True, capture_output=True, timeout=180)
    if result.returncode:
        # Preserve no arbitrary credential-bearing command stderr in release artifacts.
        raise ValueError(f"GitHub provenance verification failed for {path.name} (exit {result.returncode})")
    verified = json.loads(result.stdout)
    if not verified:
        raise ValueError("Empty provenance verification result")
    return verified


def source_files(root):
    kit = root / "plugins/hollis"
    for filename in SOURCE_ALLOWLIST:
        rel = Path(filename)
        path = kit / rel
        if any((kit / Path(*rel.parts[:i])).is_symlink() for i in range(1, len(rel.parts) + 1)) or not path.is_file():
            raise ValueError(f"Missing or unsafe allowlisted plugin source: {rel}")
        yield Path("plugins/hollis") / rel, path
    for rel in (".claude-plugin/marketplace.json", ".agents/plugins/marketplace.json"):
        yield Path(rel), root / rel


def zip_entry(archive, name, content, executable=False):
    entry = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
    entry.create_system = 3
    entry.external_attr = (stat.S_IFREG | (0o755 if executable else 0o644)) << 16
    entry.compress_type = zipfile.ZIP_DEFLATED
    archive.writestr(entry, content)


def package(root, output, assets=None):
    manifest, lock = validate_source(root)
    if tuple(map(int, manifest["version"].split("."))) >= (0, 2, 0):
        if lock["schema_version"] != 2 or tuple(map(int, lock["version"].split("."))) < (0, 4, 0):
            raise ValueError("Plugin 0.2.0 awaits verified runtime 0.4.0 pins; run refresh_lock.py after runtime publication")
    output.mkdir(parents=True, exist_ok=True)
    name = f"hollis-plugin-{manifest['version']}"
    final = output / f"{name}.zip"
    if final.exists():
        raise ValueError(f"Refusing to overwrite existing package: {final}")
    with tempfile.TemporaryDirectory(prefix=".plugin-build-", dir=output) as temporary:
        stage = Path(temporary)
        receipts = {}
        for key in asset_keys(lock):
            item = lock[key]
            target = stage / item["name"]
            if assets:
                check_asset(assets / item["name"], item["sha256"])
                shutil.copyfile(assets / item["name"], target)
            else:
                req = urllib.request.Request(f"{lock['release_url']}/{item['name']}", headers={"User-Agent": UA})
                with urllib.request.urlopen(req, timeout=120) as response, target.open("wb") as stream:
                    if not response.geturl().startswith("https://"):
                        raise ValueError("Insecure asset redirect")
                    shutil.copyfileobj(response, stream)
            check_asset(target, item["sha256"])
            receipts[item["name"]] = authenticate(target, lock)
        check_bridges(stage / lock["bridges"]["name"], lock["bridge_files"])
        receipt = {
            "runtime_version": lock["version"], "runtime_source_commit": lock["source_commit"],
            "runtime_provenance_verified": True, "verifications": receipts,
            "archive_attestation": "Separate GitHub Actions attestation; not asserted by this receipt.",
        }
        receipt_bytes = (json.dumps(receipt, indent=2, sort_keys=True) + "\n").encode()
        with zipfile.ZipFile(stage / "package.zip", "w") as archive:
            for rel, source in source_files(root):
                zip_entry(archive, f"{name}/{rel.as_posix()}", source.read_bytes(), source.suffix == ".sh")
            for key in asset_keys(lock):
                asset = lock[key]["name"]
                zip_entry(archive, f"{name}/plugins/hollis/assets/runtime/{asset}", (stage / asset).read_bytes(), key in ("binary", "native"))
            zip_entry(archive, f"{name}/plugins/hollis/assets/runtime/provenance.json", receipt_bytes)
        os.replace(stage / "package.zip", final)
        (output / f"{name}.runtime-provenance.json").write_bytes(receipt_bytes)
    (output / f"{name}.sha256").write_text(f"{digest(final)}  {final.name}\n")
    return final


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=ROOT)
    parser.add_argument("--output", type=Path, default=ROOT / "dist/plugin")
    parser.add_argument("--assets-dir", type=Path, help="Cached pinned assets; provenance is still freshly verified")
    parser.add_argument("--validate-only", action="store_true")
    args = parser.parse_args()
    if args.validate_only:
        validate_source(args.root)
        print("Portable/native identities, shared skills and both marketplace entries validated.")
    else:
        print(package(args.root, args.output, args.assets_dir))


if __name__ == "__main__":
    main()
