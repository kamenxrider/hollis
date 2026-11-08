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

import argparse
import plistlib
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


# OS profiles. The 27 profile is the
# measured one; the 26 profile is a best guess and ships labeled untested:
# the on-device/ChatGPT WFLLMModel strings have not been decoded on a 26
# install, and the Cloud string is inferred from public plists. Cloud Pro
# did not exist on 26 (Use Model had no Cloud Pro location), so it is
# deliberately absent.
PROFILES = {
    "27": {
        "client_version": "3100.0.2.3",
        "bridges": {
            "AFM Bridge - Cloud": "Apple Intelligence",
            "AFM Bridge - Cloud Pro": "Apple Intelligence Pro",
            "AFM Bridge - On-Device": "Apple Intelligence on Device",
            "AFM Bridge - ChatGPT": "ChatGPT",
        },
    },
    "26": {
        "client_version": "2700.0.4",
        "bridges": {
            "AFM Bridge - Cloud": "Apple Intelligence",
            "AFM Bridge - On-Device": "Apple Intelligence on Device",
            "AFM Bridge - ChatGPT": "ChatGPT",
        },
    },
}


def build(model: str, client_version: str, output_type: str = "text") -> dict:
    if output_type not in ("text", "json"):
        raise ValueError("output_type must be text or json")
    ask = {
        "WFWorkflowActionIdentifier": "is.workflow.actions.askllm",
        "WFWorkflowActionParameters": {
            "UUID": ASK_UUID,
            "WFGenerativeResultType": "Dictionary" if output_type == "json" else "Text",
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

    workflow = {
        "WFWorkflowActions": [ask, out],
        "WFWorkflowClientVersion": client_version,
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
    if output_type == "json":
        # Dictionary profile measured on macOS 27. Keep the complete observed
        # input classes and root metadata, not only the Use Model result type.
        workflow["WFWorkflowClientVersion"] = "5114.0.6.204"
        workflow["WFWorkflowOutputContentItemClasses"] = ["WFDictionaryContentItem"]
        workflow["WFWorkflowInputContentItemClasses"] = [
            "WFAppContentItem", "WFAppStoreAppContentItem", "WFArticleContentItem",
            "WFContactContentItem", "WFDateContentItem", "WFEmailAddressContentItem",
            "WFFolderContentItem", "WFGenericFileContentItem", "WFImageContentItem",
            "WFiTunesProductContentItem", "WFLocationContentItem", "WFDCMapsLinkContentItem",
            "WFAVAssetContentItem", "WFPDFContentItem", "WFPhoneNumberContentItem",
            "WFRichTextContentItem", "WFSafariWebPageContentItem", "WFStringContentItem",
            "WFURLContentItem",
        ]
        workflow["WFWorkflowTypes"] = ["WFWorkflowTypeShowInSearch"]
        del out["WFWorkflowActionParameters"]["WFNoOutputSurfaceBehavior"]
    return workflow


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Generate AFM bridge .shortcut files bound to Shortcut Input."
    )
    parser.add_argument(
        "outdir",
        nargs="?",
        default=".",
        help="directory to write the .shortcut files into (default: .)",
    )
    parser.add_argument(
        "--os",
        choices=sorted(PROFILES),
        default="27",
        help="target macOS generation (default: 27, the measured profile; "
        "26 writes three bridges with best-guess strings and is untested)",
    )
    parser.add_argument("--output-type", choices=("text", "json"), default="text",
                        help="text bridges (default) or macOS 27 Dictionary bridges")
    args = parser.parse_args()
    if args.output_type == "json" and args.os != "27":
        parser.error("Dictionary bridges are measured only on macOS 27")

    profile = PROFILES[args.os]
    outdir = Path(args.outdir)
    outdir.mkdir(parents=True, exist_ok=True)
    if args.os == "26":
        print(
            "WARNING: the 26 profile is a best guess: "
            "the on-device/ChatGPT WFLLMModel strings were never decoded on 26, "
            "and Cloud Pro does not exist on 26. Treat the output as untested."
        )
    for name, model in profile["bridges"].items():
        if args.output_type == "json":
            name = name.replace("AFM Bridge - ", "AFM Bridge JSON - ")
        path = outdir / f"{name}.shortcut"
        with path.open("wb") as fh:
            plistlib.dump(build(model, profile["client_version"], args.output_type), fh, fmt=plistlib.FMT_BINARY)
        print(f"wrote {path}  (WFLLMModel={model!r})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
