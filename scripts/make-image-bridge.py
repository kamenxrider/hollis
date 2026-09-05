#!/usr/bin/env python3
"""Generate inspectable Image Playground Shortcut bridge source files.

The action identifiers and GenerateImageIntent parameter layout are copied
from actions exported by the macOS 27 Shortcuts editor. The generated files
remain unsigned until passed through ``shortcuts sign``.
"""

import argparse
import plistlib
import uuid
from pathlib import Path

OBJ = "\ufffc"
CLIENT_VERSION = "3100.0.2.3"
CHATGPT_PROMPT = (
    "A realistic photograph of a timber cabin beside an alpine lake, pine trees "
    "reflected in still water, soft golden sunrise and thin morning mist."
)
# Captured from the native ChatGPT style entity. The editor's transient image
# proxy URI is intentionally omitted; the stable identifier and labels define
# the selection and avoid persisting machine-specific metadata.
CHATGPT_STYLE = {
    "identifier": "z_external_provider_com.apple.gms.GenerativePartnerPrototypeExtension.GenerativePartnerPrototypeIntentChatGPT",
    "subtitle": {"key": "ChatGPT"},
    "title": {"key": "ChatGPT"},
}


def action_output(action_uuid: str, name: str) -> dict:
    return {
        "Value": {"OutputName": name, "OutputUUID": action_uuid, "Type": "ActionOutput"},
        "WFSerializationType": "WFTextTokenAttachment",
    }


def action_output_token(action_uuid: str, name: str) -> dict:
    return {
        "Value": {
            "attachmentsByRange": {
                "{0, 1}": {"OutputName": name, "OutputUUID": action_uuid, "Type": "ActionOutput"}
            },
            "string": OBJ,
        },
        "WFSerializationType": "WFTextTokenString",
    }


def extension_input() -> dict:
    return {
        "Value": {"Type": "ExtensionInput"},
        "WFSerializationType": "WFTextTokenAttachment",
    }


def variable_token(name: str) -> dict:
    return {
        "Value": {
            "attachmentsByRange": {"{0, 1}": {"Type": "Variable", "VariableName": name}},
            "string": OBJ,
        },
        "WFSerializationType": "WFTextTokenString",
    }


def get_dictionary_value(key: str) -> tuple[dict, str]:
    action_uuid = str(uuid.uuid4()).upper()
    return {
        "WFWorkflowActionIdentifier": "is.workflow.actions.getvalueforkey",
        "WFWorkflowActionParameters": {
            "UUID": action_uuid,
            "WFDictionaryKey": key,
            "WFInput": extension_input(),
        },
    }, action_uuid


def build_dynamic_reference() -> dict:
    prompt, prompt_uuid = get_dictionary_value("prompt")
    set_prompt_uuid = str(uuid.uuid4()).upper()
    set_prompt = {
        "WFWorkflowActionIdentifier": "is.workflow.actions.setvariable",
        "WFWorkflowActionParameters": {
            "UUID": set_prompt_uuid,
            "WFVariableName": "Prompt",
            "WFInput": action_output(prompt_uuid, "Dictionary Value"),
        },
    }
    style, style_uuid = get_dictionary_value("style")
    reference, reference_uuid = get_dictionary_value("reference_base64")
    decode_uuid = str(uuid.uuid4()).upper()
    decode = {
        "WFWorkflowActionIdentifier": "is.workflow.actions.base64encode",
        "WFWorkflowActionParameters": {
            "UUID": decode_uuid,
            "WFEncodeMode": "Decode",
            "WFBase64LineBreakMode": "None",
            "WFInput": action_output(reference_uuid, "Dictionary Value"),
        },
    }
    image_uuid = str(uuid.uuid4()).upper()
    image_from_input = {
        "WFWorkflowActionIdentifier": "is.workflow.actions.detect.images",
        "WFWorkflowActionParameters": {
            "UUID": image_uuid,
        },
    }
    create_uuid = str(uuid.uuid4()).upper()
    create = {
        "WFWorkflowActionIdentifier": "com.apple.GenerativePlaygroundApp.GenerateImageIntent",
        "WFWorkflowActionParameters": {
            "AppIntentDescriptor": {
                "AppIntentIdentifier": "GenerateImageIntent",
                "BundleIdentifier": "com.apple.GenerativePlaygroundApp",
                "Name": "Image Playground",
                "TeamIdentifier": "0000000000",
            },
            "UUID": create_uuid,
            "prompt": variable_token("Prompt"),
            "style": action_output(style_uuid, "Dictionary Value"),
            "image": action_output(image_uuid, "Images"),
            "saveToLibrary": "never",
        },
    }
    output_attachment = action_output(create_uuid, "Image")
    output = {
        "WFWorkflowActionIdentifier": "is.workflow.actions.output",
        "WFWorkflowActionParameters": {
            "UUID": str(uuid.uuid4()).upper(),
            "WFNoOutputSurfaceBehavior": "Do Nothing",
            "WFOutput": action_output_token(create_uuid, "Image"),
            "WFResponse": output_attachment,
        },
    }
    return workflow([prompt, set_prompt, style, reference, decode, image_from_input, create, output])


