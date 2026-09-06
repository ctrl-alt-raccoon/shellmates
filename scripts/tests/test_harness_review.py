"""Bounded packet and native permission contracts, consolidated from agent-harness."""

import argparse
import contextlib
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ASSETS = Path(__file__).resolve().parents[2] / "internal/harness/assets"
sys.path.insert(0, str(ASSETS))
import harness
SCRIPT = ASSETS / "shared/skills/cross-review/scripts/review.py"
spec = importlib.util.spec_from_file_location("review", SCRIPT)
review = importlib.util.module_from_spec(spec)
spec.loader.exec_module(review)


class ReviewTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        self.addCleanup(self.temp.cleanup)
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
        self.env.update(HOME=str(self.root), XDG_CONFIG_HOME=str(self.root / "config"))
        environment = patch.dict(os.environ, self.env, clear=True)
        environment.start()
        self.addCleanup(environment.stop)
        self.git("init", "-q")
        self.git("config", "user.email", "fixture@example.invalid")
        self.git("config", "user.name", "Fixture")
        (self.root / "code.txt").write_text("old\n", encoding="utf-8")
        self.git("add", "code.txt")
        self.git("-c", "core.hooksPath=/dev/null", "commit", "-qm", "fixture")

    def git(self, *args):
        return subprocess.run(["git", "-c", "core.hooksPath=/dev/null", *args], cwd=self.root, env=self.env,
                              capture_output=True, text=True, check=True, timeout=20).stdout

    def args(self, **changes):
        result = dict(repo=self.root, diff=None, base=None, path=[], file=[])
        result.update(changes)
        return argparse.Namespace(**result)

    def test_tracked_diff_excludes_untracked_and_preserves_tree(self):
        (self.root / "code.txt").write_text("new\n", encoding="utf-8")
        (self.root / "secret.env").write_text("DO_NOT_SEND_THIS", encoding="utf-8")
        before = self.git("status", "--porcelain")
        prompt, _ = review.packet(self.args(diff="working"))
        self.assertIn("+new", prompt)
        self.assertNotIn("DO_NOT_SEND_THIS", prompt)
        self.assertEqual(before, self.git("status", "--porcelain"))

    def test_staged_and_working_scopes_differ(self):
        (self.root / "code.txt").write_text("staged\n", encoding="utf-8")
        self.git("add", "code.txt")
        (self.root / "code.txt").write_text("unstaged\n", encoding="utf-8")
        staged, _ = review.packet(self.args(diff="staged"))
        working, _ = review.packet(self.args(diff="working"))
        self.assertIn("+staged", staged)
        self.assertIn("+unstaged", working)

    def test_plan_context_and_packet_bound(self):
        (self.root / "project.md").write_text("PROJECT_REQUIREMENT", encoding="utf-8")
        (self.root / "plan.md").write_text("PLAN_UNDER_REVIEW", encoding="utf-8")
        prompt, sources = review.packet(self.args(file=["plan.md"]))
        self.assertIn("PROJECT_REQUIREMENT", prompt)
        self.assertIn("PLAN_UNDER_REVIEW", prompt)
        with patch.object(review, "LIMIT", 10):
            with self.assertRaisesRegex(ValueError, "limit"):
                review.packet(self.args(file=["plan.md"]))

    def test_packet_uses_isolated_git_home_and_disables_diff_colors(self):
        self.git("config", "--global", "harness.fixture", "temporary-home")
        self.assertEqual(review.git(self.root, "config", "--global", "--get", "harness.fixture").strip(),
                         "temporary-home")
        self.git("config", "--global", "color.ui", "always")
        self.git("config", "--global", "color.diff", "always")
        (self.root / "code.txt").write_text("PLAIN_DIFF", encoding="utf-8")
        self.git("add", "code.txt")
        for scope in ("staged", "working"):
            with self.subTest(scope=scope):
                prompt, _ = review.packet(self.args(diff=scope))
                self.assertIn("+PLAIN_DIFF", prompt)
                self.assertNotIn("\x1b", prompt)

    def test_file_labels_are_relative_inside_repo_and_duplicates_are_removed(self):
        (self.root / "nested").mkdir()
        prompt, sources = review.packet(self.args(
            file=["code.txt", str(self.root / "code.txt"), "nested/../code.txt"]))
        self.assertEqual(sources.count("code.txt"), 1)
        self.assertNotIn(str(self.root), prompt)
        with tempfile.TemporaryDirectory() as directory:
            external = Path(directory).resolve() / "plan.md"
            external.write_text("EXPLICIT_EXTERNAL_CONTEXT", encoding="utf-8")
            prompt, sources = review.packet(self.args(file=[str(external)]))
            self.assertIn(str(external), sources)
            self.assertIn("EXPLICIT_EXTERNAL_CONTEXT", prompt)

    def test_base_diff_uses_merge_base_and_literal_path_filter_without_dirty_work(self):
        self.git("branch", "harness-base")
        self.git("checkout", "-qb", "harness-topic")
        (self.root / "code.txt").write_text("TOPIC_CODE\n", encoding="utf-8")
        chosen = self.root / "code[1].txt"
        chosen.write_text("TOPIC_LITERAL\n", encoding="utf-8")
        self.git("add", "code.txt", "code[1].txt")
        self.git("commit", "-qm", "topic change")
        self.git("checkout", "harness-base")
        (self.root / "upstream.txt").write_text("UPSTREAM_ONLY\n", encoding="utf-8")
        self.git("add", "upstream.txt")
        self.git("commit", "-qm", "diverged base")
        self.git("checkout", "harness-topic")
        chosen.write_text("STAGED_ONLY\n", encoding="utf-8")
        self.git("add", "code[1].txt")
        chosen.write_text("UNSTAGED_ONLY\n", encoding="utf-8")
        (self.root / "untracked.txt").write_text("UNTRACKED_ONLY\n", encoding="utf-8")
        before = self.git("status", "--porcelain")
        full, _ = review.packet(self.args(base="harness-base"))
        filtered, _ = review.packet(self.args(base="harness-base", path=["code[1].txt"]))
        self.assertIn("+TOPIC_CODE", full)
        self.assertIn("+TOPIC_LITERAL", full)
        self.assertIn("+TOPIC_LITERAL", filtered)
        self.assertNotIn("TOPIC_CODE", filtered)
        for prompt in (full, filtered):
            for excluded in ("UPSTREAM_ONLY", "STAGED_ONLY", "UNSTAGED_ONLY", "UNTRACKED_ONLY"):
                self.assertNotIn(excluded, prompt)
        self.assertEqual(self.git("status", "--porcelain"), before)

    def test_invalid_base_and_path_without_diff_are_refused(self):
        before = self.git("status", "--porcelain")
        for base in ("--help", "missing-base"):
            with self.subTest(base=base):
                with self.assertRaises(subprocess.CalledProcessError):
                    review.packet(self.args(base=base))
        with self.assertRaisesRegex(ValueError, "requires --diff or --base"):
            review.packet(self.args(path=["code.txt"], file=["code.txt"]))
        self.assertEqual(self.git("status", "--porcelain"), before)

    def test_preview_does_not_require_reviewer_cli_and_execute_still_does(self):
        for vendor in ("claude", "codex"):
            for execute in (False, True):
                with self.subTest(vendor=vendor, execute=execute):
                    argv = [str(SCRIPT), "--reviewer", vendor, "--repo", str(self.root), "--file", "code.txt"]
                    if execute:
                        argv.append("--execute")
                    with patch.object(sys, "argv", argv), patch.object(review.shutil, "which", return_value=None) as lookup:
                        with patch.object(review, "run") as launch, contextlib.redirect_stderr(io.StringIO()):
                            self.assertEqual(review.main(), 2 if execute else 0)
                            launch.assert_not_called()
                        if execute:
                            lookup.assert_called_once_with(vendor)
                        else:
                            lookup.assert_not_called()

    def test_symlink_empty_diff_and_binary_rejected(self):
        (self.root / "link").symlink_to(self.root / "code.txt")
        with self.assertRaises(ValueError):
            review.packet(self.args(file=["link"]))
        with self.assertRaisesRegex(ValueError, "empty"):
            review.packet(self.args(diff="working"))
        (self.root / "binary").write_bytes(b"a\0b")
        with self.assertRaisesRegex(ValueError, "bounded text"):
            review.packet(self.args(file=["binary"]))

    def test_commands_do_not_interpolate_model_or_effort(self):
        with patch.object(review.shutil, "which", side_effect=lambda name: "/bin/" + name):
            for agent in ("claude", "codex"):
                cmd = review.command(agent, "model name;not-a-command", "medium")
                self.assertIn("model name;not-a-command", cmd)
                self.assertFalse(any("bypass" in arg for arg in cmd))
            claude = review.command("claude")
            codex = review.command("codex")
        self.assertEqual(claude[claude.index("--tools") + 1], "")
        self.assertEqual(codex[codex.index("--sandbox") + 1], "read-only")
        self.assertIn("features.hooks=false", codex)
        self.assertIn("features.plugins=false", codex)
        self.assertIn("features.multi_agent=false", codex)
        self.assertNotIn("agents.enabled=false", codex)
        self.assertIn("features.view_image=false", codex)
        self.assertNotIn("tools.view_image=false", codex)

    def test_codex_capability_probe_requires_image_control(self):
        with patch.object(review.subprocess, "run") as probe:
            review.require_capabilities("claude", "/bin/claude")
            probe.assert_not_called()
            for code, output in ((0, "shell_tool stable false\n"),
                                 (1, "view_image stable true\n")):
                probe.return_value = subprocess.CompletedProcess([], code, output, "")
                with self.assertRaisesRegex(ValueError, "required view_image"):
                    review.require_capabilities("codex", "/bin/codex")
            probe.return_value = subprocess.CompletedProcess([], 0, "view_image stable true\n", "")
            review.require_capabilities("codex", "/bin/codex")
            self.assertEqual(probe.call_args.args[0], ["/bin/codex", "features", "list"])
            self.assertEqual(probe.call_args.kwargs["timeout"], 10)

    def test_unsupported_codex_refused_before_model_launch(self):
        argv = [str(SCRIPT), "--reviewer", "codex", "--repo", str(self.root),
                "--file", "code.txt", "--execute"]
        with patch.object(sys, "argv", argv), patch.object(review.shutil, "which", return_value="/bin/codex"):
            with patch.object(review.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "", "")):
                with patch.object(review, "run") as launch, contextlib.redirect_stderr(io.StringIO()):
                    self.assertEqual(review.main(), 2)
                    launch.assert_not_called()

    def test_cli_failures_and_empty_results_are_not_approval(self):
        with self.assertRaises(ValueError):
            review.result("claude", '{"subtype":"success","result":""}')
        with self.assertRaises(ValueError):
            review.result("codex", '{"type":"turn.failed"}')
        with self.assertRaises(ValueError):
            review.result("codex", '{"type":"item.completed","item":{"type":"agent_message","text":"ok"}}')
        self.assertEqual(review.result("claude", '{"subtype":"success","result":"finding"}'), "finding")

    def test_timeout_terminates_own_descendant(self):
        marker = self.root / "should-not-exist"
        child = "import time,pathlib; time.sleep(2); pathlib.Path(" + repr(str(marker)) + ").touch()"
        parent = "import subprocess,sys,time; subprocess.Popen([sys.executable,'-c'," + repr(child) + "]); time.sleep(20)"
        with self.assertRaisesRegex(ValueError, "timed out"):
            review.run([sys.executable, "-c", parent], "", str(self.root), 0.1)
        # A bounded local test delay, not an agent polling loop.
        subprocess.run([sys.executable, "-c", "import time; time.sleep(2.2)"], check=True)
        self.assertFalse(marker.exists())


if __name__ == "__main__":
    unittest.main()
