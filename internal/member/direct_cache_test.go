package member

import (
	"crypto/ed25519"
	"sync"
	"testing"
	"utxo/protocol"
)

func cacheCertificate(n byte) (protocol.OrgConfig, protocol.OutputCertificate) {
	net := protocol.Digest("cache-net")
	cfg := protocol.OrgConfig{Network: net, Org: protocol.Digest("cache-org"), Epoch: 1}
	var keys [4]ed25519.PrivateKey
	for i := range keys {
		seed := protocol.Digest("cache-key", []byte{byte(i)})
		keys[i] = ed25519.NewKeyFromSeed(seed[:])
		copy(cfg.Members[i][:], keys[i][32:])
	}
	out := protocol.Output{Asset: protocol.AssetCAL, Amount: 100, Recipient: protocol.NewDescriptor(net, protocol.Route{Kind: protocol.OrgRoute, Org: cfg.Org}, keys[0])}
	s := protocol.OutputSummary{Network: net, Tx: protocol.TxID(protocol.Digest("cache-tx", []byte{n})), Issuer: cfg.Org, Config: cfg.Hash(), Epoch: 1,
		Rules:     protocol.RuleIDs{Fee: protocol.Digest("f"), Work: protocol.Digest("w"), Accounting: protocol.Digest("a")},
		Outputs:   []protocol.OutputCommitment{{Digest: protocol.OutputDigest(out), Amount: 100}},
		Admission: protocol.AdmissionVector{{Key: protocol.ResourceKey{Kind: protocol.ResourceCAL, Account: cfg.Org, Version: 1}, Cap: 100}}, Grants: []protocol.Hash{protocol.Digest("grant")}}
	c := protocol.OutputCertificate{Summary: s, QC: protocol.SpendQC{Fact: s.Fact()}}
	for i := 0; i < 3; i++ {
		c.QC.Votes = append(c.QC.Votes, protocol.SignSpend(c.QC.Fact, uint16(i), keys[i]))
	}
	return cfg, c
}

func TestDirectCertificateCacheConcurrentEviction(t *testing.T) {
	cfg, first := cacheCertificate(1)
	_, second := cacheCertificate(2)
	cache := newDirectCertificateCache(1, cfg.Hash())
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				cert := first
				if (i+j)%2 == 0 {
					cert = second
				}
				if _, err := cache.verify(cert, cfg, true); err != nil {
					errs <- err
					return
				}
				ref, _ := protocol.CertificateReference(cert)
				if raw, ok := cache.resolve(ref); ok {
					decoded, err := protocol.DecodeOutputCertificate(raw)
					if err != nil {
						errs <- err
						return
					}
					if err = decoded.Verify(cfg); err != nil {
						errs <- err
						return
					}
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if len(cache.entries) > 1 || len(cache.order) > 1 {
		t.Fatal("concurrent inserts exceeded capacity")
	}
}

func TestDirectCertificateCacheExactBoundedAndOwned(t *testing.T) {
	cfg, c := cacheCertificate(1)
	cache := newDirectCertificateCache(1, cfg.Hash())
	if _, err := cache.verify(c, cfg, true); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.verify(c, cfg, true); err != nil {
		t.Fatal(err)
	}
	if cache.stats().VerificationHits != 1 {
		t.Fatal("no verification reuse")
	}
	ref, _ := protocol.CertificateReference(c)
	raw, ok := cache.resolve(ref)
	if !ok {
		t.Fatal("missing stored certificate")
	}
	raw[0] ^= 1
	raw, ok = cache.resolve(ref)
	if !ok {
		t.Fatal("missing certificate")
	}
	if _, err := protocol.DecodeOutputCertificate(raw); err != nil {
		t.Fatal("mutable cache bytes:", err)
	}
	c.QC.Votes[0].Signature[0] ^= 1
	if _, err := cache.verify(c, cfg, true); err == nil {
		t.Fatal("forged signature hit cache")
	}
	_, next := cacheCertificate(2)
	if _, err := cache.verify(next, cfg, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.resolve(ref); ok {
		t.Fatal("capacity bound ignored")
	}
	wrong := cfg
	wrong.Epoch++
	if _, err := cache.verify(next, wrong, true); err == nil {
		t.Fatal("wrong trust configuration reused")
	}
}

func TestDirectCertificateCacheCanDisableVerificationReuse(t *testing.T) {
	cfg, c := cacheCertificate(1)
	cache := newDirectCertificateCache(8, cfg.Hash())
	for i := 0; i < 2; i++ {
		if _, err := cache.verify(c, cfg, false); err != nil {
			t.Fatal(err)
		}
	}
	if cache.stats().VerificationHits != 0 || cache.stats().VerificationMisses != 2 {
		t.Fatal(cache.stats())
	}
	ref, _ := protocol.CertificateReference(c)
	if _, ok := cache.resolve(ref); !ok {
		t.Fatal("reference-only cache not populated")
	}
}