def build_chatgpt_diagnostic() -> dict:
    create_uuid = str(uuid.uuid4()).upper()
    create = {
        "WFWorkflowActionIdentifier": "com.apple.GenerativePlaygroundApp.GenerateImageIntent",
        "WFWorkflowActionParameters": {
            "AppIntentDescriptor": {
                "AppIntentIdentifier": "GenerateImageIntent",
                "BundleIdentifier": "com.apple.GenerativePlaygroundApp",
                "Name": "Image Playground",
                "TeamIdentifier": "0000000000",
            },
            "UUID": create_uuid,
            "prompt": {
                "Value": {"attachmentsByRange": {}, "string": CHATGPT_PROMPT},
                "WFSerializationType": "WFTextTokenString",
            },
            "style": CHATGPT_STYLE,
            "saveToLibrary": "never",
        },
    }
    output_attachment = action_output(create_uuid, "Image")
    output = {
        "WFWorkflowActionIdentifier": "is.workflow.actions.output",
        "WFWorkflowActionParameters": {
            "UUID": str(uuid.uuid4()).upper(),
            "WFNoOutputSurfaceBehavior": "Do Nothing",
            "WFOutput": action_output_token(create_uuid, "Image"),
            "WFResponse": output_attachment,
        },
    }
    return workflow([create, output])


def workflow(actions: list[dict]) -> dict:
    return {
        "WFWorkflowActions": actions,
        "WFWorkflowClientVersion": CLIENT_VERSION,
        "WFWorkflowMinimumClientVersion": 900,
        "WFWorkflowMinimumClientVersionString": "900",
        "WFWorkflowHasOutputFallback": False,
        "WFWorkflowHasShortcutInputVariables": True,
        "WFWorkflowImportQuestions": [],
        "WFWorkflowInputContentItemClasses": ["WFStringContentItem", "WFRichTextContentItem"],
        "WFWorkflowTypes": [],
        "WFQuickActionSurfaces": [],
        "WFWorkflowIcon": {
            "WFWorkflowIconGlyphNumber": 59511,
            "WFWorkflowIconStartColor": 2071128575,
        },
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Generate Hollis image Shortcut source files")
    parser.add_argument("outdir", nargs="?", default=".")
    args = parser.parse_args()
    outdir = Path(args.outdir)
    outdir.mkdir(parents=True, exist_ok=True)
    path = outdir / "Hollis Image - Reference Input v2.shortcut"
    with path.open("wb") as handle:
        plistlib.dump(build_dynamic_reference(), handle, fmt=plistlib.FMT_BINARY)
    print(f"wrote {path}")
    diagnostic = outdir / "Hollis Image - ChatGPT Diagnostic.shortcut"
    with diagnostic.open("wb") as handle:
        plistlib.dump(build_chatgpt_diagnostic(), handle, fmt=plistlib.FMT_BINARY)
    print(f"wrote {diagnostic}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
