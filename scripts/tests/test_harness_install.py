"""Public project-harness lifecycle. All mutation is inside disposable repositories."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ASSETS = Path(__file__).resolve().parents[2] / "internal/harness/assets"
sys.path.insert(0, str(ASSETS))
import harness


class InstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="harness-install-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.repo = self.root / "repo with spaces; literal"
        self.repo.mkdir()
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
        self.env.update(HOME=str(self.root / "unused-home"), CODEX_HOME=str(self.root / "unused-codex"),
                        CLAUDE_CONFIG_DIR=str(self.root / "unused-claude"),
                        XDG_CONFIG_HOME=str(self.root / "unused-config"),
                        XDG_STATE_HOME=str(self.root / "unused-state"),
                        XDG_DATA_HOME=str(self.root / "unused-data"))
        env = patch.dict(os.environ, self.env, clear=True)
        env.start()
        self.addCleanup(env.stop)
        self.git("init", "--quiet", "--template=")

    def git(self, *args):
        return subprocess.run(["git", "-c", "core.hooksPath=/dev/null", *args], cwd=self.repo,
                              env=self.env, capture_output=True, check=True, timeout=20).stdout

    def run_harness(self, action="install", *options):
        output, errors = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            code = harness.main([action, "--project", str(self.repo), *options])
        return code, output.getvalue() + errors.getvalue()

    def success(self, action="install", *options):
        code, output = self.run_harness(action, *options)
        self.assertEqual(code, 0, output)
        return output

    def snapshot(self):
        return {str(p.relative_to(self.repo)): ("link", os.readlink(p)) if p.is_symlink()
                else ("file", p.read_bytes(), p.stat().st_mode)
                for p in self.repo.rglob("*") if p.is_symlink() or p.is_file()}

    def test_preview_has_no_writes_and_install_needs_no_profiles(self):
        before = self.snapshot()
        self.success("install", "--dry-run")
        self.assertEqual(before, self.snapshot())
        self.success()
        for name in ("unused-home", "unused-codex", "unused-claude", "unused-config", "unused-state", "unused-data"):
            self.assertFalse((self.root / name).exists(), name)
        self.success("check")
        before = self.snapshot()
        self.success()
        self.assertEqual(before, self.snapshot())

    def test_native_profile_and_non_root_targets_are_refused(self):
        before = self.snapshot()
        with patch.dict(os.environ, {"CODEX_HOME": str(self.repo)}):
            self.assertEqual(self.run_harness()[0], 2)
        self.assertEqual(before, self.snapshot())
        nested = self.repo / "nested"
        nested.mkdir()
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(harness.main(["install", "--project", str(nested)]), 2)

    def test_portable_bundle_and_both_vendors_have_context(self):
        (self.repo / "project.md").write_text("# Projet\nFACT_891: vérifier les tests.\n")
        self.success()
        for name in ("CLAUDE.md", "AGENTS.md"):
            body = (self.repo / name).read_text()
            self.assertIn("FACT_891", body)
            self.assertIn("YAGNI", body)
            self.assertEqual(body.count("FACT_891"), 1)
            self.assertNotIn(str(ASSETS), body)
        other = self.root / "moved repo"
        shutil.copytree(self.repo, other, symlinks=True)
        for name, target in harness.LINKS.items():
            self.assertEqual(os.readlink(other / name), target)
            self.assertFalse(Path(target).is_absolute())
            self.assertTrue((other / name / "SKILL.md").is_file())
            self.assertTrue((other / name).resolve().is_relative_to(other))
        completed = subprocess.run([sys.executable, "-I", "-B", str(other / harness.PREFIX / "harness.py"),
            "check", "--project", str(other)], env=self.env, capture_output=True, text=True, timeout=20)
        self.assertEqual(completed.returncode, 0, completed.stderr)

    def test_preserves_native_text_and_user_edits_outside_block(self):
        (self.repo / "CLAUDE.md").write_text("# Existing Claude instructions\nKeep this exact text.")
        self.success()
        with (self.repo / "CLAUDE.md").open("a") as stream:
            stream.write("\nNew vendor note.\n")
        (self.repo / "project.md").write_text("# Actual project\nRun real-check.\n")
        self.assertEqual(self.run_harness("check")[0], 1)
        self.success("update")
        self.assertIn("Run real-check.", (self.repo / "AGENTS.md").read_text())
        self.success("check")
        self.success("remove")
        self.assertEqual((self.repo / "CLAUDE.md").read_text(),
                         "# Existing Claude instructions\nKeep this exact text.\nNew vendor note.\n")
        self.assertFalse((self.repo / "AGENTS.md").exists())
        self.assertTrue((self.repo / "project.md").exists())
        self.assertFalse((self.repo / harness.MANIFEST).exists())
        self.assertFalse((self.repo / ".shellmates").exists())

    def test_shared_install_survives_a_real_git_clone_without_mode_drift(self):
        self.success()
        self.git("config", "user.email", "fixture@example.invalid")
        self.git("config", "user.name", "Fixture")
        self.git("add", ".")
        self.git("commit", "--quiet", "-m", "Project harness fixture")
        clone = self.root / "fresh clone"
        self.git("clone", "--quiet", str(self.repo), str(clone))
        completed = subprocess.run([sys.executable, "-I", "-B", str(ASSETS / "harness.py"),
            "check", "--project", str(clone)], env=self.env, capture_output=True, text=True, timeout=20)
        self.assertEqual(completed.returncode, 0, completed.stderr + completed.stdout)

    def test_private_install_tightens_files_and_remove_restores_original_mode(self):
        entry = self.repo / "AGENTS.md"
        entry.write_text("Existing instructions")
        entry.chmod(0o644)
        self.success("install", "--local")
        self.assertEqual(entry.stat().st_mode & 0o777, 0o600)
        self.success("remove")
        self.assertEqual(entry.stat().st_mode & 0o777, 0o644)
        self.assertEqual(entry.read_text(), "Existing instructions")

    def test_concurrent_installer_refuses_without_mutation(self):
        import fcntl
        before = self.snapshot()
        with contextlib.closing(harness.Tree(self.repo)) as tree:
            fcntl.flock(tree.fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.assertEqual(self.run_harness()[0], 2)
        self.assertEqual(before, self.snapshot())

    def test_adopts_old_projection_and_restores_exact_original(self):
        body = "# Shared project\nCommand: true\n"
        original = f"<!-- agent-harness generated sha256={harness.digest(body)}; edit sources -->\n{body}"
        (self.repo / "project.md").write_text(body)
        (self.repo / "AGENTS.md").write_text(original)
        self.success()
        self.assertEqual((self.repo / "AGENTS.md").read_text().count("Command: true"), 1)
        self.success("remove")
        self.assertEqual((self.repo / "AGENTS.md").read_text(), original)

    def test_private_preferences_are_opt_in_ignored_and_retained_on_update(self):
        rules = self.root / "personal.md"
        rules.write_text("# Private preferences\nPERSONAL_MARKER\n")
        before = self.snapshot()
        self.assertEqual(self.run_harness("install", "--preferences", str(rules))[0], 2)
        self.assertEqual(before, self.snapshot())
        (self.repo / harness.EXCLUDE).parent.mkdir(parents=True, exist_ok=True)
        (self.repo / harness.EXCLUDE).write_text("# My existing excludes\n.local-notes\n")
        exclude_before = (self.repo / harness.EXCLUDE).read_bytes()
        self.success("install", "--local", "--preferences", str(rules))
        self.assertIn("PERSONAL_MARKER", (self.repo / "AGENTS.md").read_text())
        for name in ["CLAUDE.md", "AGENTS.md", harness.MANIFEST, harness.PREFIX + "shared/house-rules.md", *harness.LINKS]:
            self.git("check-ignore", "--quiet", name)
        self.success("update")
        self.assertIn("PERSONAL_MARKER", (self.repo / "AGENTS.md").read_text())
        rules.write_text("# Private preferences\nUPDATED_PERSONAL_MARKER\n")
        self.assertEqual(self.run_harness("check", "--preferences", str(rules))[0], 1)
        self.success("update", "--preferences", str(rules))
        self.success("check")
        self.success("remove")
        self.assertEqual((self.repo / harness.EXCLUDE).read_bytes(), exclude_before)
        self.assertEqual(rules.read_text(), "# Private preferences\nUPDATED_PERSONAL_MARKER\n")

    def test_private_mode_refuses_tracked_entries_without_any_writes(self):
        (self.repo / "AGENTS.md").write_text("team-owned")
        self.git("add", "AGENTS.md")
        before = self.snapshot()
        code, output = self.run_harness("install", "--local")
        self.assertEqual(code, 2, output)
        self.assertIn("tracked", output)
        self.assertEqual(before, self.snapshot())

    def test_symlinks_hardlinks_special_files_and_collisions_refuse(self):
        for name, kind in (("AGENTS.md", "symlink"), (".claude", "directory-link"),
                ("project.md", "hardlink"), ("project.md", "fifo"),
                (".agents/skills/handover", "directory"), ("AGENTS.override.md", "broken-link")):
            with self.subTest(name=name, kind=kind):
                target = self.repo / name
                target.parent.mkdir(parents=True, exist_ok=True)
                outside = self.root / ("outside-" + kind)
                if kind == "directory-link":
                    outside.mkdir()
                    target.symlink_to(outside, target_is_directory=True)
                elif kind in ("symlink", "broken-link"):
                    if kind == "symlink":
                        outside.write_text("UNCHANGED")
                    target.symlink_to(outside)
                elif kind == "hardlink":
                    outside.write_text("UNCHANGED")
                    os.link(outside, target)
                elif kind == "fifo":
                    os.mkfifo(target)
                else:
                    target.mkdir()
                self.assertEqual(self.run_harness()[0], 2)
                self.assertFalse((self.repo / harness.MANIFEST).exists())
                if outside.is_file():
                    self.assertEqual(outside.read_text(), "UNCHANGED")
                if target.is_dir() and not target.is_symlink():
                    target.rmdir()
                else:
                    target.unlink()

    def test_modified_owned_file_or_link_prevents_update_and_remove(self):
        self.success()
        file = self.repo / "AGENTS.md"
        original = file.read_text()
        file.write_text(original.replace("YAGNI", "MY_EDIT"))
        before = self.snapshot()
        for action in ("check", "update", "remove"):
            self.assertEqual(self.run_harness(action)[0], 2)
            self.assertEqual(before, self.snapshot())
        file.write_text(original)
        link = self.repo / ".agents/skills/handover"
        link.unlink()
        link.symlink_to(self.root)
        self.assertEqual(self.run_harness("remove")[0], 2)

    def test_failed_write_rolls_back_exact_files(self):
        before = self.snapshot()
        original = harness.Tree.put
        failed = False
        def fail_once(tree, name, value):
            nonlocal failed
            if name == "AGENTS.md" and not failed:
                failed = True
                raise OSError("injected write failure")
            return original(tree, name, value)
        with patch.object(harness.Tree, "put", fail_once):
            self.assertEqual(self.run_harness()[0], 2)
        self.assertTrue(failed)
        self.assertEqual(before, self.snapshot())
        self.assertFalse((self.repo / ".shellmates").exists())

    def test_crash_journal_can_recover_but_cannot_overwrite_external_changes(self):
        with contextlib.closing(harness.Tree(self.repo)) as tree:
            before = {"AGENTS.md": None}
            after = {"AGENTS.md": harness.text_file("partial install")}
            journal = {"schema": 1, "before": before, "after": after, "directories": [".shellmates"]}
            tree.put(harness.JOURNAL, harness.json_file(journal))
            tree.put("AGENTS.md", after["AGENTS.md"])
        self.assertEqual(self.run_harness()[0], 2)
        (self.repo / "AGENTS.md").write_text("external edit")
        self.assertEqual(self.run_harness("recover")[0], 2)
        self.assertEqual((self.repo / "AGENTS.md").read_text(), "external edit")
        (self.repo / "AGENTS.md").write_text("partial install")
        self.success("recover")
        self.assertFalse((self.repo / "AGENTS.md").exists())
        self.assertFalse((self.repo / harness.JOURNAL).exists())

    def test_malformed_recovery_paths_and_duplicates_refused(self):
        target = self.repo / harness.JOURNAL
        target.parent.mkdir()
        for body in ('{"schema":1,"schema":1}', json.dumps({"schema": 1,
            "before": {"../outside": None}, "after": {"../outside": None}, "directories": []})):
            target.write_text(body)
            self.assertEqual(self.run_harness("recover")[0], 2)
            self.assertEqual(target.read_text(), body)

    def test_other_skills_and_settings_survive_remove(self):
        keep = self.repo / ".claude/skills/my-project/SKILL.md"
        keep.parent.mkdir(parents=True)
        keep.write_text("project-owned")
        settings = self.repo / ".claude/settings.json"
        settings.write_text('{"hooks":{"unchanged":true}}')
        self.success()
        self.success("remove")
        self.assertEqual(keep.read_text(), "project-owned")
        self.assertEqual(settings.read_text(), '{"hooks":{"unchanged":true}}')

    def test_deleting_old_native_notes_does_not_resurrect_them_on_remove(self):
        entry = self.repo / "AGENTS.md"
        entry.write_text("Migrate this shared fact into project.md")
        self.success()
        manifest = json.loads((self.repo / harness.MANIFEST).read_text())
        entry.write_text(manifest["native"]["AGENTS.md"]["block"])
        self.success("update")
        self.success("remove")
        self.assertEqual(entry.read_text(), "")

    def test_remove_refuses_unknown_private_bundle_files_before_unignoring(self):
        self.success("install", "--local")
        extra = self.repo / harness.PREFIX / "my-private-note.md"
        extra.write_text("DO_NOT_UNIGNORE")
        before = self.snapshot()
        self.assertEqual(self.run_harness("remove")[0], 2)
        self.assertEqual(before, self.snapshot())
        self.git("check-ignore", "--quiet", str(extra.relative_to(self.repo)))


if __name__ == "__main__":
    unittest.main()
