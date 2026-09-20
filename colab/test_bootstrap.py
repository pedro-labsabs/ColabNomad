import hashlib
import io
import json
import os
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest import mock
from urllib.error import HTTPError

from colab import bootstrap


class BootstrapTests(unittest.TestCase):
    def test_checksum_mismatch_never_executes_binary(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            manifest = {"bootstrap_go": {"version": "1.27.1", "artifacts": {
                "linux-amd64": {"url": "https://go.test/go.tgz", "integrity": "sha256:" + "0" * 64}
            }}}
            (root / "config").mkdir()
            (root / "config" / "versions.json").write_text(json.dumps(manifest))
            with mock.patch.object(bootstrap.urllib.request, "urlopen", return_value=io.BytesIO(b"wrong")), \
                 mock.patch.object(bootstrap.subprocess, "run") as run:
                with self.assertRaises(bootstrap.BootstrapError):
                    bootstrap.build_checked_out(
                        bootstrap.BootstrapConfig("owner/repo", state_dir=root / "state"), root
                    )
                run.assert_not_called()

    def test_secret_is_environment_only_not_argv(self):
        config = bootstrap.BootstrapConfig("owner/repo", "main")
        with mock.patch.object(bootstrap, "_userdata_get", return_value="ghp:a@b!punctuation"), \
             mock.patch.object(bootstrap.subprocess, "run") as run:
            bootstrap.run_up(Path("/state/colabnomad"), config, {"GITHUB_TOKEN": "ghp:a@b!punctuation"})
        argv = run.call_args.args[0]
        self.assertNotIn("ghp:a@b!punctuation", argv)
        self.assertNotIn("ghp:a@b!punctuation", repr(argv))
        self.assertEqual(run.call_args.kwargs["env"]["GITHUB_TOKEN"], "ghp:a@b!punctuation")
        self.assertEqual(argv, ["/state/colabnomad", "up", "--repo", "owner/repo", "--ref", "main"])

    def test_platform_key_maps_supported_machines(self):
        with mock.patch.object(bootstrap.platform, "machine", return_value="x86_64"):
            self.assertEqual(bootstrap.platform_key(), "linux-amd64")
        with mock.patch.object(bootstrap.platform, "machine", return_value="aarch64"):
            self.assertEqual(bootstrap.platform_key(), "linux-arm64")

    def test_release_requires_named_binary_in_checksums(self):
        config = bootstrap.BootstrapConfig("owner/repo", release="v0.1.0")
        with tempfile.TemporaryDirectory() as tmp, mock.patch.object(
            bootstrap.urllib.request, "urlopen", side_effect=[
                io.BytesIO(b"abc\n"), io.BytesIO(b"missing  other\n")
            ]):
            with self.assertRaises(bootstrap.BootstrapError):
                bootstrap.try_release(config, Path(tmp))

    def test_release_downloads_and_verifies_binary(self):
        payload = b"binary"
        digest = hashlib.sha256(payload).hexdigest()
        with tempfile.TemporaryDirectory() as tmp, mock.patch.object(
            bootstrap.urllib.request, "urlopen", side_effect=[
                io.BytesIO((digest + "  colabnomad-linux-amd64\n").encode()),
                io.BytesIO(payload),
            ]):
            config = bootstrap.BootstrapConfig("owner/repo", release="v0.1.0", state_dir=Path(tmp) / "state")
            result = bootstrap.try_release(config, Path(tmp))
            self.assertEqual(result.read_bytes(), payload)
            self.assertTrue(os.access(result, os.X_OK))

    def test_fallback_uses_pinned_go_archive_and_builds_checked_out_source(self):
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode="w:gz") as tar:
            data = b"go binary"
            info = tarfile.TarInfo("go/bin/go")
            info.size = len(data)
            tar.addfile(info, io.BytesIO(data))
        archive_bytes = archive.getvalue()
        digest = hashlib.sha256(archive_bytes).hexdigest()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "config").mkdir()
            (root / "config" / "versions.json").write_text(json.dumps({
                "bootstrap_go": {"version": "1.27.1", "artifacts": {
                    "linux-amd64": {"url": "https://go.dev/dl/pinned.tgz", "integrity": "sha256:" + digest}
                }}
            }))
            config = bootstrap.BootstrapConfig("owner/repo", target_ref="ref", state_dir=root / "state")
            with mock.patch.object(bootstrap, "try_release", return_value=None), \
                 mock.patch.object(bootstrap.urllib.request, "urlopen", return_value=io.BytesIO(archive_bytes)), \
                 mock.patch.object(bootstrap.subprocess, "run") as run:
                result = bootstrap.build_checked_out(config, root)
            self.assertEqual(result, root / "state" / "bin" / "colabnomad")
            command = run.call_args.args[0]
            self.assertEqual(command[0], str(root / "state" / "toolchains" / "go-1.27.1" / "bin" / "go"))
            self.assertEqual(command[-1], "./cmd/colabnomad")
            self.assertEqual(run.call_args.kwargs["cwd"], str(root))
            self.assertEqual(run.call_args.kwargs["env"]["GOARCH"], "amd64")

    def test_missing_release_falls_back_on_404(self):
        config = bootstrap.BootstrapConfig("owner/repo", release="v0.1.0")
        error = HTTPError("url", 404, "missing", {}, None)
        with tempfile.TemporaryDirectory() as tmp, mock.patch.object(
            bootstrap.urllib.request, "urlopen", side_effect=error
        ):
            self.assertIsNone(bootstrap.try_release(config, Path(tmp)))

    def test_run_up_returns_after_foreground_subprocess(self):
        config = bootstrap.BootstrapConfig("owner/repo")
        with mock.patch.object(bootstrap.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as run:
            result = bootstrap.run_up(Path("/bin/colabnomad"), config, {})
        self.assertEqual(result, 0)
        run.assert_called_once()


if __name__ == "__main__":
    unittest.main()
