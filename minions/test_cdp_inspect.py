import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import cdp_inspect


class CDPInspectTests(unittest.TestCase):
    def test_origin_scope_and_explicit_path_prefix(self):
        target='https://openrouter.ai/settings/privacy'
        self.assertTrue(cdp_inspect.matching_url(target+'?secret=hidden', target))
        self.assertTrue(cdp_inspect.matching_url('https://openrouter.ai:443/settings/privacy',target))
        for other in ['https://openrouter.ai.evil/settings/privacy', 'https://evil.test/settings/privacy', 'http://openrouter.ai/settings/privacy', target+'-extra', 'https://openrouter.ai:9443/settings/privacy']:
            self.assertFalse(cdp_inspect.matching_url(other,target))
        self.assertTrue(cdp_inspect.matching_url(target,'https://openrouter.ai/settings/'))
        for url in ['https://user:password@openrouter.ai/',target+'?key=secret','file:///private/file']:
            with self.assertRaises(ValueError):cdp_inspect.checked_url(url)

    def test_no_link_query_credentials_or_key_labels(self):
        self.assertEqual('https://openrouter.ai/settings/privacy',cdp_inspect.public_url('https://user:password@openrouter.ai/settings/privacy?api_key=secret#secret'))
        self.assertIsNone(cdp_inspect.public_url('javascript:readCookie()'))
        self.assertNotIn('fake-private-key',cdp_inspect.clean_text('Key sk-or-v1-fake-private-key'))

    def test_new_private_output_confined_to_tmp(self):
        (cdp_inspect.ROOT/'tmp').mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(dir=cdp_inspect.ROOT/'tmp') as tmp:
            root=Path(tmp)
            with patch.object(cdp_inspect,'ROOT',root):
                output=root/'tmp/report.json'
                cdp_inspect.write_report(output,{'controls':[]})
                self.assertEqual(0o600,output.stat().st_mode&0o777)
                self.assertEqual({'controls':[]},json.loads(output.read_text()))
                with self.assertRaises(FileExistsError):cdp_inspect.write_report(output,{})
                with self.assertRaises(ValueError):cdp_inspect.write_report(root/'other.json',{})
                link=root/'tmp/link';link.symlink_to(root,target_is_directory=True)
                with self.assertRaises(ValueError):cdp_inspect.write_report(link/'report.json',{})


if __name__=='__main__':unittest.main()
