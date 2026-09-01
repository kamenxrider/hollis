#!/usr/bin/env python3
"""Generate AFM bridge .shortcut files whose Use Model prompt is bound to
Shortcut Input rather than a hardcoded literal string.

Structure mirrors the decoded internals of the existing PCC Test shortcuts
(~/Library/Shortcuts/Shortcuts.sqlite -> ZSHORTCUTACTIONS.ZDATA):

    is.workflow.actions.askllm   (Use Model)
    is.workflow.actions.output   (Stop and Output)

The only deliberate change is WFLLMPrompt, which becomes a WFTextTokenString
carrying an ExtensionInput attachment (= the "Shortcut Input" variable).
"""

import plistlib
import sys
from pathlib import Path

# U+FFFC OBJECT REPLACEMENT CHARACTER: the placeholder Shortcuts uses in a
# token string to mark where an attachment is spliced in.
OBJ = "\ufffc"

ASK_UUID = "A0000000-0000-4000-8000-00000000AA01"
OUT_UUID = "A0000000-0000-4000-8000-00000000AA02"


def token_string(attachment: dict) -> dict:
    """A WFTextTokenString consisting of exactly one attachment."""
    return {
        "Value": {
            "attachmentsByRange": {"{0, 1}": attachment},
            "string": OBJ,
        },
        "WFSerializationType": "WFTextTokenString",
    }


def build(model: str) -> dict:
    ask = {
        "WFWorkflowActionIdentifier": "is.workflow.actions.askllm",
        "WFWorkflowActionParameters": {
            "UUID": ASK_UUID,
            "WFGenerativeResultType": "Text",
            "WFLLMModel": model,
            # The whole prompt is the piped stdin text.
            "WFLLMPrompt": token_string({"Type": "ExtensionInput"}),
        },
    }

    response_token = token_string(
        {
            "OutputName": "Response",
            "OutputUUID": ASK_UUID,
            "Type": "ActionOutput",
        }
    )

    out = {
        "WFWorkflowActionIdentifier": "is.workflow.actions.output",
        "WFWorkflowActionParameters": {
            "UUID": OUT_UUID,
            "WFNoOutputSurfaceBehavior": "Do Nothing",
            "WFOutput": response_token,
            "WFResponse": response_token,
        },
    }

    return {
        "WFWorkflowActions": [ask, out],
        "WFWorkflowClientVersion": "3100.0.2.3",
        "WFWorkflowMinimumClientVersion": 900,
        "WFWorkflowMinimumClientVersionString": "900",
        "WFWorkflowHasOutputFallback": False,
        "WFWorkflowHasShortcutInputVariables": True,
        "WFWorkflowImportQuestions": [],
        # Declare that the shortcut accepts text from callers (incl. the CLI).
        "WFWorkflowInputContentItemClasses": [
            "WFStringContentItem",
            "WFRichTextContentItem",
        ],
        "WFWorkflowTypes": [],
        "WFQuickActionSurfaces": [],
        "WFWorkflowIcon": {
            "WFWorkflowIconGlyphNumber": 59511,
            "WFWorkflowIconStartColor": 2071128575,
        },
    }


BRIDGES = {
    "AFM Bridge - Cloud": "Apple Intelligence",
    "AFM Bridge - Cloud Pro": "Apple Intelligence Pro",
    # Exact WFLLMModel strings decoded from the Hollis Probe - On-Device /
    # ChatGPT shortcuts (Shortcuts.sqlite ZDATA, 2026-09-01):
    #   On-Device -> "Apple Intelligence on Device"
    #   ChatGPT   -> "ChatGPT" (requires the ChatGPT extension enabled in
    #               System Settings > Apple Intelligence & Siri)
    "AFM Bridge - On-Device": "Apple Intelligence on Device",
    "AFM Bridge - ChatGPT": "ChatGPT",
}


def main() -> int:
    outdir = Path(sys.argv[1] if len(sys.argv) > 1 else ".")
    outdir.mkdir(parents=True, exist_ok=True)
    for name, model in BRIDGES.items():
        path = outdir / f"{name}.shortcut"
        with path.open("wb") as fh:
            plistlib.dump(build(model), fh, fmt=plistlib.FMT_BINARY)
        print(f"wrote {path}  (WFLLMModel={model!r})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
