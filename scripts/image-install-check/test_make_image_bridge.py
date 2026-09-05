import importlib.util
import plistlib
import sys
import unittest
from pathlib import Path


SCRIPT = Path(__file__).parents[1] / "make-image-bridge.py"
SPEC = importlib.util.spec_from_file_location("make_image_bridge", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


class ImageBridgeGeneratorTests(unittest.TestCase):
    def test_dynamic_reference_contract(self):
        workflow = MODULE.build_dynamic_reference()
        actions = workflow["WFWorkflowActions"]
        self.assertEqual(
            [action["WFWorkflowActionIdentifier"] for action in actions],
            [
                "is.workflow.actions.getvalueforkey",
                "is.workflow.actions.setvariable",
                "is.workflow.actions.getvalueforkey",
                "is.workflow.actions.getvalueforkey",
                "is.workflow.actions.base64encode",
                "is.workflow.actions.detect.images",
                "com.apple.GenerativePlaygroundApp.GenerateImageIntent",
                "is.workflow.actions.output",
            ],
        )
        self.assertEqual(actions[0]["WFWorkflowActionParameters"]["WFDictionaryKey"], "prompt")
        self.assertEqual(actions[2]["WFWorkflowActionParameters"]["WFDictionaryKey"], "style")
        self.assertEqual(actions[3]["WFWorkflowActionParameters"]["WFDictionaryKey"], "reference_base64")
        self.assertEqual(actions[4]["WFWorkflowActionParameters"]["WFEncodeMode"], "Decode")
        create = actions[6]["WFWorkflowActionParameters"]
        self.assertEqual(create["saveToLibrary"], "never")
        self.assertEqual(create["AppIntentDescriptor"]["AppIntentIdentifier"], "GenerateImageIntent")
        self.assertNotIn("WFInput", actions[5]["WFWorkflowActionParameters"])
        output = actions[7]["WFWorkflowActionParameters"]
        self.assertEqual(output["WFOutput"]["WFSerializationType"], "WFTextTokenString")
        self.assertEqual(output["WFResponse"]["WFSerializationType"], "WFTextTokenAttachment")
        encoded = plistlib.dumps(workflow, fmt=plistlib.FMT_BINARY)
        self.assertEqual(plistlib.loads(encoded), workflow)

    def test_chatgpt_diagnostic_uses_captured_typed_entity(self):
        workflow = MODULE.build_chatgpt_diagnostic()
        actions = workflow["WFWorkflowActions"]
        self.assertEqual(len(actions), 2)
        create = actions[0]["WFWorkflowActionParameters"]
        self.assertEqual(create["prompt"]["Value"]["string"], MODULE.CHATGPT_PROMPT)
        self.assertEqual(create["style"]["title"]["key"], "ChatGPT")
        self.assertIn("GenerativePartnerPrototypeIntentChatGPT", create["style"]["identifier"])
        self.assertNotIn("image", create)
        self.assertEqual(create["saveToLibrary"], "never")


if __name__ == "__main__":
    unittest.main()
