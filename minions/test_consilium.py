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

    def test_coverage_and_reference_validation(self):
        with tempfile.TemporaryDirectory(dir=ROOT/'tmp') as d:
            path=Path(d); (path/'README.md').write_text('first\nsecond\n')
            report={'summary':'Reviewed', 'scores':dict.fromkeys(council.CRITERIA,3),
                    'reviewed_files':['README.md'],'findings':[], 'limitations':[]}
            self.assertEqual(council.validate_report(json.dumps(report),['README.md'],path),report)
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
