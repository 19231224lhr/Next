package config

import (
	ct "github.com/cometbft/cometbft/types"
	"testing"
)

func TestSharedGenesisBlockCapacity(t *testing.T) {
	for _, limit := range []int64{0, 32768, -2} {
		p, err := (Network{ConsensusBlockBytes: limit}).ConsensusParams()
		if limit < 0 {
			if err == nil {
				t.Fatal("invalid block limit accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		want := limit
		if want == 0 {
			want = ct.DefaultConsensusParams().Block.MaxBytes
		}
		if p.Block.MaxBytes != want {
			t.Fatalf("got %d want %d", p.Block.MaxBytes, want)
		}
	}
}
