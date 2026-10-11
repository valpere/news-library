"""Regression coverage for stale review targets and incomplete CI gates."""
import unittest

from pr_gate import blockers


class GateTests(unittest.TestCase):
    def fixture(self):
        return {"state": "OPEN", "isDraft": False, "headRefOid": "head", "baseRefOid": "base",
                "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN",
                "statusCheckRollup": [{"__typename": "CheckRun", "name": "news-library-check",
                                       "status": "COMPLETED", "conclusion": "SUCCESS"}]}

    def test_only_exact_reviewed_target_passes(self):
        pr = self.fixture()
        self.assertEqual(blockers(pr, "head", "base", ["news-library-check"]), [])
        for field in ("headRefOid", "baseRefOid"):
            changed = dict(pr, **{field: "new"})
            self.assertTrue(blockers(changed, "head", "base", ["news-library-check"]))

    def test_missing_pending_failed_skipped_and_unknown_checks_block(self):
        for check in (None,
                      {"__typename": "CheckRun", "status": "IN_PROGRESS", "conclusion": ""},
                      {"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "FAILURE"},
                      {"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SKIPPED"},
                      {"__typename": "Unknown", "status": "COMPLETED", "conclusion": "SUCCESS"}):
            pr = self.fixture()
            pr["statusCheckRollup"] = [] if check is None else [dict(check, name="news-library-check")]
            self.assertTrue(blockers(pr, "head", "base", ["news-library-check"]))

    def test_unsuccessful_additional_check_also_blocks(self):
        pr = self.fixture()
        pr["statusCheckRollup"].append({"__typename": "StatusContext", "context": "other", "state": "PENDING"})
        self.assertTrue(blockers(pr, "head", "base", ["news-library-check"]))
        pr["statusCheckRollup"][-1]["state"] = "SUCCESS"
        self.assertEqual(blockers(pr, "head", "base", ["news-library-check"]), [])

    def test_closed_draft_conflicting_and_blocked_prs_fail(self):
        for field, value in (("state", "MERGED"), ("isDraft", True),
                             ("mergeable", "UNKNOWN"), ("mergeStateStatus", "BLOCKED")):
            self.assertTrue(blockers(dict(self.fixture(), **{field: value}), "head", "base", ["news-library-check"]))


if __name__ == "__main__":
    unittest.main()
