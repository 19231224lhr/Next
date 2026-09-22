package protocol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"sync"
	"testing"
)

func cacheDescriptor(n int) ReceiveDescriptor {
	seed := sha256.Sum256([]byte("descriptor cache test"))
	key := ed25519.NewKeyFromSeed(seed[:])
	network := Digest("descriptor-cache-network", nil)
	org := Digest("descriptor-cache-route", []byte{byte(n), byte(n >> 8), byte(n >> 16)})
	return NewDescriptor(network, Route{Kind: OrgRoute, Org: org}, key)
}

func TestReceiveDescriptorCacheIsExactAndBounded(t *testing.T) {
	d := cacheDescriptor(1)
	if err := d.Verify(d.Network); err != nil {
		t.Fatal(err)
	}
	if !verifiedReceiveDescriptors.Contains(d) {
		t.Fatal("successful signature was not cached")
	}
	changes := []func(*ReceiveDescriptor){
		func(x *ReceiveDescriptor) { x.Wire++ },
		func(x *ReceiveDescriptor) { x.Network[0] ^= 1 },
		func(x *ReceiveDescriptor) { x.Owner[0] ^= 1 },
		func(x *ReceiveDescriptor) { x.Route.Org[0] ^= 1 },
		func(x *ReceiveDescriptor) { x.Route.Kind = 255 },
		func(x *ReceiveDescriptor) { x.Signature[0] ^= 1 },
	}
	for i, change := range changes {
		bad := d
		change(&bad)
		if err := bad.Verify(bad.Network); err == nil {
			t.Fatalf("mutation %d bypassed signature/rule checks", i)
		}
		if verifiedReceiveDescriptors.Contains(bad) {
			t.Fatal("invalid descriptor cached")
		}
	}
	wrong := d.Network
	wrong[0] ^= 1
	if d.Verify(wrong) == nil {
		t.Fatal("cached signature bypassed expected network")
	}
	for i := 0; i < 1100; i++ {
		x := cacheDescriptor(i + 100)
		if x.Verify(x.Network) != nil {
			t.Fatal("valid descriptor rejected")
		}
	}
	if verifiedReceiveDescriptors.Len() > 1024 {
		t.Fatal("cache exceeded capacity")
	}
	if err := d.Verify(d.Network); err != nil {
		t.Fatal("eviction changed verification result")
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if d.Verify(d.Network) != nil {
					t.Error("concurrent verification failed")
				}
			}
		}()
	}
	wg.Wait()
}

func BenchmarkReceiveDescriptorReuse(b *testing.B) {
	for _, count := range []int{2, 2048} {
		name := "reuse2"
		if count > 2 {
			name = "churn2048"
		}
		b.Run(name, func(b *testing.B) {
			ds := make([]ReceiveDescriptor, count)
			for i := range ds {
				ds[i] = cacheDescriptor(i + 10000)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d := ds[i%count]
				if d.Verify(d.Network) != nil {
					b.Fatal("verify failed")
				}
			}
		})
	}
}
