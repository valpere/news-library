"""Offline council boundaries; no external agents or models."""
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('council', ROOT/'minions/consilium/run.py')
council = importlib.util.module_from_spec(spec)
spec.loader.exec_module(council)


def archive(entries):
    buffer=io.BytesIO()
    with tarfile.open(fileobj=buffer,mode='w') as t:
        for name,content in entries.items():
            data=content.encode()
            entry=tarfile.TarInfo(name); entry.size=len(data)
            t.addfile(entry,io.BytesIO(data))
    return buffer.getvalue()


class CouncilTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        (ROOT/'tmp').mkdir(exist_ok=True)

    def test_snapshot_rejects_runtime_paths_and_traversal(self):
        for name in ['../outside','.env','.env.private','data/private.md']:
            with tempfile.TemporaryDirectory(dir=ROOT/'tmp') as d:
                with self.assertRaises(ValueError):
                    council.extract_snapshot(archive({name:'secret'}),Path(d)/'snapshot')
        with tempfile.TemporaryDirectory(dir=ROOT/'tmp') as d:
            council.extract_snapshot(archive({'.env.example':'public'}),Path(d)/'snapshot')
            before=council.fingerprints(Path(d)/'snapshot')
            (Path(d)/'snapshot'/'link').symlink_to('/outside')
            self.assertNotEqual(before,council.fingerprints(Path(d)/'snapshot'))

    def test_snapshot_skips_relative_in_tree_symlink_but_rejects_escapes(self):
        def with_link(target):
            buffer = io.BytesIO()
            with tarfile.open(fileobj=buffer, mode='w') as t:
                entry = tarfile.TarInfo('CLAUDE.md'); entry.type = tarfile.SYMTYPE; entry.linkname = target
                t.addfile(entry)
            return buffer.getvalue()
        with tempfile.TemporaryDirectory(dir=ROOT/'tmp') as d:
            council.extract_snapshot(with_link('AGENTS.md'), Path(d)/'snapshot')
            self.assertFalse((Path(d)/'snapshot'/'CLAUDE.md').exists())
        for target in ['/etc/passwd', '../outside']:
            with tempfile.TemporaryDirectory(dir=ROOT/'tmp') as d:
                with self.assertRaises(ValueError):
                    council.extract_snapshot(with_link(target), Path(d)/'snapshot')

    def test_diagnose_classifies_transient_provider_errors(self):
        overloaded = json.dumps({'type': 'error', 'error': {'name': 'UnknownError', 'data': {'message': 'Streaming response failed: [503] Upstream error from Nvidia: Service temporarily overloaded'}}})
        detail, transient = council.diagnose('step noise\n' + overloaded + '\n')
        self.assertTrue(transient)
        self.assertIn('503', detail)
        kiro = json.dumps({'type': 'runError', 'data': {'stage': 'prompt', 'message': 'Internal error'}})
        self.assertTrue(council.diagnose(kiro)[1])
        auth = json.dumps({'type': 'error', 'error': {'data': {'message': 'Invalid API key'}}})
        self.assertEqual(council.diagnose(auth), ('Invalid API key', False))
        self.assertEqual(council.diagnose('{"type":"text"}\nnot json'), ('', False))

    def test_retry_only_transient_failures_up_to_limit(self):
        calls = []
        def run(attempt):
            calls.append(attempt)
            return {'status': 'failed', 'transient': attempt < 2}
        result = council.retry(run, retries=2, sleep=lambda s: None)
        self.assertEqual(calls, [0, 1, 2])
        self.assertEqual(result['attempts'], 3)
        calls.clear()
        result = council.retry(lambda a: calls.append(a) or {'status': 'failed', 'transient': False}, retries=2, sleep=lambda s: None)
        self.assertEqual(calls, [0])
        self.assertEqual(result['attempts'], 1)
        calls.clear()
        done = council.retry(lambda a: calls.append(a) or {'status': 'complete'}, retries=2, sleep=lambda s: None)
        self.assertEqual((calls, done['attempts']), ([0], 1))

    def test_coverage_and_reference_validation(self):
        with tempfile.TemporaryDirectory(dir=ROOT/'tmp') as d:
            path=Path(d); (path/'README.md').write_text('first\nsecond\n')
            report={'summary':'Reviewed', 'scores':dict.fromkeys(council.CRITERIA,3),
                    'reviewed_files':['README.md'],'findings':[], 'limitations':[]}
            self.assertEqual(council.validate_report(json.dumps(report),['README.md'],path),report)
            wrapped = 'draft\n' + json.dumps(report) + '\n```json\n' + json.dumps(report) + '\n```'
            self.assertEqual(council.validate_report(wrapped,['README.md'],path),report)
            with self.assertRaises(ValueError):
                council.validate_report(json.dumps(report) + json.dumps(report),['README.md'],path)
            report['scores']['clarity']=True
            with self.assertRaises(ValueError): council.validate_report(json.dumps(report),['README.md'],path)
            report['scores']['clarity']=3
            finding={'severity':'major','file':'../outside','line':1,
                     'trigger':'x','impact':'x','evidence':'x','suggested_fix':'x'}
            report['findings']=[finding]
            with self.assertRaises(ValueError): council.validate_report(json.dumps(report),['README.md'],path)
            finding['file']='README.md';finding['line']=3
            with self.assertRaises(ValueError): council.validate_report(json.dumps(report),['README.md'],path)
            with self.assertRaises(ValueError): council.validate_report('', ['README.md'],path)

    def test_codex_cannot_be_selected_and_live_opt_in_required(self):
        for args in [[], ['--run-agents','--agents','codex']]:
            result=subprocess.run(['python3',str(ROOT/'minions/consilium/run.py'),*args],capture_output=True,text=True)
            self.assertEqual(result.returncode,2)
        self.assertNotIn('codex',council.AGENTS)

    def test_cli_failure_is_not_empty_approval(self):
        with tempfile.TemporaryDirectory(dir=ROOT/'tmp') as d, patch.dict(council.AGENTS,{'fixture':('none',['/bin/false'])}):
            result=council.run_agent('fixture',archive({'README.md':'public'}),'prompt',['README.md'],Path(d)/'agent',30)
            self.assertEqual(result['status'],'failed')
            self.assertEqual(result['exit_code'],1)
            self.assertNotIn('finding_count',result)

    def test_cli_event_extraction_retains_only_final_answer(self):
        raw=json.dumps({'type':'text','part':{'text':'final'}})
        self.assertEqual(council.report_text('opencode',raw),'final')
        self.assertEqual(council.report_text('kiro-cli',json.dumps({'type':'runFinished','data':{'finalText':'done'}})),'done')
        self.assertEqual(council.report_text('agy',json.dumps({'response':'answer'})),'answer')


if __name__=='__main__': unittest.main()
