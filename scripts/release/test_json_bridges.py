"""Offline checks for reproducible Dictionary generation and shipped exports."""
import importlib.util
from pathlib import Path
import plistlib
import subprocess
import sys
import tempfile
import unittest
import zipfile

import manifest
import privacy

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("bridge_generator", ROOT / "scripts/make-bridge.py")
BUILDER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BUILDER)


class JSONBridgeTests(unittest.TestCase):
    def test_privacy_accepts_complete_current_or_legacy_sets_only(self):
        privacy.check_names(sorted(privacy.LEGACY_BRIDGE_FILES), "bridges")
        privacy.check_names(sorted(privacy.BRIDGE_FILES), "bridges")
        for count in range(1, 4):
            partial = privacy.LEGACY_BRIDGE_FILES | set(sorted(privacy.BRIDGE_FILES - privacy.LEGACY_BRIDGE_FILES)[:count])
            with self.subTest(count=count), self.assertRaises(ValueError):
                privacy.check_names(sorted(partial), "bridges")

    def test_text_bytes_are_unchanged(self):
        for name, model in BUILDER.PROFILES["27"]["bridges"].items():
            expected = plistlib.dumps(BUILDER.build(model, "3100.0.2.3"), fmt=plistlib.FMT_BINARY)
            self.assertEqual((ROOT / "bridges" / (name + ".shortcut")).read_bytes(), expected)

    def test_dictionary_profile_preserves_complete_observed_structure(self):
        for model in BUILDER.PROFILES["27"]["bridges"].values():
            value = BUILDER.build(model, "3100.0.2.3", "json")
            self.assertEqual(value["WFWorkflowClientVersion"], "5114.0.6.204")
            self.assertEqual(value["WFWorkflowOutputContentItemClasses"], ["WFDictionaryContentItem"])
            self.assertEqual(len(value["WFWorkflowInputContentItemClasses"]), 19)
            self.assertEqual(value["WFWorkflowTypes"], ["WFWorkflowTypeShowInSearch"])
            ask, output = [action["WFWorkflowActionParameters"] for action in value["WFWorkflowActions"]]
            self.assertEqual(ask["WFGenerativeResultType"], "Dictionary")
            self.assertEqual(ask["WFLLMModel"], model)
            self.assertEqual(ask["WFLLMPrompt"], BUILDER.token_string({"Type": "ExtensionInput"}))
            self.assertNotIn("WFNoOutputSurfaceBehavior", output)
            self.assertEqual(output["WFOutput"], output["WFResponse"])
            self.assertEqual(output["WFOutput"]["Value"]["attachmentsByRange"]["{0, 1}"]["OutputUUID"], ask["UUID"])

    def test_archive_has_all_nine_and_reviewed_signed_json_exports(self):
        with tempfile.TemporaryDirectory() as directory:
            subprocess.run([sys.executable, str(ROOT / "scripts/package-bridges.py"), directory],
                           check=True, capture_output=True)
            with zipfile.ZipFile(Path(directory) / "hollis-bridges.zip") as archive:
                self.assertEqual(set(archive.namelist()), manifest.BRIDGE_MEMBERS)
                for name in archive.namelist():
                    if name.startswith("AFM Bridge JSON - "):
                        data = archive.read(name)
                        self.assertEqual(data[:4], b"AEA1")
                        self.assertEqual(data, (ROOT / "bridges" / name).read_bytes())


if __name__ == "__main__":
    unittest.main()
