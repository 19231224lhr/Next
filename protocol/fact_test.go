package protocol

import (
	"bytes"
	"testing"
)

func TestFinalFactCanonicalAndBounded(t *testing.T) {
	f := FinalFact{Kind: FactOutputCreated, Key: Digest("key", nil), Revision: 1, Network: Digest("network", nil), Payload: []byte{1, 2, 3}}
	b, e := f.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	g, e := DecodeFact(b)
	if e != nil || g.ID() != f.ID() {
		t.Fatal("fact identity")
	}
	g.Payload[0] ^= 1
	if bytes.Equal(g.Payload, f.Payload) {
		t.Fatal("decoder aliases caller")
	}
	if _, e := DecodeFact(append(b, 0)); e == nil {
		t.Fatal("trailing bytes")
	}
	f.Revision = 0
	if _, e := f.MarshalBinary(); e == nil {
		t.Fatal("revision zero")
	}
}
