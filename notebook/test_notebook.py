import io
import json
import sys
import types
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest import mock


class NotebookStructureTests(unittest.TestCase):
    def test_notebook_has_only_two_code_cells(self):
        with Path("notebook/colabnomad.ipynb").open(encoding="utf-8") as notebook:
            nb = json.load(notebook)
        code = [cell for cell in nb["cells"] if cell["cell_type"] == "code"]
        self.assertEqual(len(code), 2)



    def test_notebook_uses_current_repo_and_realigns_reused_origin(self):
        with Path("notebook/colabnomad.ipynb").open(encoding="utf-8") as notebook:
            nb = json.load(notebook)
        code = [cell for cell in nb["cells"] if cell["cell_type"] == "code"]
        first = "".join(code[0]["source"])
        second = "".join(code[1]["source"])
        self.assertIn("https://github.com/pedro-labsabs/ColabNomad.git", first)
        self.assertIn("remote", second)
        self.assertIn("set-url", second)
        self.assertIn("COLABNOMAD_REPO", second)

    def test_notebook_passes_colab_userdata_secrets_to_bootstrap_environment(self):
        with Path("notebook/colabnomad.ipynb").open(encoding="utf-8") as notebook:
            nb = json.load(notebook)
        code = [cell for cell in nb["cells"] if cell["cell_type"] == "code"]
        second = "".join(code[1]["source"])
        self.assertIn("from google.colab import userdata", second)
        self.assertIn("GITHUB_TOKEN", second)
        self.assertIn("OPENCODE_API_KEY", second)
        self.assertIn("env=bootstrap_env", second)
        self.assertIn("SecretNotFoundError", second)
        self.assertIn("TimeoutException", second)

    def test_notebook_prints_bootstrap_connection_summary(self):
        with Path("notebook/colabnomad.ipynb").open(encoding="utf-8") as notebook:
            nb = json.load(notebook)
        code = [cell for cell in nb["cells"] if cell["cell_type"] == "code"]
        second = "".join(code[1]["source"])

        userdata = types.SimpleNamespace(get=lambda _name: None)
        colab = types.ModuleType("google.colab")
        colab.userdata = userdata
        google = types.ModuleType("google")
        google.colab = colab
        summary = '{"OpenCodeURL":"https://open.lhr.life","TerminalURL":"https://term.trycloudflare.com"}\n'

        def fake_run(argv, **kwargs):
            if argv and argv[0] == "python":
                return mock.Mock(returncode=0, stdout=summary, check_returncode=lambda: None)
            return mock.Mock(returncode=0, stdout="", check_returncode=lambda: None)

        namespace = {
            "COLABNOMAD_REPO": "https://github.com/pedro-labsabs/ColabNomad.git",
            "COLABNOMAD_REF": "main",
            "COLABNOMAD_RELEASE": "",
            "TARGET_REPO": "https://github.com/pedro-labsabs/opjev",
            "TARGET_REF": "",
        }
        output = io.StringIO()
        with mock.patch.dict(sys.modules, {"google": google, "google.colab": colab}), \
             mock.patch("subprocess.run", side_effect=fake_run), redirect_stdout(output):
            exec(second, namespace)
        self.assertIn("https://open.lhr.life", output.getvalue())
        self.assertIn("https://term.trycloudflare.com", output.getvalue())

    def test_notebook_treats_userdata_timeout_as_missing_optional_secret(self):
        with Path("notebook/colabnomad.ipynb").open(encoding="utf-8") as notebook:
            nb = json.load(notebook)
        code = [cell for cell in nb["cells"] if cell["cell_type"] == "code"]
        second = "".join(code[1]["source"])

        class TimeoutException(Exception):
            pass

        class FakeUserdata:
            @staticmethod
            def get(name):
                if name == "OPENCODE_API_KEY":
                    raise TimeoutException(name)
                return None

        FakeUserdata.TimeoutException = TimeoutException
        colab = types.ModuleType("google.colab")
        colab.userdata = FakeUserdata
        google = types.ModuleType("google")
        google.colab = colab
        bootstrap_calls = []

        def fake_run(argv, **kwargs):
            if argv and argv[0] == "python":
                bootstrap_calls.append((argv, kwargs))
                return mock.Mock(returncode=0, stdout="ok\n", check_returncode=lambda: None)
            return mock.Mock(returncode=0, stdout="", check_returncode=lambda: None)

        namespace = {
            "COLABNOMAD_REPO": "https://github.com/pedro-labsabs/ColabNomad.git",
            "COLABNOMAD_REF": "main",
            "COLABNOMAD_RELEASE": "",
            "TARGET_REPO": "https://github.com/pedro-labsabs/opjev",
            "TARGET_REF": "",
        }
        with mock.patch.dict(sys.modules, {"google": google, "google.colab": colab}), \
             mock.patch("subprocess.run", side_effect=fake_run):
            exec(second, namespace)

        self.assertEqual(len(bootstrap_calls), 1)

    def test_notebook_contains_no_runtime_orchestration(self):
        text = Path("notebook/colabnomad.ipynb").read_text(encoding="utf-8")
        for banned in ("cloudflared tunnel", "ttyd ", "opencode web", "opencode serve", "apt-get", "nohup"):
            self.assertNotIn(banned, text)
