"""Offline result validation; no service startup or cloud calls."""
from pathlib import Path
import sqlite3
import tempfile
import unittest

from live_digest_smoke import ROOT, inspect


class LiveResultTests(unittest.TestCase):
    def test_completed_fixture_keeps_zero_prediction_and_reports_model(self):
        (ROOT / "tmp").mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(dir=ROOT / "tmp", prefix="minion-test-") as directory:
            data = Path(directory)
            (data / "digest.md").write_text("## Про що матеріал\nПриклад.")
            with sqlite3.connect(":memory:") as db:
                db.executescript("""
                    CREATE TABLE items(id,state,error);
                    CREATE TABLE digests(item_id,markdown_path,actual_model,provider,prompt_version);
                    CREATE TABLE interest_predictions(item_id,probability);
                    INSERT INTO items VALUES(1,'ready','');
                    INSERT INTO digests VALUES(1,'digest.md','fixture:cloud','ollama-cloud','fixture-v1');
                    INSERT INTO interest_predictions VALUES(1,0.0);
                """)
                result = inspect(db, data, True)
                self.assertEqual(result["fixture_confidence"], 0)
                self.assertEqual(result["actual_model"], "fixture:cloud")
                self.assertEqual(result["sections"], ["## Про що матеріал"])
                db.execute("DELETE FROM interest_predictions")
                with self.assertRaises(ValueError):
                    inspect(db, data, True)
                db.execute("UPDATE items SET state='enriching'")
                self.assertIsNone(inspect(db, data, False))
                db.execute("UPDATE items SET state='needs_input'")
                with self.assertRaises(ValueError):
                    inspect(db, data, False)


if __name__ == "__main__":
    unittest.main()
