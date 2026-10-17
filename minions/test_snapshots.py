from pathlib import Path
import shutil
import tempfile
import unittest

import snapshots


class Snapshots(unittest.TestCase):
    def setUp(self):
        self.base = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.base)
        for n in ('a', 'b'):
            (self.base / n).mkdir()
            (self.base / n / 'f').write_text('x')

    def test_dry_run_changes_nothing(self):
        self.assertEqual(snapshots.prune(['a'], False, self.base), ['would remove a'])
        self.assertTrue((self.base / 'a').exists())

    def test_apply_removes_only_named(self):
        snapshots.prune(['a'], True, self.base)
        self.assertEqual(snapshots.names(self.base), ['b'])

    def test_kept_is_refused_until_unkept(self):
        snapshots.set_keep('a', True, self.base)
        self.assertIn('refused a', snapshots.prune(['a'], True, self.base)[0])
        self.assertTrue((self.base / 'a').exists())
        snapshots.set_keep('a', False, self.base)
        snapshots.prune(['a'], True, self.base)
        self.assertEqual(snapshots.names(self.base), ['b'])

    def test_unknown_and_traversal_refused(self):
        self.assertIn('refused', snapshots.prune(['../a', 'zzz'], True, self.base)[0])
        self.assertIn('refused', snapshots.prune(['../a', 'zzz'], True, self.base)[1])

    def test_symlink_not_a_snapshot(self):
        (self.base / 'link').symlink_to(self.base / 'a')
        self.assertNotIn('link', snapshots.names(self.base))


if __name__ == '__main__':
    unittest.main()
