package transport

import (
	"context"
	"utxo/finality"
	"utxo/protocol"
)

func (c *CommitteeClient) DirectReceipts(ctx context.Context, id protocol.SpendFactID) ([]finality.FactProof, error) {
	raw, err := c.get(ctx, "/v3/receipts/"+protocol.Hash(id).String(), protocol.MaxRequestBytes)
	if err != nil {
		return nil, err
	}
	d := protocol.NewDecoder(raw)
	if d.U16() != 308 {
		return nil, protocol.ErrEncoding
	}
	proofs := make([]finality.FactProof, d.Count(protocol.MaxAdmission))
	for i := range proofs {
		proofs[i], err = finality.Decode(d.Bytes(finality.MaxProofBytes))
		if err != nil {
			return nil, err
		}
	}
	return proofs, d.Done()
}
