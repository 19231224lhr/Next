package protocol

import "testing"

func TestE8DescriptorCounters(t *testing.T) {
	old := descriptorMetricsEnabled
	descriptorMetricsEnabled = true
	defer func() { descriptorMetricsEnabled = old }()
	d := cacheDescriptor(17)
	verifiedReceiveDescriptors.Remove(d)
	before := DescriptorMetrics()
	if e := d.Verify(d.Network); e != nil {
		t.Fatal(e)
	}
	if e := d.Verify(d.Network); e != nil {
		t.Fatal(e)
	}
	after := DescriptorMetrics()
	if after.Queries-before.Queries != 2 || after.Hits-before.Hits != 1 || after.Verifications-before.Verifications != 1 {
		t.Fatalf("bad counters %+v %+v", before, after)
	}
}
