import unittest
from analyze import closed

class TimingTest(unittest.TestCase):
    def test_closure_waits_for_both_observation_paths(self):
        self.assertEqual(closed({'PublicNS':200,'MemberNS':[100,110,120,130]}),200)
        self.assertEqual(closed({'PublicNS':100,'MemberNS':[200,210,220,230]}),230)
        self.assertEqual(closed({'PublicNS':0,'MemberNS':[200,210,220,230]}),0)
        self.assertEqual(closed({'PublicNS':200,'MemberNS':[100,110,0,130]}),0)

if __name__=='__main__':unittest.main()
