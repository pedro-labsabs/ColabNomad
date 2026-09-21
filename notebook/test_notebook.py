import json
import unittest
from pathlib import Path


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

    def test_notebook_contains_no_runtime_orchestration(self):
        text = Path("notebook/colabnomad.ipynb").read_text(encoding="utf-8")
        for banned in ("cloudflared tunnel", "ttyd ", "opencode web", "opencode serve", "apt-get", "nohup"):
            self.assertNotIn(banned, text)
