import hashlib
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

import corpus_audit


class CorpusAuditTests(unittest.TestCase):
    def setUp(self):
        # Test artifacts follow the same tmp/ rule as private corpus reports.
        (corpus_audit.ROOT / 'tmp').mkdir(exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=corpus_audit.ROOT / 'tmp')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.data = self.root / 'data'
        self.data.mkdir()
        self.db = self.data / 'library.sqlite'
        with sqlite3.connect(self.db) as db:
            db.executescript('''
                PRAGMA user_version=1;
                CREATE TABLE items(id PRIMARY KEY,title,selected_snapshot);
                CREATE VIEW active_items AS SELECT * FROM items;
                CREATE TABLE source_versions(id PRIMARY KEY,item_id,text_path,hash,completeness,warning);
                CREATE TABLE generated_artifacts(id PRIMARY KEY,item_id,snapshot_id,markdown_path,hash);
                CREATE TABLE article_knowledge(id PRIMARY KEY,item_id,snapshot_id,digest_id,markdown_path,hash);
                CREATE TABLE connection_versions(id PRIMARY KEY,note_id,revision,title,markdown_path,hash);
                CREATE TABLE connection_members(version_id,item_id,snapshot_id,digest_id);
            ''')
            source = self.blob('texts/source.txt', 'Copyright only')
            digest = self.blob('digests/digest.md', 'This digest is longer than the source')
            db.execute('INSERT INTO items VALUES(1,?,1)', ('Synthetic short source',))
            db.execute('INSERT INTO source_versions VALUES(1,1,?,?,?,?)', (*source, 'unknown', 'fixture'))
            db.execute('INSERT INTO generated_artifacts VALUES(1,1,1,?,?)', digest)

    def blob(self, relative, content):
        p = self.data / relative
        p.parent.mkdir(exist_ok=True)
        p.write_text(content)
        return relative, hashlib.sha256(content.encode()).hexdigest()

    def test_read_only_flags_without_mutations(self):
        before = {p: p.read_bytes() for p in self.data.rglob('*') if p.is_file()}
        report = corpus_audit.audit(self.data)
        self.assertEqual(0, report['summary']['integrity_problems'])
        self.assertEqual(2, report['summary']['verified_primary_references'])
        self.assertEqual({'short_source', 'digest_longer_than_source', 'missing_current_metadata'},
                         {s['kind'] for s in report['signals']})
        self.assertEqual(before, {p: p.read_bytes() for p in self.data.rglob('*') if p.is_file()})

    def test_corrupt_primary_and_foreign_ownership(self):
        (self.data / 'texts/source.txt').write_text('different')
        with sqlite3.connect(self.db) as db:
            db.execute('UPDATE generated_artifacts SET item_id=2')
        report = corpus_audit.audit(self.data)
        self.assertIn('primary_file', {p['kind'] for p in report['problems']})
        self.assertIn('digest_ownership', {p['kind'] for p in report['problems']})

    def test_no_symlink_or_path_escape_reads(self):
        outside = self.root / 'outside'
        outside.write_text('secret fixture')
        (self.data / 'texts/source.txt').unlink()
        (self.data / 'texts/source.txt').symlink_to(outside)
        report = corpus_audit.audit(self.data)
        self.assertEqual('symlink refused', report['problems'][0]['details'])
        with self.assertRaises(ValueError):
            corpus_audit.local_path(self.data, '../outside')
        with self.assertRaises(ValueError):
            corpus_audit.local_path(self.data, outside)
        directory_link = self.root / 'data-link'
        directory_link.symlink_to(self.data, target_is_directory=True)
        with self.assertRaises(ValueError):
            corpus_audit.audit(directory_link)

    def test_no_creation_or_migration(self):
        with self.assertRaises(ValueError):
            corpus_audit.audit(self.root / 'missing')
        self.assertFalse((self.root / 'missing').exists())
        with sqlite3.connect(self.db) as db:
            db.execute('PRAGMA user_version=14')
        with self.assertRaises(ValueError):
            corpus_audit.audit(self.data)
        with sqlite3.connect(self.db) as db:
            self.assertEqual(14, db.execute('PRAGMA user_version').fetchone()[0])

    def test_output_private_new_tmp_directory(self):
        # Isolate the output policy without publishing real corpus data.
        with patch.object(corpus_audit, 'ROOT', self.root):
            out = self.root / 'tmp/report'
            corpus_audit.save_report(corpus_audit.audit(self.data), out)
            self.assertEqual(0o700, out.stat().st_mode & 0o777)
            self.assertEqual(1, json.loads((out / 'report.json').read_text())['summary']['active_items'])
            with self.assertRaises(FileExistsError):
                corpus_audit.save_report({}, out)
            with self.assertRaises(ValueError):
                corpus_audit.save_report({}, self.data / 'report')
            link = self.root / 'tmp/link'
            link.symlink_to(self.data, target_is_directory=True)
            with self.assertRaises(ValueError):
                corpus_audit.save_report({}, link / 'report')

    def test_current_revisions_and_historical_sources(self):
        with sqlite3.connect(self.db) as db:
            source = self.blob('texts/second.txt', 'second source')
            note = self.blob('digests/note.md', 'a synthetic connection')
            db.execute('INSERT INTO source_versions VALUES(2,1,?,?,?,?)', (*source, 'unknown', ''))
            db.execute('INSERT INTO source_versions VALUES(3,2,?,?,?,?)', (*source, 'unknown', ''))
            db.execute('INSERT INTO items VALUES(2,?,3)', ('Second item',))
            db.execute('UPDATE items SET selected_snapshot=2 WHERE id=1')
            for vid, nid, revision in [(1,1,1), (2,1,2), (3,2,1)]:
                db.execute('INSERT INTO connection_versions VALUES(?,?,?,?,?,?)',
                           (vid,nid,revision,'Shared title',*note))
                db.execute('INSERT INTO connection_members VALUES(?,?,?,NULL)', (vid,1,1 if vid == 1 else 2))
                db.execute('INSERT INTO connection_members VALUES(?,?,?,NULL)', (vid,2,3))
        report = corpus_audit.audit(self.data)
        self.assertEqual(0, report['summary']['integrity_problems'])
        self.assertEqual([2,3], [n['version_id'] for n in report['connections']])
        self.assertNotIn('connection_historical_source', {s['kind'] for s in report['signals']})
        repeated = [s for s in report['signals'] if s['kind'] == 'repeated_connection_title']
        self.assertEqual([2,3], repeated[0]['version_ids'])
        with sqlite3.connect(self.db) as db:
            db.execute('UPDATE connection_members SET snapshot_id=1 WHERE version_id=2 AND item_id=1')
        report = corpus_audit.audit(self.data)
        historical = [s for s in report['signals'] if s['kind'] == 'connection_historical_source']
        self.assertEqual(1, len(historical))
        self.assertEqual(2, historical[0]['version_id'])
        self.assertEqual(0, report['summary']['integrity_problems'])


if __name__ == '__main__':
    unittest.main()
