import unittest

import search_feedback


def event(id, mode, results, judgments, not_found=None):
    return {'id': id, 'query': f'q{id}', 'mode': mode, 'actual_mode': mode, 'filters': '',
            'result_ids': results, 'judgments': judgments, 'created_at': 't', 'not_found_at': not_found}


class SearchFeedback(unittest.TestCase):
    events = [
        event(1, 'fts', [5, 6, 7], {6: 2, 5: 0}),
        event(2, 'hybrid', [8, 9], {8: 1, 9: 2}),
        event(3, 'hybrid', [4], {}, not_found='t'),
    ]

    def test_summary_by_mode(self):
        s = search_feedback.summary(self.events)
        self.assertEqual(s['fts']['searches'], 1)
        self.assertEqual(s['fts']['mrr_exact'], 0.5)  # exact result at rank 2
        self.assertEqual(s['fts']['precision_exact'], 0.5)
        self.assertEqual(s['hybrid']['searches'], 2)
        self.assertEqual(s['hybrid']['judged_searches'], 1)
        self.assertEqual(s['hybrid']['not_found'], 1)
        self.assertEqual(s['hybrid']['partial'], 1)

    def test_cases_use_only_exact_judgments(self):
        cases = search_feedback.cases(self.events)
        self.assertEqual(cases, [
            {'query': 'q1', 'modes': ['fts'], 'expected_ids': [6]},
            {'query': 'q2', 'modes': ['fts', 'hybrid'], 'expected_ids': [9]},
        ])

    def test_empty(self):
        self.assertEqual(search_feedback.summary([]), {})
        self.assertEqual(search_feedback.cases([]), [])


if __name__ == '__main__':
    unittest.main()
