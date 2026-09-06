import re
import unittest
from pathlib import Path


WORKFLOW = Path(__file__).parents[2] / ".github" / "workflows" / "poolside-review.yml"


def step_block(workflow, name):
    marker = f"      - name: {name}\n"
    if marker not in workflow:
        return ""
    return workflow.split(marker, 1)[1].split("\n      - name:", 1)[0]


def policy_errors(workflow):
    errors = []
    if "  pull_request_target:\n    branches: [main]" not in workflow:
        errors.append("workflow source is not limited to trusted main")
    if re.search(r"^  pull_request:\s*$", workflow, re.MULTILINE):
        errors.append("untrusted pull_request trigger remains")
    if "ref: ${{ github.event.pull_request.base.sha }}" not in workflow:
        errors.append("checkout is not bound to base SHA")
    if workflow.count("github.event.pull_request.base.ref == github.event.repository.default_branch") != 2:
        errors.append("default-branch guard is not enforced in both jobs")
    if workflow.count("github.event.pull_request.head.repo.full_name == github.repository") != 2:
        errors.append("same-repository guard is not enforced in both jobs")
    if workflow.count("github.event.pull_request.draft == false") != 2:
        errors.append("draft guard is not enforced in both jobs")
    if "persist-credentials: false" not in workflow or "path: trusted-source" not in workflow:
        errors.append("trusted checkout policy is incomplete")
    if "python3 trusted-source/scripts/poolside-review/poolside_review.py" not in workflow:
        errors.append("helper is not run from trusted source")
    lowered = workflow.lower()
    for forbidden in ("curl ", "| sh", "pool exec", "unsafe-auto-allow"):
        if forbidden in lowered:
            errors.append(f"forbidden executable path: {forbidden}")
    action_uses = re.findall(r"uses:\s*([^\s]+)", workflow)
    if not action_uses or any(not re.fullmatch(r"[^@]+@[0-9a-f]{40}", use) for use in action_uses):
        errors.append("action is not pinned to a full commit")

    if "\n  review:\n" not in workflow or "\n  post:\n" not in workflow:
        return errors + ["separate review and post jobs are required"]
    review_job, post_job = workflow.split("\n  review:\n", 1)[1].split("\n  post:\n", 1)
    if "issues: write" in review_job or "pull-requests: write" in review_job:
        errors.append("review job has comment write permission")
    if re.search(r"^\s{6}(?!issues: write$)[a-z-]+:\s+(?:read|write)$", post_job, re.MULTILINE):
        errors.append("post job has permission beyond issue comments")
    if "      issues: write" not in post_job:
        errors.append("post job lacks issue comment permission")

    fetch = step_block(workflow, "Fetch the validated pull request diff")
    inference = step_block(workflow, "Request a text-only Poolside review")
    validate = step_block(workflow, "Validate the artifact as inert data")
    post = step_block(workflow, "Post the review comment")
    if "GH_TOKEN:" not in fetch or "POOLSIDE_API_KEY" in fetch:
        errors.append("fetch credential scope is unsafe")
    if "POOLSIDE_API_KEY:" not in inference or "GH_TOKEN:" in inference:
        errors.append("Poolside credential scope is unsafe")
    if "GH_TOKEN:" in validate or "POOLSIDE_API_KEY" in validate:
        errors.append("artifact validation receives a credential")
    if "GH_TOKEN:" not in post or "POOLSIDE_API_KEY" in post:
        errors.append("posting credential scope is unsafe")
    if workflow.count("POOLSIDE_API_KEY:") != 1:
        errors.append("Poolside credential must appear in one step only")
    if "${{ runner.temp }}/poolside-review/review.json" not in workflow:
        errors.append("artifact is not outside trusted source")
    if "keys == [\"base_sha\", \"head_sha\", \"pull_request\", \"repository\", \"review_markdown\", \"schema_version\"]" not in workflow:
        errors.append("posting artifact schema is not exact")
    if "/usr/bin/gh api --silent" not in workflow:
        errors.append("posting command does not suppress the response body")
    return errors


class WorkflowPolicyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = WORKFLOW.read_text(encoding="utf-8")

    def test_workflow_enforces_trusted_source_and_secret_boundaries(self):
        self.assertEqual(policy_errors(self.workflow), [])

    def test_fixture_with_pr_head_checkout_is_rejected(self):
        unsafe = self.workflow.replace(
            "ref: ${{ github.event.pull_request.base.sha }}",
            "ref: ${{ github.event.pull_request.head.sha }}",
            1,
        )
        self.assertIn("checkout is not bound to base SHA", policy_errors(unsafe))

    def test_fixture_with_poolside_secret_in_post_job_is_rejected(self):
        unsafe = self.workflow.replace(
            "          GH_TOKEN: ${{ github.token }}\n          REVIEW_REQUEST:",
            "          GH_TOKEN: ${{ github.token }}\n"
            "          POOLSIDE_API_KEY: ${{ secrets.POOLSIDE_API_KEY }}\n"
            "          REVIEW_REQUEST:",
            1,
        )
        errors = policy_errors(unsafe)
        self.assertIn("posting credential scope is unsafe", errors)
        self.assertIn("Poolside credential must appear in one step only", errors)

    def test_verified_action_commits_are_recorded(self):
        self.assertIn("actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683", self.workflow)
        self.assertIn(
            "actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02",
            self.workflow,
        )
        self.assertIn(
            "actions/download-artifact@d3f86a106a0bac45b974a628896c90dbdf5c8093",
            self.workflow,
        )


if __name__ == "__main__":
    unittest.main()
