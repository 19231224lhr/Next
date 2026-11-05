package protocol

import (
	"crypto/ed25519"
	"testing"
)

func TestReserveEncodingBindsAuthorization(t *testing.T) {
	seed := Digest("reserve-test")
	key := ed25519.NewKeyFromSeed(seed[:])
	c := ReserveIncrease{Network: Digest("network"), Organization: Digest("organization"), Grant: Digest("grant"), Key: ResourceKey{Kind: ResourceCAL, Account: Digest("account"), Version: 1}, Previous: 3600, Amount: 300}
	c.Sign(key)
	raw, err := c.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeReserveIncrease(raw)
	if err != nil || decoded != c {
		t.Fatal("round trip", err)
	}
	if _, err = DecodeReserveIncrease(append(raw, 0)); err == nil {
		t.Fatal("trailing bytes")
	}
	raw[10] ^= 1
	if _, err = DecodeReserveIncrease(raw); err == nil {
		t.Fatal("unsigned network change")
	}
	c.Amount = 0
	c.Sign(key)
	if c.Verify() == nil {
		t.Fatal("zero increment")
	}
	c.Amount = ^uint64(0)
	c.Sign(key)
	if c.Verify() == nil {
		t.Fatal("overflow")
	}
}
