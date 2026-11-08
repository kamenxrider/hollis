"""Check every command the plugin's skills and docs tell an agent to run.

The current CLI is built from this checkout and its `agent-context` is the
authority for command paths and flags. The checked-in 0.4.0 contract is the
runtime the plugin currently pins. An instruction that runtime cannot honor
must be listed in NEEDS_NEW_RUNTIME with the gate sentence an agent checks
first, and that sentence must appear in the same paragraph, list item or table
row as the instruction. Release re-pinning empties the list.
"""
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
KIT = ROOT / "plugins/hollis"
SOURCES = ("skills", "docs")
PINNED = Path(__file__).resolve().parent / "testdata/agent-context-0.4.0.json"

# Instructions the pinned 0.4.0 runtime cannot honor, each with the gate that
# must accompany it so an agent on 0.4.0 checks before running it.
NEEDS_NEW_RUNTIME = {
    "respond --code": "`agent-context` lists `--code`",
    "respond --schema": "`agent-context` lists `--schema`",
    "respond --prompt-file -": "`agent-context` describes `--prompt-file -`",
    "doctor --model": "`agent-context` lists `--model` on `doctor`",
    "respond --messages": "`agent-context` lists `--messages` on `respond`",
    "setup": "`agent-context` lists `--import` on `setup`",
    "setup --model": "`agent-context` lists `--import` on `setup`",
    "setup --import": "`agent-context` lists `--import` on `setup`",
}

ROOT_FLAGS = {"agent", "help", "json", "no-input", "quiet", "select", "version"}
# Forward-compatible instructions must remain gated until the runtime exposes them.
PENDING_FLAGS = {"setup": {"import"}}
SETUP_COMMANDS = {"check", "install", "rollback", "path", "runtime-record", "status", "import"}
SETUP_ROUTES = {"local", "cloud", "cloud-pro", "on-device", "chatgpt", "image"}
# Flags named in the text that belong to the native helper or setup.sh, not the CLI.
FOREIGN_FLAGS = {"protocol-version", "reopen"}
PREFIXES = (
    (re.compile(r'"\$\(bash "\$KIT/scripts/setup\.sh" path\)"'), "hollis"),
    (re.compile(r'bash "\$KIT/scripts/run\.sh"'), "hollis"),
    (re.compile(r'bash "\$KIT/scripts/setup\.sh"'), "setup.sh"),
    (re.compile(r'^run\.sh\b'), "hollis"),
)
ITEM = re.compile(r"^\s*(?:[-*]|\d+\.|\|)\s")


def paragraphs(text):
    """Split Markdown into paragraphs; list items and table rows stand alone.

    A fenced block joins the prose paragraph before it, which is where its
    gate is written.
    """
    blocks, current, fence, previous = [], [], None, ""
    for line in text.splitlines():
        if fence is not None:
            if line.startswith("```"):
                blocks.append(("fence", previous + "\n" + "\n".join(fence), fence))
                fence = None
            else:
                fence.append(line)
            continue
        if line.startswith("```"):
            if current:
                blocks.append(("prose", "\n".join(current), None)); current = []
            previous = blocks[-1][1] if blocks else ""
            fence = []
            continue
        if not line.strip() or ITEM.match(line):
            if current:
                blocks.append(("prose", "\n".join(current), None)); current = []
            if not line.strip():
                continue
        current.append(line)
    if current:
        blocks.append(("prose", "\n".join(current), None))
    return blocks


def snippets():
    """Yield (file, paragraph, snippet) for code spans and fenced shell lines."""
    for source in SOURCES:
        for path in sorted((KIT / source).rglob("*.md")):
            name = str(path.relative_to(KIT))
            for kind, paragraph, fence in paragraphs(path.read_text()):
                if kind == "fence":
                    for line in fence:
                        line = line.strip()
                        if line and not line.startswith("#"):
                            yield name, paragraph, line
                else:
                    for span in re.findall(r"`([^`\n]+)`", paragraph):
                        yield name, paragraph, span.strip()


