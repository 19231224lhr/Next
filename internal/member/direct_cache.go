package member

import (
	"bytes"
	"errors"
	"sync"
	"utxo/protocol"
)

var ErrCertificateCacheMiss = errors.New("certificate reference unavailable")

type DirectCacheStats struct {
	VerificationHits, VerificationMisses, ReferenceHits, ReferenceMisses, Evictions uint64
}
type directCachedCertificate struct {
	raw     []byte
	outputs protocol.VerifiedOutputs
}
type directCertificateCache struct {
	mu               sync.Mutex
	limit, usedBytes int
	config           protocol.Hash
	entries          map[protocol.Hash]directCachedCertificate
	order            []protocol.Hash
	counts           DirectCacheStats
}

func newDirectCertificateCache(limit int, config protocol.Hash) *directCertificateCache {
	return &directCertificateCache{limit: limit, config: config, entries: make(map[protocol.Hash]directCachedCertificate)}
}

func (c *directCertificateCache) verify(cert protocol.OutputCertificate, cfg protocol.OrgConfig, reuse bool) (protocol.VerifiedOutputs, error) {
	if c == nil || cfg.Hash() != c.config || cert.Summary.Config != c.config {
		return cert.VerifyOutputs(cfg)
	}
	raw, err := cert.MarshalBinary()
	if err != nil {
		return protocol.VerifiedOutputs{}, err
	}
	ref := protocol.Digest("DIRECT_CERTIFICATE_REFERENCE", raw)
	c.mu.Lock()
	entry, found := c.entries[ref]
	if found && reuse {
		c.counts.VerificationHits++
		c.mu.Unlock()
		return entry.outputs, nil
	}
	c.counts.VerificationMisses++
	c.mu.Unlock()
	outputs, err := cert.VerifyOutputs(cfg)
	if err != nil {
		return protocol.VerifiedOutputs{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, found := c.entries[ref]; found {
		return outputs, nil
	}
	const maxBytes = 32 << 20
	if len(raw) > maxBytes || c.limit <= 0 {
		return outputs, nil
	}
	for len(c.entries) >= c.limit || c.usedBytes+len(raw) > maxBytes {
		old := c.order[0]
		c.usedBytes -= len(c.entries[old].raw)
		delete(c.entries, old)
		c.order = c.order[1:]
		c.counts.Evictions++
	}
	c.entries[ref] = directCachedCertificate{raw: raw, outputs: outputs}
	c.order = append(c.order, ref)
	c.usedBytes += len(raw)
	return outputs, nil
}

func (c *directCertificateCache) resolve(ref protocol.Hash) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, found := c.entries[ref]
	if !found {
		c.counts.ReferenceMisses++
		return nil, false
	}
	c.counts.ReferenceHits++
	return bytes.Clone(entry.raw), true
}
func (c *directCertificateCache) stats() DirectCacheStats {
	if c == nil {
		return DirectCacheStats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts
}
func (m *Member) DirectCacheStats() DirectCacheStats { return m.directCache.stats() }

func (m *Member) decodeDirectRequest(raw []byte) (protocol.DirectRequest, error) {
	if protocol.IsDirectContinuationRequest(raw) {
		return m.decodeContinuation(raw)
	}
	if !protocol.IsDirectReferenceRequest(raw) {
		return protocol.DecodeDirectRequest(raw)
	}
	compact, err := protocol.DecodeDirectReferenceRequest(raw)
	if err != nil {
		return protocol.DirectRequest{}, err
	}
	if compact.Tx.Body.Config != m.cfg.Organization.Hash() {
		return protocol.DirectRequest{}, protocol.ErrAuth
	}
	req := protocol.DirectRequest{Tx: compact.Tx}
	for _, ref := range compact.Parents {
		raw, found := m.directCache.resolve(ref.Certificate)
		if !found {
			return protocol.DirectRequest{}, ErrCertificateCacheMiss
		}
		cert, err := protocol.DecodeOutputCertificate(raw)
		if err != nil {
			return protocol.DirectRequest{}, err
		}
		req.InputCertificates = append(req.InputCertificates, protocol.InputCertificate{Certificate: cert, Index: ref.Index})
	}
	return req, nil
}
