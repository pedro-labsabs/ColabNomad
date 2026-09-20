import json
import unittest
from pathlib import Path


class NotebookStructureTests(unittest.TestCase):
    def test_notebook_has_only_two_code_cells(self):
        with Path("notebook/colabnomad.ipynb").open(encoding="utf-8") as notebook:
            nb = json.load(notebook)
        code = [cell for cell in nb["cells"] if cell["cell_type"] == "code"]
        self.assertEqual(len(code), 2)


    def test_notebook_contains_no_runtime_orchestration(self):
        text = Path("notebook/colabnomad.ipynb").read_text(encoding="utf-8")
        for banned in ("cloudflared tunnel", "ttyd ", "opencode web", "opencode serve", "apt-get", "nohup"):
            self.assertNotIn(banned, text)
