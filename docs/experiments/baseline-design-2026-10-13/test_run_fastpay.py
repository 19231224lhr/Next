import socket
import unittest
from run_fastpay import tcp_ready

class ReadinessChecks(unittest.TestCase):
    def test_readiness_requires_listener_not_only_reserved_address(self):
        with socket.socket() as s:
            s.bind(('127.0.0.1',0))
            c={'host':'127.0.0.1','base_port':s.getsockname()[1]}
            self.assertFalse(tcp_ready(c))
            s.listen()
            self.assertTrue(tcp_ready(c))

if __name__=='__main__':unittest.main()
