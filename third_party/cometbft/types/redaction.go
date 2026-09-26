// Experimental wire-v3 extension, compiled against CometBFT v0.38.26.
package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"

	"github.com/cometbft/cometbft/crypto/merkle"
	"github.com/cometbft/cometbft/libs/bits"
	"utxo/crypto/chameleon"
)

var redactionKey *chameleon.Public
var redactionChain string
var redactionConfig sync.Mutex
var txMagic = []byte{'U', 'T', 'X', 'O', '4', 'C', 'H', 0}
var partMagic = []byte{'P', 'A', 'R', 'T', '3', 'C', 'H', 0}
var ErrRedaction = errors.New("invalid wire-v3 redaction")

// ConfigureRedaction is a startup-only setting for a new genesis network.
func ConfigureRedaction(chain string, key *chameleon.Public) error {
	redactionConfig.Lock()
	defer redactionConfig.Unlock()
	if chain == "" || key == nil {
		return ErrRedaction
	}
	if redactionKey != nil {
		if redactionChain == chain && redactionKey.KeyID() == key.KeyID() {
			return nil
		}
		return ErrRedaction
	}
	redactionKey, redactionChain = key, chain
	return nil
}

// EncodeRedactableTx is the consensus envelope, not a payment validator.
// Fixed must include commitments to every mutable slot and its allowed policy.
func EncodeRedactableTx(fixed, mutable []byte) Tx {
	b := make([]byte, 16+len(fixed)+len(mutable))
	copy(b, txMagic)
	binary.BigEndian.PutUint32(b[8:12], uint32(len(fixed)))
	binary.BigEndian.PutUint32(b[12:16], uint32(len(mutable)))
	copy(b[16:], fixed)
	copy(b[16+len(fixed):], mutable)
	return Tx(b)
}

func redactionTxHash(tx Tx) []byte {
	data := []byte(tx)
	// Wire identity is self-describing; readers need no repair-key setup.
	if len(tx) >= 16 && bytes.Equal(tx[:8], txMagic) {
		n, m := uint64(binary.BigEndian.Uint32(tx[8:12])), uint64(binary.BigEndian.Uint32(tx[12:16]))
		if n+m+16 == uint64(len(tx)) {
			data = tx[:16+n]
		}
	}
	h := sha256.Sum256(data)
	return h[:]
}

type PartRedaction struct {
	Height   int64             `json:"height"`
	Revision uint64            `json:"revision"`
	Opening  chameleon.Opening `json:"opening"`
}

// ValidateOriginal admits only canonical execution parts into live consensus.
// Revision is metadata, so checking its zero label alone is insufficient.
// Historical storage and generic inclusion use the separate revision path.
func (p *Part) ValidateOriginal(height int64) error {
	if p == nil {
		return ErrRedaction
	}
	if redactionKey == nil && p.Redaction == nil {
		return nil
	}
	var initial chameleon.Opening
	initial[chameleon.Size-1] = 1
	if p.Redaction == nil || p.Redaction.Height != height || p.Redaction.Revision != 0 || p.Redaction.Opening != initial {
		return ErrRedaction
	}
	return nil
}

func RedactionContext(height int64, index uint32) []byte {
	h := sha256.New()
	h.Write([]byte("BLOCK_PART_V3"))
	h.Write([]byte(redactionChain))
	keyID := redactionKey.KeyID()
	h.Write(keyID[:])
	var b [12]byte
	binary.BigEndian.PutUint64(b[:8], uint64(height))
	binary.BigEndian.PutUint32(b[8:], index)
	h.Write(b[:])
	return h.Sum(nil)
}

func partCommitment(p *Part) ([]byte, error) {
	if p.Redaction == nil {
		if redactionKey != nil {
			return nil, ErrRedaction
		}
		return p.Bytes, nil
	}
	if redactionKey == nil || p.Redaction.Height < 1 {
		return nil, ErrRedaction
	}
	c, err := redactionKey.Digest(RedactionContext(p.Redaction.Height, p.Index), p.Bytes, p.Redaction.Opening)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(c[:])
	return h[:], nil
}

func encodeRedactionPart(p *Part) []byte {
	if p.Redaction == nil {
		return p.Bytes
	}
	b := make([]byte, 24+chameleon.Size+len(p.Bytes))
	copy(b, partMagic)
	binary.BigEndian.PutUint64(b[8:16], uint64(p.Redaction.Height))
	binary.BigEndian.PutUint64(b[16:24], p.Redaction.Revision)
	copy(b[24:], p.Redaction.Opening[:])
	copy(b[24+chameleon.Size:], p.Bytes)
	return b
}

func decodeRedactionPart(p *Part, wire []byte) error {
	if redactionKey == nil {
		p.Bytes = wire
		return nil
	}
	if len(wire) < 24+chameleon.Size || !bytes.Equal(wire[:8], partMagic) {
		return ErrRedaction
	}
	p.Redaction = &PartRedaction{Height: int64(binary.BigEndian.Uint64(wire[8:16])), Revision: binary.BigEndian.Uint64(wire[16:24])}
	copy(p.Redaction.Opening[:], wire[24:24+chameleon.Size])
	p.Bytes = wire[24+chameleon.Size:]
	_, err := partCommitment(p)
	return err
}

func (ps *PartSet) acceptRedaction(p *Part) bool {
	if p.Redaction == nil {
		return redactionKey == nil
	}
	if ps.redactionSeen {
		return ps.redactionHeight == p.Redaction.Height && ps.redactionRevision == p.Redaction.Revision
	}
	ps.redactionSeen = true
	ps.redactionHeight, ps.redactionRevision = p.Redaction.Height, p.Redaction.Revision
	return true
}

// Nil openings mean the canonical initial opening 1. Fresh block commitments
// must be deterministic when the proposer and validators independently rebuild.
func NewRedactablePartSet(data []byte, partSize uint32, height int64, revision uint64, openings []chameleon.Opening) (*PartSet, error) {
	if redactionKey == nil || partSize == 0 || height < 1 {
		return nil, ErrRedaction
	}
	total := (len(data) + int(partSize) - 1) / int(partSize)
	if total == 0 || (openings != nil && len(openings) != total) || (revision != 0 && openings == nil) {
		return nil, ErrRedaction
	}
	parts, leaves := make([]*Part, total), make([][]byte, total)
	for i := range parts {
		var r chameleon.Opening
		r[chameleon.Size-1] = 1
		if openings != nil {
			r = openings[i]
		}
		end := min((i+1)*int(partSize), len(data))
		parts[i] = &Part{Index: uint32(i), Bytes: bytes.Clone(data[i*int(partSize) : end]), Redaction: &PartRedaction{Height: height, Revision: revision, Opening: r}}
		leaf, err := partCommitment(parts[i])
		if err != nil {
			return nil, err
		}
		leaves[i] = leaf
	}
	root, proofs := merkle.ProofsFromByteSlices(leaves)
	set := &PartSet{total: uint32(total), hash: root, parts: parts, partsBitArray: bits.NewBitArray(total), count: uint32(total), byteSize: int64(len(data)), redactionSeen: true, redactionHeight: height, redactionRevision: revision}
	for i := range parts {
		parts[i].Proof = *proofs[i]
		set.partsBitArray.SetIndex(i, true)
	}
	return set, nil
}
