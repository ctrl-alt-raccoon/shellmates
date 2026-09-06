import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[1] / "try-harness.py"
spec = importlib.util.spec_from_file_location("trial", SCRIPT)
trial = importlib.util.module_from_spec(spec)
spec.loader.exec_module(trial)


class TrialTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="shellmates-harness-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.source = self.root / "harness with spaces"
        self.source.mkdir()
        self.script = self.source / "harness.py"
        self.script.write_text("import sys\nassert sys.argv[1] == 'project'\n"
                               "assert '--with-harness' in sys.argv\n", encoding="utf-8")
        self.target = self.root / "trial with spaces"
        self.env = {"PATH": os.environ["PATH"], "HOME": str(self.root / "home"),
                    "XDG_CONFIG_HOME": str(self.root / "original-config"),
                    "XDG_STATE_HOME": str(self.root / "original-state"),
                    "XDG_DATA_HOME": str(self.root / "original-data"),
                    "CODEX_HOME": str(self.root / "native-codex"),
                    "CLAUDE_CONFIG_DIR": str(self.root / "native-claude"),
                    "PYTHONDONTWRITEBYTECODE": "1"}
        self.args = ["--harness", str(self.source), "--directory", str(self.target)]

    def invoke(self, extra=()):
        with patch.dict(os.environ, self.env, clear=True), contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            return trial.main(self.args + list(extra))

    def test_preview_does_not_create_or_execute(self):
        with patch.object(trial.subprocess, "run", side_effect=AssertionError("preview executed")):
            self.assertEqual(self.invoke(), 0)
        self.assertFalse(self.target.exists())

    def test_existing_target_and_broken_symlink_refused(self):
        self.target.mkdir()
        note = self.target / "keep"
        note.write_text("user work")
        self.assertEqual(self.invoke(["--apply"]), 2)
        self.assertEqual(note.read_text(), "user work")
        note.unlink()
        self.target.rmdir()
        self.target.symlink_to(self.root / "absent", target_is_directory=True)
        self.assertEqual(self.invoke(["--apply"]), 2)
        self.assertTrue(self.target.is_symlink())

    def test_failure_retains_partial_trial_without_runner(self):
        self.script.write_text("raise SystemExit(42)\n")
        self.assertEqual(self.invoke(["--apply"]), 2)
        self.assertTrue((self.target / "project/project.md").exists())
        self.assertFalse((self.target / "run").exists())

    def test_runner_preserves_argv_and_native_homes_isolates_manager_state(self):
        self.assertEqual(self.invoke(["--apply"]), 0)
        self.assertEqual(self.target.stat().st_mode & 0o777, 0o700)
        subprocess.run(["sh", "-n", str(self.target / "run")], check=True)
        probe = self.root / "probe.py"
        probe.write_text("import json, os, sys\nprint(json.dumps({'args':sys.argv[1:], 'cwd':os.getcwd(), 'env':dict(os.environ)}))\n")
        payload = ["new", "--topic", "quoted topic", "--", "-p", "profile with spaces",
                   "-c", 'model_reasoning_effort="high"', "$(must-not-execute)", "; literal"]
        result = subprocess.run([str(self.target / "run"), sys.executable, str(probe), *payload],
                                env=self.env, text=True, capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        data = json.loads(result.stdout)
        self.assertEqual(data["args"], payload)
        self.assertEqual(data["cwd"], str(self.target / "project"))
        for key in ("HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR"):
            self.assertEqual(data["env"][key], self.env[key])
            self.assertFalse(Path(self.env[key]).exists())
        for key in ("XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "SCREENDIR"):
            self.assertTrue(Path(data["env"][key]).is_relative_to(self.target))
            self.assertTrue(Path(data["env"][key]).is_dir())
        for key in ("XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"):
            self.assertFalse(Path(self.env[key]).exists())


if __name__ == "__main__":
    unittest.main()
