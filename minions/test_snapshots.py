from pathlib import Path
import shutil
import tempfile
import unittest

import sqlite3
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

    def make_data(self):
        data = self.base / 'live'
        (data / 'texts' / '00').mkdir(parents=True)
        (data / 'texts' / '00' / 't.txt').write_text('text')
        con = sqlite3.connect(data / 'library.sqlite')
        con.execute('PRAGMA user_version=15')
        con.execute('CREATE TABLE t(x)')
        con.commit()
        con.close()
        return data

    def test_create_copies_db_and_hardlinks_files(self):
        data = self.make_data()
        name = snapshots.create('before-test', data, self.base)
        snap = self.base / name
        self.assertEqual(snapshots.schema(snap), 15)
        self.assertEqual((snap / 'texts/00/t.txt').stat().st_ino, (data / 'texts/00/t.txt').stat().st_ino)
        self.assertTrue((snap / 'snapshot.json').exists())
        self.assertIn(name, snapshots.names(self.base))

    def test_create_rejects_bad_purpose_and_missing_db(self):
        with self.assertRaises(ValueError):
            snapshots.create('../x', self.make_data(), self.base)
        with self.assertRaises(ValueError):
            snapshots.create('ok', self.base / 'a', self.base)

    def test_candidates_respect_keep_last_age_and_keep(self):
        import time
        now = time.time() + 40 * 86400
        snapshots.set_keep('a', True, self.base)
        self.assertEqual(snapshots.candidates(self.base, 30, 0, now), ['b'])
        self.assertEqual(snapshots.candidates(self.base, 30, 5, now), [])


if __name__ == '__main__':
    unittest.main()
