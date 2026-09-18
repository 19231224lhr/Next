package main

import (
	"github.com/cometbft/cometbft/rpc/client/local"
	"github.com/cometbft/cometbft/types"
	"net/http"
	"utxo/internal/committee"
	"utxo/internal/transport"
	"utxo/protocol"
)

// All available cumulative credits in one request. A shared block header is
// fetched once; every returned fact still has its ordinary verifiable proof.
func directReceiptsHandler(app *committee.App, engine *committee.Engine, client *local.Local) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var id protocol.Hash
		if id.UnmarshalText([]byte(r.PathValue("spend"))) != nil {
			http.Error(w, "INVALID_ID", 400)
			return
		}
		payment, err := engine.DirectPayment(protocol.SpendFactID(id))
		if err != nil {
			http.Error(w, "DEFERRED", 404)
			return
		}
		headers := map[int64]types.SignedHeader{}
		var proofs [][]byte
		for _, a := range payment.Summary.Admission {
			key := (protocol.CreditReceipt{Spend: protocol.SpendFactID(id), Resource: a.Key}).Key()
			fact, err := app.LatestFact(protocol.FactCredit, key)
			if err != nil {
				continue
			}
			height, err := app.FactHeight(fact.ID())
			if err != nil {
				continue
			}
			height++
			header, ok := headers[height]
			if !ok {
				commit, err := client.Commit(r.Context(), &height)
				if err != nil {
					continue
				}
				header = commit.SignedHeader
				headers[height] = header
			}
			proof, err := app.Proof(fact.ID(), header)
			if err != nil {
				continue
			}
			raw, err := proof.MarshalBinary()
			if err != nil {
				continue
			}
			proofs = append(proofs, raw)
		}
		e := new(protocol.Encoder)
		e.U16(308)
		e.U32(uint32(len(proofs)))
		for _, p := range proofs {
			e.Bytes(p)
		}
		w.Header().Set("Content-Type", transport.MediaType)
		_, _ = w.Write(e.Data())
	}
}