def normalize(snippet):
    for pattern, replacement in PREFIXES:
        snippet = pattern.sub(replacement, snippet)
    return snippet


def tokens_of(snippet):
    try:
        words = shlex.split(snippet)
    except ValueError:
        return None
    for i, word in enumerate(words):
        if word in (">", "|", "&&", ";"):
            return words[:i]
    return words


def placeholder(word):
    return word.startswith("<") or word in ("...",) or "|" in word or word.startswith("$")


def flags_in(words):
    flags, values = [], {}
    for i, word in enumerate(words):
        if word.startswith("--") and len(word) > 2:
            flag, _, inline = word[2:].partition("=")
            flags.append(flag)
            values[flag] = inline or (words[i + 1] if i + 1 < len(words) else None)
    return flags, values


class SkillCommandTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        prebuilt = os.environ.get("HOLLIS_PLUGIN_TEST_BINARY")
        if not prebuilt and not shutil.which("go"):
            raise unittest.SkipTest("Go is required to build the CLI whose agent-context is the contract")
        cls.temp = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.temp.cleanup)
        binary = Path(prebuilt) if prebuilt else Path(cls.temp.name) / "hollis"
        if not prebuilt:
            subprocess.run(["go", "build", "-o", str(binary), "./cmd/hollis"], cwd=ROOT, check=True, timeout=600)
        env = dict(os.environ, HOME=cls.temp.name, HOLLIS_STATE_DIR=str(Path(cls.temp.name) / "state"))
        current = json.loads(subprocess.run([str(binary), "agent-context"], env=env, check=True,
                                            capture_output=True, text=True, timeout=60).stdout)
        cls.current = cls.flatten(current["commands"])
        models = json.loads(subprocess.run([str(binary), "models", "--json"], env=env, check=True,
                                           capture_output=True, text=True, timeout=60).stdout)
        cls.models = {row["model"] for row in models}
        cls.pinned = {k: set(v) for k, v in json.loads(PINNED.read_text())["commands"].items()}

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    @staticmethod
    def flatten(commands, parent=""):
        table = {}
        for command in commands:
            name = f"{parent} {command['name']}".strip()
            table[name] = {flag["name"] for flag in command.get("flags", [])}
            table.update(SkillCommandTests.flatten(command.get("subcommands", []), name))
        return table

    def uses(self):
        """(file, paragraph, snippet, command path, remaining words, features) per instruction.

        The path is None for a bare flag mention, "setup.sh" for the setup
        helper and "" for root flags.
        """
        found = []
        top = {name for name in self.current if " " not in name}
        for name, paragraph, raw in snippets():
            words = tokens_of(normalize(raw))
            if not words:
                continue
            if words[0] == "setup.sh":
                found.append((name, paragraph, raw, "setup.sh", words[1:], set()))
                continue
            if words[0] == "hollis":
                words = words[1:]
            elif words[0] not in top or len(words) == 1:
                if words[0].startswith("--"):
                    found.append((name, paragraph, raw, None, words, set()))
                continue
            path, rest = [], list(words)
            while rest and " ".join(path + [rest[0]]) in self.current:
                path.append(rest.pop(0))
            features = set()
            if not path and rest and not rest[0].startswith("-"):
                features.add("bare prompt")
                path = ["respond"]
            found.append((name, paragraph, raw, " ".join(path), rest, features))
        return found

    def new_runtime_needs(self, path, words, features):
        """What in one instruction the pinned runtime cannot honor."""
        needed = set(features)
        if path not in self.pinned:
            needed.add(path)
        flags, values = flags_in(words)
        for flag in flags:
            if flag not in self.pinned.get(path, set()):
                needed.add(f"{path} --{flag}")
        if path == "respond" and values.get("prompt-file") == "-":
            needed.add("respond --prompt-file -")
        return needed

    def test_text_names_real_commands_and_flags(self):
        all_flags = set().union(*self.current.values(), *PENDING_FLAGS.values()) | ROOT_FLAGS | FOREIGN_FLAGS
        checked = 0
        for name, paragraph, raw, path, words, _ in self.uses():
            with self.subTest(file=name, snippet=raw):
                flags, values = flags_in(words)
                if path is None:
                    # Packaging docs name maintainer-tool flags; only skills are agent instructions.
                    if not name.startswith("skills/"):
                        continue
                    for flag in flags:
                        self.assertIn(flag, all_flags, f"unknown flag --{flag}")
                elif path == "setup.sh":
                    self.assertIn(words[0], SETUP_COMMANDS, "unknown setup.sh command")
                    if words[0] in ("status", "import") and len(words) > 1 and not placeholder(words[1]):
                        self.assertIn(words[1], SETUP_ROUTES)
                    for flag in flags:
                        self.assertEqual(flag, "reopen", f"unknown setup.sh flag --{flag}")
                else:
                    allowed = self.current[path] if path else ROOT_FLAGS
                    for flag in flags:
                        if flag in PENDING_FLAGS.get(path, set()) and flag not in allowed:
                            self.assertIn(NEEDS_NEW_RUNTIME[f"{path} --{flag}"], paragraph)
                        else:
                            self.assertIn(flag, allowed, f"--{flag} is not a flag of '{path or 'hollis'}'")
                    model = values.get("model")
                    if model and not placeholder(model):
                        self.assertIn(model, self.models, f"unknown model {model}")
                checked += 1
        self.assertGreater(checked, 30, "command extraction found too few instructions")

    def test_new_runtime_instructions_carry_their_gate(self):
        seen = set()
        for name, paragraph, raw, path, words, features in self.uses():
            # Root flags and setup.sh commands exist in every supported runtime.
            if path in (None, "setup.sh", ""):
                continue
            for entry in self.new_runtime_needs(path, words, features):
                seen.add(entry)
                with self.subTest(file=name, snippet=raw, needs=entry):
                    self.assertIn(entry, NEEDS_NEW_RUNTIME,
                                  "the pinned runtime cannot run this; gate it and list it in NEEDS_NEW_RUNTIME")
                    self.assertIn(NEEDS_NEW_RUNTIME[entry], paragraph,
                                  "instruction for the new runtime without its gate in the same paragraph")
        self.assertEqual(set(NEEDS_NEW_RUNTIME) - seen, set(), "stale NEEDS_NEW_RUNTIME entries")
        for entry in set(NEEDS_NEW_RUNTIME) - {"respond --prompt-file -"}:
            command, _, flag = entry.partition(" --")
            with self.subTest(entry=entry):
                self.assertTrue(command not in self.pinned or (flag and flag not in self.pinned[command]),
                                "the pinned runtime already supports this")

    def test_ungated_instructions_are_caught(self):
        """The gate check must fail for the instructions a reviewer reported."""
        for raw in ('bash "$KIT/scripts/run.sh" respond --code "x" > out.sh',
                    "doctor --model local --json"):
            words = tokens_of(normalize(raw))
            if words[0] == "hollis":
                words = words[1:]
            path = words[0]
            needed = self.new_runtime_needs(path, words[1:], set())
            with self.subTest(snippet=raw):
                self.assertTrue(needed)
                self.assertTrue(all(NEEDS_NEW_RUNTIME[e] not in f"Use `{raw}`." for e in needed))

    def test_pinned_contract_matches_runtime_lock(self):
        lock = json.loads((KIT / "runtime.lock.json").read_text())
        self.assertEqual(json.loads(PINNED.read_text())["runtime"], lock["version"],
                         "refresh testdata/agent-context-<version>.json from the newly pinned runtime")


if __name__ == "__main__":
    unittest.main()
