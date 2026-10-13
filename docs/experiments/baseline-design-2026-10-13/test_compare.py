import unittest
import pathlib
import subprocess
import sys
import tempfile
from compare import run_managed, summarize_run, verify


class ComparisonChecks(unittest.TestCase):
    def test_rejects_nonfinite_and_wrong_counts(self):
        r={'system':'Next','workload':'chain','completed':2,'samples_ms':[1,float('nan')]}
        with self.assertRaises(ValueError): verify(r,'Next','chain',2)
        r['samples_ms']=[1,2]
        with self.assertRaises(ValueError): verify(r,'Next','chain',3)

    def test_uses_per_run_percentiles_and_chain_endpoint(self):
        r={'system':'Next','workload':'chain','completed':3,'samples_ms':[3,1,2],'chain_total_ms':9}
        verify(r,'Next','chain',3)
        s=summarize_run(r)
        self.assertEqual(s['p50_ms'],2)
        self.assertEqual(s['p95_ms'],3)
        self.assertEqual(s['chain_total_ms'],9)

    def test_timeout_gives_adapter_time_to_clean_up(self):
        with tempfile.TemporaryDirectory() as d:
            proof=pathlib.Path(d)/'stopped'
            code='import signal,time,pathlib,sys\n' + \
                'def stop(*args):\n pathlib.Path(sys.argv[1]).write_text("cleaned");sys.exit(0)\n' + \
                'signal.signal(signal.SIGTERM,stop)\ntime.sleep(30)\n'
            with self.assertRaises(subprocess.TimeoutExpired):
                run_managed([sys.executable,'-c',code,str(proof)],timeout=.3)
            self.assertEqual(proof.read_text(),'cleaned')


if __name__=='__main__': unittest.main()
