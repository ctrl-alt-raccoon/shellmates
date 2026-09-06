"""Opt-in installed-CLI tests against a localhost, synthetic provider. No real model."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import unittest

import sys
ASSETS = Path(__file__).resolve().parents[2] / "internal/harness/assets"
sys.path.insert(0, str(ASSETS))
import harness
import importlib.util

spec = importlib.util.spec_from_file_location("review", harness.ROOT / "shared/skills/cross-review/scripts/review.py")
review = importlib.util.module_from_spec(spec)
spec.loader.exec_module(review)


class Provider(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"data":[]}')

    def do_POST(self):
        payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        self.server.requests.append(payload)
        answer = "SYNTHETIC_NATIVE_FIXTURE_OK"
        if "/messages" in self.path:
            events = [
                ("message_start", {"type": "message_start", "message": {
                    "id": "msg_fixture", "type": "message", "role": "assistant",
                    "model": "claude-sonnet-4-6", "content": [], "stop_reason": None,
                    "usage": {"input_tokens": 1, "output_tokens": 1}}}),
                ("content_block_start", {"type": "content_block_start", "index": 0,
                    "content_block": {"type": "text", "text": ""}}),
                ("content_block_delta", {"type": "content_block_delta", "index": 0,
                    "delta": {"type": "text_delta", "text": answer}}),
                ("content_block_stop", {"type": "content_block_stop", "index": 0}),
                ("message_delta", {"type": "message_delta", "delta": {
                    "stop_reason": "end_turn", "stop_sequence": None},
                    "usage": {"output_tokens": 1}}),
                ("message_stop", {"type": "message_stop"}),
            ]
        else:
            message = {"id": "msg_fixture", "type": "message", "role": "assistant",
                       "status": "completed", "content": [
                           {"type": "output_text", "text": answer, "annotations": []}]}
            events = [
                ("response.created", {"type": "response.created", "response": {
                    "id": "resp_fixture", "status": "in_progress", "output": []}}),
                ("response.output_item.added", {"type": "response.output_item.added",
                    "output_index": 0, "item": message}),
                ("response.output_text.delta", {"type": "response.output_text.delta",
                    "item_id": "msg_fixture", "output_index": 0, "content_index": 0,
                    "delta": answer}),
                ("response.output_item.done", {"type": "response.output_item.done",
                    "output_index": 0, "item": message}),
                ("response.completed", {"type": "response.completed", "response": {
                    "id": "resp_fixture", "status": "completed", "output": [message],
                    "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}),
            ]
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for event, body in events:
            self.wfile.write(("event: " + event + "\ndata: " + json.dumps(body) + "\n\n").encode())
        self.wfile.flush()


@unittest.skipUnless(os.environ.get("HARNESS_NATIVE_SMOKE") == "1", "opt-in installed CLI fixture")
class NativeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="native-harness-")
        self.root = Path(self.temp.name).resolve()
        self.addCleanup(self.temp.cleanup)
        self.home = self.root / "home with spaces"
        self.repo = self.root / "repo with spaces"
        self.repo.mkdir()
        (self.repo / "nested").mkdir()
        self.env = {
            "PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
            "HOME": str(self.home), "CODEX_HOME": str(self.home / ".codex"),
            "CLAUDE_CONFIG_DIR": str(self.home / ".claude"),
            "XDG_CONFIG_HOME": str(self.home / ".config"),
            "XDG_DATA_HOME": str(self.home / ".local/share"),
            "XDG_STATE_HOME": str(self.home / ".local/state"),
            "TMPDIR": str(self.root), "NO_PROXY": "*",
            "ANTHROPIC_API_KEY": "synthetic-localhost-fixture-key",
            "HARNESS_TEST_KEY": "synthetic-localhost-fixture-key",
            "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
        }
        subprocess.run(["git", "init", "-q", str(self.repo)], env=self.env, check=True)
        (self.repo / "project.md").write_text(
            "# Fixture\nPROJECT_DISCOVERY_84d5. Check command: printf fixture-check.\n",
            encoding="utf-8")
        self.home.mkdir(parents=True)
        (self.home / ".codex").mkdir()
        (self.home / ".claude").mkdir()
        self.assertEqual(harness.main(["install", "--project", str(self.repo)]), 0)
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Provider)
        self.server.requests = []
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        self.url = f"http://127.0.0.1:{self.server.server_port}"

    def invoke(self, cmd):
        # A timeout is a fixture failure, never retry against a real provider.
        completed = subprocess.run(cmd, input="Report project instructions and available shared skills.",
            cwd=self.repo / "nested", env=self.env, text=True, encoding="utf-8",
            capture_output=True, timeout=45)
        self.assertEqual(completed.returncode, 0, completed.stderr[-4000:] + completed.stdout[-1000:])
        self.assertIn("SYNTHETIC_NATIVE_FIXTURE_OK", completed.stdout)
        payload = json.dumps(self.server.requests)
        for marker in ("PROJECT_DISCOVERY_84d5", "Shared working agreements", "handover", "cross-review"):
            self.assertTrue(marker in payload, "Native context missing: " + marker)

    def codex_provider_args(self):
        return ["-c", 'model="harness-fixture"', "-c", 'model_provider="fixture"',
            "-c", 'model_providers.fixture.name="Fixture"',
            "-c", 'model_providers.fixture.base_url="' + self.url + '/v1"',
            "-c", 'model_providers.fixture.wire_api="responses"',
            "-c", 'model_providers.fixture.env_key="HARNESS_TEST_KEY"',
            "-c", "model_providers.fixture.request_max_retries=0",
            "-c", "features.enable_request_compression=false",
            "-c", "features.shell_snapshot=false"]

    def test_claude_native_context_from_subdirectory(self):
        binary = shutil.which("claude")
        self.assertIsNotNone(binary)
        self.env["ANTHROPIC_BASE_URL"] = self.url
        self.invoke([binary, "--print", "--no-session-persistence", "--model", "sonnet",
                     "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}',
                     "--permission-mode", "dontAsk", "--output-format", "json"])

    def test_codex_native_context_from_subdirectory(self):
        binary = shutil.which("codex")
        self.assertIsNotNone(binary)
        self.invoke([binary, "exec", "--ephemeral", "--sandbox", "read-only", "--json"]
                    + self.codex_provider_args())

    def exercise_review(self, vendor):
        cmd = review.command(vendor)
        try:
            review.require_capabilities(vendor, cmd[0], env=self.env)
        except ValueError as exc:
            # This is an unsupported native CLI, not a passed transport check.
            # Unit tests separately require refusal before any model invocation.
            self.skipTest(str(exc))
        if vendor == "codex":
            cmd += self.codex_provider_args()
        else:
            self.env["ANTHROPIC_BASE_URL"] = self.url
        import argparse
        packet, sources = review.packet(argparse.Namespace(repo=self.repo, diff=None,
            base=None, path=[], file=["project.md"]))
        self.assertIn("project.md", sources)
        self.assertIn("PROJECT_DISCOVERY_84d5", packet)
        with tempfile.TemporaryDirectory(dir=self.root, prefix="review-empty-") as cwd:
            completed = subprocess.run(cmd, input=packet,
                cwd=cwd, env=self.env, text=True, encoding="utf-8", capture_output=True, timeout=45)
        self.assertEqual(completed.returncode, 0, completed.stderr[-3000:])
        self.assertEqual(review.result(vendor, completed.stdout), "SYNTHETIC_NATIVE_FIXTURE_OK")
        tools = [t.get("name", t.get("type")) for r in self.server.requests for t in r.get("tools", [])]
        # These only update turn UI/state, not files/network. Older Codex also
        # advertises update_plan. Every actual I/O tool (including view_image)
        # remains forbidden, irrespective of which feature keys a CLI accepts.
        unexpected = set(tools) - ({"request_user_input", "update_plan"} if vendor == "codex" else set())
        self.assertFalse(unexpected, "Review adapter exposed I/O tools: " + repr(tools))

    def test_claude_review_transport_and_empty_tool_set(self):
        self.exercise_review("claude")

    def test_codex_review_transport_and_no_io_tools(self):
        self.exercise_review("codex")


if __name__ == "__main__":
    unittest.main()
