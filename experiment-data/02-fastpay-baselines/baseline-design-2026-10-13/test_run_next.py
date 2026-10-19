import importlib.util
import os
import pathlib
import signal
import subprocess
import sys
import tempfile
import unittest


MODULE = pathlib.Path(__file__).with_name("run_next.py")
SPEC = importlib.util.spec_from_file_location("run_next", MODULE)
run_next = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(run_next)


def output_id(value):
    return [value] * 32


def chain_report():
    return {
        "Requested": 2, "Error": "", "FastChainMS": 12.0,
        "Hops": [
            {"Hop": 1, "Input": output_id(1), "Output": output_id(2), "CertificateInput": False,
             "FastMS": 2.0, "ReadyUnixNS": 10_000_000, "SentUnixNS": 8_000_000,
             "OutputData": {"Asset": 1, "Amount": 100}},
            {"Hop": 2, "Input": output_id(2), "Output": output_id(3), "CertificateInput": True,
             "FastMS": 3.0, "ReadyUnixNS": 20_000_000, "SentUnixNS": 17_000_000,
             "OutputData": {"Asset": 1, "Amount": 100}},
        ],
    }


def bench_report():
    return {"Summary": {"count": 2, "failed": 0}, "Samples": [
        {"Index": 0, "Fact": "a" * 64, "Outcome": "COMPLETE", "FastMS": 1.0, "SentUnixNS": 1_000_000_000,
         "FastUnixNS": 1_001_000_000, "DispatchLagMS": 0.0, "Error": ""},
        {"Index": 1, "Fact": "b" * 64, "Outcome": "COMPLETE", "FastMS": 2.0, "SentUnixNS": 1_500_000_000,
         "FastUnixNS": 1_502_000_000, "DispatchLagMS": 0.2, "Error": ""},
    ]}


class NativeReportTests(unittest.TestCase):
    def test_chain_requires_actual_prior_output_as_next_input(self):
        report = chain_report()
        self.assertEqual(run_next.chain_result(report, 2)["samples_ms"], [2.0, 3.0])
        report["Hops"][1]["Input"] = output_id(9)
        with self.assertRaisesRegex(ValueError, "prior output"):
            run_next.chain_result(report, 2)

    def test_chain_keeps_verified_receipts_when_later_closure_fails(self):
        report = chain_report()
        report["Error"] = "member observer timed out"
        result = run_next.chain_result(report, 2)
        self.assertEqual(result["completed"], 2)
        self.assertEqual(result["closure_status"], "incomplete")

    def test_chain_rejects_missing_amount_asset_output_and_bad_timestamp(self):
        for field, value in (("Amount", None), ("Asset", 2)):
            report = chain_report()
            report["Hops"][0]["OutputData"][field] = value
            with self.assertRaises(ValueError):
                run_next.chain_result(report, 2)
        report = chain_report()
        report["Hops"][1]["Output"] = output_id(0)
        with self.assertRaises(ValueError):
            run_next.chain_result(report, 2)
        report = chain_report()
        report["Hops"][1]["ReadyUnixNS"] = report["Hops"][1]["SentUnixNS"]
        with self.assertRaises(ValueError):
            run_next.chain_result(report, 2)

    def test_independent_keeps_receipt_when_later_closure_fails_and_reports_sends(self):
        report = bench_report()
        report["Samples"][1]["Outcome"] = "UNKNOWN"
        report["Samples"][1]["Error"] = "public closure timed out"
        report["Summary"]["failed"] = 1
        result = run_next.independent_result(report, 2)
        self.assertEqual(result["samples_ms"], [1.0, 2.0])
        self.assertEqual(result["closure_status"], "incomplete")
        self.assertEqual(result["sent_unix_ns"], [1_000_000_000, 1_500_000_000])
        self.assertEqual(result["dispatch_lag_ms"], [0.0, 0.2])
        self.assertEqual(result["realized_send_rate_per_s"], 2.0)

    def test_independent_rejects_send_failure_without_receipt(self):
        report = bench_report()
        report["Samples"][1]["FastUnixNS"] = 0
        report["Samples"][1]["Error"] = "INVALID_REQUEST"
        with self.assertRaisesRegex(ValueError, "receipt"):
            run_next.independent_result(report, 2)

    def test_independent_rejects_duplicate_receipt_fact(self):
        report = bench_report()
        report["Samples"][1]["Fact"] = report["Samples"][0]["Fact"]
        with self.assertRaisesRegex(ValueError, "identity"):
            run_next.independent_result(report, 2)

    def test_measurement_command_retains_nonzero_exit_and_log(self):
        with tempfile.TemporaryDirectory() as folder:
            log = pathlib.Path(folder) / "client.log"
            code = run_next.run_command([sys.executable, "-c", "import sys; print('closure failed'); sys.exit(7)"], log, os.environ.copy(), check=False)
            self.assertEqual(code, 7)
            self.assertIn("closure failed", log.read_text())

    @unittest.skipUnless(sys.platform == "linux", "Linux process groups")
    def test_stop_lab_joins_its_child_process_group(self):
        parent = subprocess.Popen([sys.executable, "-c", "import subprocess,time; subprocess.Popen(['sleep','30']); print('ready',flush=True); time.sleep(30)"],
                                  stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, start_new_session=True, text=True)
        try:
            self.assertEqual(parent.stdout.readline().strip(), "ready")
            run_next.stop_lab(parent)
            self.assertFalse(run_next.process_group_alive(parent.pid))
        finally:
            parent.stdout.close()
            try:
                os.killpg(parent.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            parent.wait()


if __name__ == "__main__":
    unittest.main()
