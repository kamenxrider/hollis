"""Provider-free package archive and extraction checks.

The package fixture uses the real packager and synthetic runtime assets. The
only mock is ``authenticate`` inside the test, standing in for the developer
machine's GitHub CLI; the shipping packager still has no provenance bypass.
When the retained review ZIP is present, its checksum and structure are also
checked without rewriting it.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile


SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

import package_plugin as packaging


ROOT = Path(__file__).resolve().parents[2]
CANONICAL_ARCHIVE = ROOT / "dist/plugin-v0.1.0/hollis-plugin-0.1.0.zip"
CANONICAL_SHA256 = (
    "60fda465d04467989809c8fa5400b734d28be853b04e1702766a4742814e54f1"
)


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def zip_mode(info: zipfile.ZipInfo) -> int:
    """Return permission bits stored in a Unix-created ZIP entry."""

    return (info.external_attr >> 16) & 0o7777


def zip_type(info: zipfile.ZipInfo) -> int:
    return stat.S_IFMT(info.external_attr >> 16)


class PackageArchiveTests(unittest.TestCase):
    """Build a real temporary package, then inspect its observable archive."""

    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory(prefix="hollis-package-archive-")
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.fixture_root = self.base / "source"
        self.kit = self.fixture_root / "plugins/hollis"
        shutil.copytree(
            ROOT / "plugins/hollis",
            self.kit,
            ignore=shutil.ignore_patterns("assets"),
        )
        for relative in (
            Path(".claude-plugin/marketplace.json"),
            Path(".agents/plugins/marketplace.json"),
        ):
            destination = self.fixture_root / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / relative, destination)

        self.lock = json.loads((self.kit / "runtime.lock.json").read_text())
        self.assets = self.kit / "assets/runtime"
        self.assets.mkdir(parents=True)
        self.binary = self.assets / self.lock["binary"]["name"]
        self.binary_bytes = b"#!/bin/sh\nprintf 'fixture runtime\\n'\n"
        self.binary.write_bytes(self.binary_bytes)
        self.binary.chmod(0o700)

        self.bridges = self.assets / self.lock["bridges"]["name"]
        self.bridge_bytes = {
            name: f"fixture bridge: {name}\n".encode()
            for name in self.lock["bridge_files"]
        }
        with zipfile.ZipFile(self.bridges, "w") as archive:
            for name, content in self.bridge_bytes.items():
                info = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
                info.create_system = 3
                info.external_attr = (stat.S_IFREG | 0o644) << 16
                info.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(info, content)

        self.lock["binary"]["sha256"] = digest(self.binary)
        self.lock["bridges"]["sha256"] = digest(self.bridges)
        (self.kit / "runtime.lock.json").write_text(
            json.dumps(self.lock, indent=2) + "\n"
        )

        self.output = self.base / "dist"
        mocked_assets: list[str] = []

        def fake_authenticate(path: Path, _lock: dict) -> list[dict]:
            # This marker makes the test-only provenance boundary visible in the
            # embedded receipt. No production command can select this behavior.
            mocked_assets.append(path.name)
            return [{"test_fixture": "mock-provenance", "asset": path.name}]

        with patch.object(
            packaging, "authenticate", side_effect=fake_authenticate
        ):
            self.archive = packaging.package(
                self.fixture_root, self.output, assets=self.assets
            )
        self.mocked_assets = mocked_assets
        self.prefix = "hollis-plugin-0.1.0/"

    def test_packager_authenticates_each_fixture_asset_without_network(self) -> None:
        self.assertEqual(
            self.mocked_assets,
            [self.lock["binary"]["name"], self.lock["bridges"]["name"]],
        )
        adjacent = self.output / "hollis-plugin-0.1.0.runtime-provenance.json"
        receipt = json.loads(adjacent.read_text())
        self.assertTrue(receipt["runtime_provenance_verified"])
        self.assertEqual(
            sorted(receipt["verifications"]),
            sorted(self.mocked_assets),
        )
        for records in receipt["verifications"].values():
            self.assertEqual(records[0]["test_fixture"], "mock-provenance")

    def test_archive_has_exact_source_runtime_content_and_five_bridges(self) -> None:
        source_entries = {
            self.prefix + relative.as_posix(): source.read_bytes()
            for relative, source in packaging.source_files(self.fixture_root)
        }
        runtime_entries = {
            self.prefix + "plugins/hollis/assets/runtime/" + self.binary.name:
            self.binary_bytes,
            self.prefix + "plugins/hollis/assets/runtime/" + self.bridges.name:
            self.bridges.read_bytes(),
        }
        with zipfile.ZipFile(self.archive) as archive:
            self.assertEqual(
                set(archive.namelist()), set(source_entries) | set(runtime_entries) | {
                    self.prefix + "plugins/hollis/assets/runtime/provenance.json"
                },
            )
            for name, content in {**source_entries, **runtime_entries}.items():
                self.assertEqual(archive.read(name), content, name)

            embedded = json.loads(
                archive.read(
                    self.prefix + "plugins/hollis/assets/runtime/provenance.json"
                )
            )
            self.assertEqual(
                embedded["verifications"][self.binary.name][0]["test_fixture"],
                "mock-provenance",
            )

            inner = zipfile.ZipFile(
                archive.open(
                    self.prefix + "plugins/hollis/assets/runtime/" + self.bridges.name
                )
            )
            with inner:
                self.assertEqual(
                    sorted(inner.namelist()), sorted(self.lock["bridge_files"])
                )
                for name, content in self.bridge_bytes.items():
                    self.assertEqual(inner.read(name), content, name)

    def test_archive_modes_are_regular_and_explicit(self) -> None:
        with zipfile.ZipFile(self.archive) as archive:
            for info in archive.infolist():
                self.assertFalse(info.is_dir(), info.filename)
                self.assertEqual(zip_type(info), stat.S_IFREG, info.filename)
                expected = (
                    0o755
                    if info.filename.endswith(".sh")
                    or info.filename.endswith("/hollis-darwin-arm64")
                    else 0o644
                )
                self.assertEqual(zip_mode(info), expected, info.filename)

    def _ditto_extract(self, archive: Path, destination: Path) -> None:
        ditto = Path("/usr/bin/ditto")
        if not ditto.is_file():
            self.skipTest("macOS ditto is unavailable on this runner")
        destination.mkdir(parents=True)
        subprocess.run(
            [str(ditto), "-x", "-k", str(archive), str(destination)],
            check=True,
            capture_output=True,
            text=True,
        )

    def _assert_ditto_archive(self, archive: Path, destination: Path) -> None:
        self._ditto_extract(archive, destination)
        with zipfile.ZipFile(archive) as source_zip:
            root = destination / source_zip.namelist()[0].split("/", 1)[0]
            executable_names = [
                name
                for name in source_zip.namelist()
                if name.endswith(".sh")
                or name.endswith("/hollis-darwin-arm64")
            ]
            for name in executable_names:
                extracted = root / name[len(root.name) + 1 :]
                self.assertEqual(extracted.read_bytes(), source_zip.read(name), name)
                self.assertEqual(extracted.stat().st_mode & 0o777, 0o755, name)

            bridge_name = next(
                name for name in source_zip.namelist() if name.endswith("hollis-bridges.zip")
            )
            bridge_zip = root / bridge_name[len(root.name) + 1 :]
            bridge_destination = destination / "bridges"
            self._ditto_extract(bridge_zip, bridge_destination)
            with zipfile.ZipFile(bridge_zip) as bridge_source:
                self.assertEqual(
                    sorted(bridge_source.namelist()), sorted(self.lock["bridge_files"])
                )
                for name in self.lock["bridge_files"]:
                    extracted = bridge_destination / name
                    self.assertEqual(extracted.read_bytes(), bridge_source.read(name), name)

    def test_ditto_extraction_preserves_fixture_modes_bytes_and_all_bridges(self) -> None:
        self._assert_ditto_archive(self.archive, self.base / "ditto-fixture")

    def test_retained_canonical_archive_hash_and_shape_when_present(self) -> None:
        if not CANONICAL_ARCHIVE.is_file():
            self.skipTest("retained review archive is not present in this checkout")
        self.assertEqual(digest(CANONICAL_ARCHIVE), CANONICAL_SHA256)
        checksum = CANONICAL_ARCHIVE.with_suffix(".sha256")
        self.assertEqual(checksum.read_text().split()[0], CANONICAL_SHA256)
        with zipfile.ZipFile(CANONICAL_ARCHIVE) as archive:
            self.assertTrue(archive.namelist())
            self.assertTrue(all(not info.is_dir() for info in archive.infolist()))
            self.assertIn(
                "hollis-plugin-0.1.0/plugins/hollis/assets/runtime/hollis-darwin-arm64",
                archive.namelist(),
            )
            bridges = zipfile.ZipFile(
                archive.open(
                    "hollis-plugin-0.1.0/plugins/hollis/assets/runtime/hollis-bridges.zip"
                )
            )
            with bridges:
                self.assertEqual(
                    sorted(bridges.namelist()), sorted(self.lock["bridge_files"])
                )

    def test_ditto_extracts_retained_canonical_archive_when_present(self) -> None:
        if not CANONICAL_ARCHIVE.is_file():
            self.skipTest("retained review archive is not present in this checkout")
        self._assert_ditto_archive(CANONICAL_ARCHIVE, self.base / "ditto-canonical")


if __name__ == "__main__":
    unittest.main()
