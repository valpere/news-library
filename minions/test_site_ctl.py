import os
from pathlib import Path
import shutil
import tempfile
import unittest

import site_ctl


class SiteCtl(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.dir)
        self.binary = self.dir / 'srv'
        shutil.copy(shutil.which('sleep'), self.binary)
        self.state = self.dir / 'state'

    def test_start_stop_cycle(self):
        site_ctl.start(['300'], self.state, self.binary)
        pid = site_ctl.status(self.state, self.binary)
        self.assertTrue(pid)
        self.assertEqual(site_ctl.start(['300'], self.state, self.binary), 'already running')
        self.assertIn('stopped', site_ctl.stop(self.state, self.binary))
        self.assertIsNone(site_ctl.status(self.state, self.binary))

    def test_foreign_pid_is_not_ours(self):
        self.state.mkdir()
        (self.state / 'pid').write_text(f'{os.getpid()}\n')
        self.assertIsNone(site_ctl.status(self.state, self.binary))
        self.assertEqual(site_ctl.stop(self.state, self.binary), 'not running')

    def test_missing_binary(self):
        self.assertIn('binary missing', site_ctl.start([], self.state, self.dir / 'none'))


if __name__ == '__main__':
    unittest.main()
