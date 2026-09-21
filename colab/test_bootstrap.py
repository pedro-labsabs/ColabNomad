import hashlib
import io
import json
import os
import subprocess
import sys
import types
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest import mock
from urllib.error import HTTPError

from colab import bootstrap


class BootstrapTests(unittest.TestCase):
    def test_network_requests_use_finite_timeout(self):
        payload = b"payload"
        digest = hashlib.sha256(payload).hexdigest()
        calls = []

        def urlopen(url, **kwargs):
            calls.append((url, kwargs))
            if url.endswith("SHA256SUMS"):
                return io.BytesIO((digest + "  colabnomad-linux-amd64\n").encode())
            return io.BytesIO(payload)

        with tempfile.TemporaryDirectory() as tmp, mock.patch.object(bootstrap.urllib.request, "urlopen", side_effect=urlopen):
            dst = Path(tmp) / "download"
            bootstrap.download_verified("https://example.test/file", "sha256:" + digest, dst)
            config = bootstrap.BootstrapConfig("owner/repo", release="v0.1.0", state_dir=Path(tmp) / "state")
            bootstrap.try_release(config, Path(tmp))
        self.assertTrue(calls)
        self.assertTrue(all(call[1].get("timeout") == bootstrap.NETWORK_TIMEOUT for call in calls))
        self.assertGreater(bootstrap.NETWORK_TIMEOUT, 0)

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
        self.assertEqual(argv, ["/state/colabnomad", "up", "--state-dir", "/content/.colabnomad", "--repo", "owner/repo", "--ref", "main"])

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

    def test_missing_release_binary_falls_back_on_404(self):
        payload = b"binary"
        digest = hashlib.sha256(payload).hexdigest()
        error = HTTPError("url", 404, "missing", {}, None)
        with tempfile.TemporaryDirectory() as tmp, mock.patch.object(
            bootstrap.urllib.request, "urlopen", side_effect=[
                io.BytesIO((digest + "  colabnomad-linux-amd64\n").encode()), error
            ]):
            config = bootstrap.BootstrapConfig("owner/repo", release="v0.1.0", state_dir=Path(tmp) / "state")
            self.assertIsNone(bootstrap.try_release(config, Path(tmp)))

    def test_release_temporary_download_is_under_state_dir(self):
        payload = b"binary"
        digest = hashlib.sha256(payload).hexdigest()
        with tempfile.TemporaryDirectory() as tmp:
            state_dir = Path(tmp) / "state"
            config = bootstrap.BootstrapConfig("owner/repo", release="v0.1.0", state_dir=state_dir)
            downloaded_paths = []

            def fake_download(url, integrity, dst):
                downloaded_paths.append(Path(dst))
                Path(dst).write_bytes(payload)
                return Path(dst)

            with mock.patch.object(bootstrap.urllib.request, "urlopen", return_value=io.BytesIO(
                (digest + "  colabnomad-linux-amd64\n").encode()
            )), mock.patch.object(bootstrap, "download_verified", side_effect=fake_download):
                bootstrap.try_release(config, Path(tmp))
            self.assertTrue(downloaded_paths[0].is_relative_to(state_dir))

    def test_main_uses_checkout_root_when_cwd_is_elsewhere(self):
        with tempfile.TemporaryDirectory() as elsewhere:
            with mock.patch.object(bootstrap, "build_checked_out", return_value=Path("binary")) as build, \
                 mock.patch.object(bootstrap, "collect_colab_secrets", return_value={}), \
                 mock.patch.object(bootstrap, "run_up", return_value=0):
                original_cwd = os.getcwd()
                os.chdir(elsewhere)
                try:
                    self.assertEqual(bootstrap.main(["--target-repo", "owner/repo"]), 0)
                finally:
                    os.chdir(original_cwd)
            self.assertEqual(build.call_args.args[1], Path(bootstrap.__file__).resolve().parents[1])

    def test_go_archive_links_are_rejected(self):
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode="w:gz") as tar:
            link = tarfile.TarInfo("go/bin/go")
            link.type = tarfile.SYMTYPE
            link.linkname = "../../outside"
            tar.addfile(link)
        archive.seek(0)
        with tempfile.TemporaryDirectory() as tmp:
            archive_path = Path(tmp) / "go.tgz"
            archive_path.write_bytes(archive.read())
            with self.assertRaises(bootstrap.BootstrapError):
                bootstrap._safe_extract(archive_path, Path(tmp) / "extract")

    def test_run_up_returns_after_foreground_subprocess(self):
        config = bootstrap.BootstrapConfig("owner/repo")
        with mock.patch.object(bootstrap.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as run:
            result = bootstrap.run_up(Path("/bin/colabnomad"), config, {})
        self.assertEqual(result, 0)
        run.assert_called_once()

    def test_run_up_hands_manifest_and_verified_binary_path_to_sanitized_process(self):
        config = bootstrap.BootstrapConfig("owner/repo")
        with mock.patch.object(bootstrap.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as run:
            bootstrap.run_up(Path("/state/bin/colabnomad"), config, {"GITHUB_TOKEN": "x"}, Path("/checkout"))
        env = run.call_args.kwargs["env"]
        self.assertEqual(env["COLABNOMAD_VERSIONS_FILE"], "/checkout/config/versions.json")
        self.assertEqual(env["PATH"].split(os.pathsep)[0], "/state/bin")
        self.assertNotIn("x", repr(run.call_args.args[0]))

    def test_run_up_passes_state_dir_to_cli_and_environment(self):
        config = bootstrap.BootstrapConfig("owner/repo", state_dir=Path("/custom/state"))
        with mock.patch.object(bootstrap.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as run:
            bootstrap.run_up(Path("/state/bin/colabnomad"), config, {})
        self.assertIn("--state-dir", run.call_args.args[0])
        self.assertEqual(run.call_args.args[0][run.call_args.args[0].index("--state-dir") + 1], "/custom/state")
        self.assertEqual(run.call_args.kwargs["env"]["COLABNOMAD_STATE_DIR"], "/custom/state")


    def test_userdata_attribute_error_is_treated_as_missing_optional_secret(self):
        fake_userdata = mock.Mock()
        fake_userdata.get.side_effect = AttributeError("no kernel")
        google = types.ModuleType("google")
        colab = types.ModuleType("google.colab")
        colab.userdata = fake_userdata
        google.colab = colab
        with mock.patch.dict(sys.modules, {"google": google, "google.colab": colab}):
            self.assertIsNone(bootstrap._userdata_get("GITHUB_TOKEN"))

    def test_userdata_secret_not_found_is_treated_as_missing_optional_secret(self):
        class SecretNotFoundError(Exception):
            pass

        fake_userdata = mock.Mock()
        fake_userdata.SecretNotFoundError = SecretNotFoundError
        fake_userdata.get.side_effect = SecretNotFoundError("GITHUB_TOKEN")
        google = types.ModuleType("google")
        colab = types.ModuleType("google.colab")
        colab.userdata = fake_userdata
        google.colab = colab
        with mock.patch.dict(sys.modules, {"google": google, "google.colab": colab}):
            self.assertIsNone(bootstrap._userdata_get("GITHUB_TOKEN"))

    def test_userdata_timeout_is_treated_as_missing_optional_secret(self):
        class TimeoutException(Exception):
            pass

        fake_userdata = mock.Mock()
        fake_userdata.TimeoutException = TimeoutException
        fake_userdata.get.side_effect = TimeoutException("OPENCODE_API_KEY")
        google = types.ModuleType("google")
        colab = types.ModuleType("google.colab")
        colab.userdata = fake_userdata
        google.colab = colab
        with mock.patch.dict(sys.modules, {"google": google, "google.colab": colab}):
            self.assertIsNone(bootstrap._userdata_get("OPENCODE_API_KEY"))

    def test_collect_colab_secrets_prefers_environment_without_userdata_lookup(self):
        with mock.patch.dict(os.environ, {"GITHUB_TOKEN": "env-token", "OPENCODE_API_KEY": "env-key"}, clear=False), \
             mock.patch.object(bootstrap, "_userdata_get") as get:
            self.assertEqual(bootstrap.collect_colab_secrets(), {
                "GITHUB_TOKEN": "env-token",
                "OPENCODE_API_KEY": "env-key",
            })
        get.assert_not_called()

    def test_release_lookup_uses_current_organization_repository(self):
        config = bootstrap.BootstrapConfig("owner/repo", release="v0.1.0")
        error = HTTPError("url", 404, "missing", {}, None)
        with tempfile.TemporaryDirectory() as tmp, mock.patch.object(
            bootstrap.urllib.request, "urlopen", side_effect=error
        ) as urlopen:
            self.assertIsNone(bootstrap.try_release(config, Path(tmp)))
        requested = urlopen.call_args.args[0]
        self.assertTrue(
            requested.startswith("https://github.com/pedro-labsabs/ColabNomad/releases/download/"),
            requested,
        )


if __name__ == "__main__":
    unittest.main()
