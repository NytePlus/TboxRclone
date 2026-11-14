"""Protect the independent performance gate from fast-but-corrupt and slow results."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1]/'compare-upload-performance.py'

class PerformanceComparisonTest(unittest.TestCase):
    def run_case(self, duration, corrupt=False, mismatch=False):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            api={'pass':True,'state':'Committed','bytes':256,'sha256':'a'*64,'total_seconds':10}
            webdav={'passed':True,'source_unchanged':True,'size':256,'sha256':('b' if mismatch else 'a')*64,
                    'cases':[{'mode':mode,'passed':True,'committed':True,'cloud_matches':not corrupt,'total_seconds':duration} for mode in ['length','chunked']]}
            (root/'api.json').write_text(json.dumps(api))
            (root/'webdav.json').write_text(json.dumps(webdav))
            result=subprocess.run(['python3',str(SCRIPT),str(root/'api.json'),str(root/'webdav.json'),str(root/'result.json')],capture_output=True,text=True)
            report=json.loads((root/'result.json').read_text()) if (root/'result.json').exists() else None
            return result.returncode,report

    def test_verified_but_slow_is_failure(self):
        code,report=self.run_case(20)
        self.assertEqual(code,1)
        self.assertFalse(report['passed'])

    def test_fast_corrupt_or_different_payload_is_not_evidence(self):
        for flags in [{'corrupt':True},{'mismatch':True}]:
            with self.subTest(flags=flags):
                code,report=self.run_case(1,**flags)
                self.assertEqual(code,2)
                self.assertIsNone(report)

    def test_eighty_percent_boundary(self):
        code,report=self.run_case(12.5)
        self.assertEqual(code,0)
        self.assertTrue(report['passed'])

if __name__ == '__main__':
    unittest.main()
