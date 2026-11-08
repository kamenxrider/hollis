"""Offline publisher checks for Hollis's Agent Plugins 1.0.0 portable core.

Rules are implemented directly from https://agent-plugins.org/specification
and https://agentskills.io/specification; no third-party schema is vendored
or retrieved at load time. Frontmatter uses this project's two-string subset.
Legacy native-host adapters are not portable components or extension data.
"""
import json
from pathlib import Path
import re

SCHEMA = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
FIELDS = {"$schema", "name", "version", "description", "author", "homepage",
          "repository", "license", "keywords", "extensions"}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def validate_manifest(manifest):
    require(isinstance(manifest, dict), "Manifest must be an object")
    require(not (manifest.keys() - FIELDS), "Unknown portable manifest field")
    require(manifest.get("$schema") == SCHEMA, "Unsupported portable schema")
    name = manifest.get("name")
    require(isinstance(name, str) and 1 <= len(name) <= 64
            and re.fullmatch(r"[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?", name)
            and "--" not in name and ".." not in name, "Invalid portable name")
    for field in ("version", "description", "homepage", "repository", "license"):
        require(field not in manifest or isinstance(manifest[field], str),
                f"{field} must be a string")
    if "author" in manifest:
        author = manifest["author"]
        require(isinstance(author, dict)
                and not (author.keys() - {"name", "email", "url"})
                and all(isinstance(v, str) for v in author.values()),
                "Invalid portable author")
    if "keywords" in manifest:
        require(isinstance(manifest["keywords"], list)
                and all(isinstance(v, str) for v in manifest["keywords"]),
                "Keywords must be strings")
    if "extensions" in manifest:
        extensions = manifest["extensions"]
        require(isinstance(extensions, dict), "Extensions must be an object")
        for namespace, value in extensions.items():
            require(re.fullmatch(r"[a-z0-9]+(?:[.-][a-z0-9]+)*\.[a-z0-9-]+", namespace)
                    and isinstance(value, dict), "Invalid extension namespace or value")


def contained(root, path):
    require(path.resolve().is_relative_to(root.resolve()), "Package path escapes plugin root")
    return path


def skill_metadata(path):
    text = path.read_text(encoding="utf-8")
    require(text.startswith("---\n") and "\n---\n" in text[4:], "Missing skill frontmatter")
    header, body = text[4:].split("\n---\n", 1)
    fields = {}
    for line in header.splitlines():
        match = re.fullmatch(r"(name|description): (.+)", line)
        require(match is not None, "Use name/description string frontmatter")
        key, value = match.groups()
        require(key not in fields, "Duplicate skill frontmatter field")
        # Keep the project's YAML plain scalars unambiguous without a dependency.
        require(": " not in value and " #" not in value
                and value[0] not in "\"'[{&*!|>@", "Use plain YAML strings")
        fields[key] = value.strip()
    name, description = fields.get("name", ""), fields.get("description", "")
    require(1 <= len(name) <= 64 and re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", name)
            and name == path.parent.name, "Invalid skill name or directory")
    require(1 <= len(description) <= 1024, "Invalid skill description length")
    require(bool(body.strip()), "Missing skill instructions")
    return fields


def validate_plugin(root):
    root = Path(root)
    manifest_path = contained(root, root / "plugin.json")
    require(manifest_path.is_file(), "Missing portable manifest")
    manifest = json.loads(manifest_path.read_text())
    validate_manifest(manifest)
    # Includes scripts, references and compatibility adapters, not just entrypoints.
    for path in root.rglob("*"):
        contained(root, path)
    skills_root = contained(root, root / "skills")
    if not skills_root.exists():
        return manifest, []
    require(skills_root.is_dir(), "Skills must be a directory")
    skills = []
    for child in sorted(skills_root.iterdir()):
        if child.is_dir() and (child / "SKILL.md").exists():
            skill = contained(root, child / "SKILL.md")
            require(skill.is_file(), "SKILL.md must be a regular file")
            skill_metadata(skill)
            skills.append(skill)
    return manifest, skills
