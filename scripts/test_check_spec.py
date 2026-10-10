"""Check failure modes that could otherwise falsely certify a specification."""
import unittest

from check_spec import validate_contract, validate_measurement


REQ = "| UC-01 | action | result |\n| [REQ-FR-01] | requirement | acceptance | -> [FUN-NL-INGEST](HLD.md#fun-nl-ingest) |\n"
HLD = "### FUN-NL-INGEST\n"
TRACE = "| FR-01 | UC-01 | M1 | AT-01 | planned |\n"


class SpecChecks(unittest.TestCase):
    def test_connected_contract(self):
        self.assertEqual(validate_contract(REQ, HLD, TRACE), 1)

    def test_missing_or_invalid_links_fail(self):
        mutations = [
            (REQ.replace("[REQ-FR-01]", "FR-01"), HLD, TRACE),
            (REQ, "", TRACE),
            (REQ.replace("#fun-nl-ingest", "#missing"), HLD, TRACE),
            (REQ, HLD, ""),
            (REQ, HLD, TRACE.replace("UC-01", "UC-99")),
            (REQ, HLD, TRACE + TRACE),
            (REQ + "| UC-02 | orphan | result |\n", HLD, TRACE),
        ]
        for args in mutations:
            with self.subTest(args=args), self.assertRaises(ValueError):
                validate_contract(*args)

    def test_na_or_failed_tool_is_not_success(self):
        payload = {"result": {"k_drift": {"requirements": 0, "hanging": 0, "value": 0}, "k_drift_verdict": "skipped"}}
        with self.assertRaises(ValueError):
            validate_measurement(payload, 1, 0)
        payload["result"]["k_drift"]["requirements"] = 1
        payload["result"]["k_drift_verdict"] = "ok"
        with self.assertRaises(ValueError):
            validate_measurement(payload, 1, 2)
        validate_measurement(payload, 1, 0)


if __name__ == "__main__":
    unittest.main()
