"""Portable discovery and dispatch need no host-specific manifest or network."""
import copy
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

import portable

ROOT = Path(__file__).resolve().parents[2]
KIT = ROOT / "plugins/hollis"


class PortableTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.kit = self.base / "portable package"
        shutil.copytree(KIT, self.kit)
        self.manifest = json.loads((self.kit / "plugin.json").read_text())

    def test_generic_discovery_and_script_resolution_without_host_adapters(self):
        for host in (".claude-plugin", ".codex-plugin"):
            (self.kit / host / "plugin.json").unlink()
            (self.kit / host).rmdir()
        _, skills = portable.validate_plugin(self.kit)
        self.assertEqual([p.parent.name for p in skills], ["hollis", "hollis-setup"])
        env = dict(os.environ, HOME=str(self.base), HOLLIS_STATE_DIR=str(self.base / "state"))
        env.pop("PLUGIN_ROOT", None)
        env.pop("PLUGIN_DATA", None)
        for skill in skills:
            self.assertEqual((skill.parent / "../..").resolve(), self.kit.resolve())
            self.assertIn('dirname "$SKILL_FILE"', skill.read_text())
            result = subprocess.run(
                ["bash", "-c", 'SKILL_FILE=$1; KIT="$(cd "$(dirname "$SKILL_FILE")/../.." && pwd -P)"; '
                 'bash "$KIT/scripts/setup.sh" help', "portable", str(skill)],
                cwd=self.base, env=env, text=True, capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("Usage: setup.sh", result.stdout)
        self.assertFalse((self.base / "state").exists())
        self.assertFalse((self.base / "Library").exists())

    def test_manifest_closed_fields_types_and_names(self):
        portable.validate_manifest(self.manifest)
        for name in ("a", "acme.tools", "lint3r", "a" * 64):
            portable.validate_manifest(dict(self.manifest, name=name))
        for name in ("", "A", "-bad", "bad-", "a--b", "a..b", "a_b", "a" * 65):
            with self.subTest(name=name), self.assertRaises(ValueError):
                portable.validate_manifest(dict(self.manifest, name=name))
        for key, value in (("skills", "./skills"), ("version", 4), ("description", None),
                           ("author", {"name": 1}), ("author", {"unknown": "x"}),
                           ("keywords", [1]), ("extensions", []),
                           ("extensions", {"claude": {}}),
                           ("extensions", {"com.example.client": "bad"})):
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                portable.validate_manifest(dict(self.manifest, **{key: value}))
        for missing in ("$schema", "name"):
            manifest = copy.deepcopy(self.manifest)
            del manifest[missing]
            with self.assertRaises(ValueError):
                portable.validate_manifest(manifest)
        portable.validate_manifest(dict(self.manifest, extensions={"com.example.client": {"custom": True}}))

    def test_skill_frontmatter_bounds_and_directory_identity(self):
        path = self.kit / "skills/hollis/SKILL.md"
        for name, description in (("Wrong", "ok"), ("other", "ok"), ("hollis", ""),
                                  ("hollis", "x" * 1025), ("hol--lis", "ok")):
            path.write_text(f"---\nname: {name}\ndescription: {description}\n---\nBody\n")
            with self.subTest(name=name, length=len(description)), self.assertRaises(ValueError):
                portable.skill_metadata(path)
        path.write_text("---\nname: hollis\ndescription: " + "x" * 1024 + "\n---\nBody\n")
        portable.skill_metadata(path)

    def test_discovery_is_not_recursive(self):
        nested = self.kit / "skills/container/deeper"
        nested.mkdir(parents=True)
        (nested / "SKILL.md").write_text("not a discovered skill")
        self.assertEqual(len(portable.validate_plugin(self.kit)[1]), 2)

    def test_package_paths_cannot_escape(self):
        outside = self.base / "outside"
        outside.write_text("outside package")
        for rel in ("plugin.json", "skills/hollis/SKILL.md", "scripts/run.sh"):
            path = self.kit / rel
            content = path.read_bytes()
            path.unlink()
            path.symlink_to(outside)
            with self.subTest(path=rel), self.assertRaisesRegex(ValueError, "escapes"):
                portable.validate_plugin(self.kit)
            path.unlink()
            path.write_bytes(content)
        link = self.kit / "skills/escape"
        link.symlink_to(self.base, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, "escapes"):
            portable.validate_plugin(self.kit)
