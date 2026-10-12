package poollab

import (
	"context"
	"fmt"
	"sort"
	"time"
	"utxo/internal/gateway"
	"utxo/internal/state"
	"utxo/internal/transport"
	"utxo/protocol"
)

type OrdinaryReport struct {
	Mode                   string    `json:"mode"`
	Storage                string    `json:"storage"`
	Transfers              int       `json:"transfers"`
	WalletOwners           int       `json:"wallet_owners"`
	Members                int       `json:"members"`
	TransferredUnitCAL     uint64    `json:"transferred_unit_cal"`
	CertifiedFaceAmountCAL uint64    `json:"certified_face_amount_cal"`
	MemberCALReserved      [4]uint64 `json:"member_cal_reserved"`
	PublicBlocks           int       `json:"public_blocks"`
	LatencyMS              []float64 `json:"transfer_latency_ms"`
	P50MS                  float64   `json:"p50_ms"`
	P95MS                  float64   `json:"p95_ms"`
	Boundary               string    `json:"measurement_boundary"`
}

func (l *Lab) CALReservations() (amounts [4]uint64, err error) {
	var key protocol.ResourceKey
	for _, g := range l.Fixture.Genesis.Grants {
		if g.Key.Kind == protocol.ResourceCAL {
			key = g.Key
		}
	}
	for i, db := range l.MemberDB {
		err = db.View(func(v state.ReadView) error {
			s, found, e := state.Load[state.Slice](v, state.SliceKey(key, 0))
			if e != nil {
				return e
			}
			if !found {
				return fmt.Errorf("member CAL slice missing")
			}
			amounts[i] = s.Reserved
			return nil
		})
		if err != nil {
			return amounts, err
		}
	}
	return amounts, nil
}

// RunOrdinary uses the existing immediate-parent-QC path without public
// settlement. Member slice values are replica-local, not additive capital.
func RunOrdinary(ctx context.Context, n int, directory string) (OrdinaryReport, error) {
	r := OrdinaryReport{Mode: "ordinary certificate chain; loopback HTTP; no public settlement", Storage: "memory", Transfers: n, WalletOwners: 3, Members: 4, TransferredUnitCAL: 100, Boundary: "sender SaveDirectRequest through gateway.CollectDirect and recipient ReceiveDirect; no pool full-QC install phase; member reservation values are replica-local"}
	if directory != "" {
		r.Storage = "synchronous Bolt"
	}
	if n < 1 {
		return r, fmt.Errorf("transfers must be positive")
	}
	l, e := New(directory)
	if e != nil {
		return r, e
	}
	defer l.Close()
	db, e := l.open("ordinary-gateway")
	if e != nil {
		return r, e
	}
	defer db.Close()
	var clients [4]gateway.MemberClient
	for i, s := range l.Servers {
		clients[i] = transport.NewMemberClient(s.URL)
	}
	collector, e := gateway.New(l.Fixture.Org, clients, db)
	if e != nil {
		return r, e
	}
	var previous protocol.OutputCertificate
	var previousOutput protocol.Output
	owner := 0
	for i := 0; i < n; i++ {
		to := (owner + 1) % 3
		template, e := l.OrdinaryTemplate(owner, uint64(i+1))
		if e != nil {
			return r, e
		}
		b := template.Body
		b.Subject = l.Descriptors[owner].Owner
		input := l.Fixture.Genesis.Outputs[0].Output
		var parents []protocol.InputCertificate
		if i > 0 {
			input = previousOutput
			b.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: previous.Summary.OutputID(0), Evidence: protocol.Hash(previous.QC.Fact)}}
			parents = []protocol.InputCertificate{{Certificate: previous, Index: 0}}
		}
		out := protocol.Output{Asset: protocol.AssetCAL, Amount: 100, Recipient: l.Descriptors[to]}
		b.Outputs = []protocol.Output{out}
		tx, e := protocol.NewFastTx(b, []protocol.InputClaim{{Output: input}}, l.Policy.Key)
		if e != nil {
			return r, e
		}
		tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), l.Keys[owner])}
		request := protocol.DirectRequest{Tx: tx, InputCertificates: parents}
		started := time.Now()
		if e = l.Wallets[owner].SaveDirectRequest(request); e != nil {
			return r, e
		}
		cert, e := collector.CollectDirect(ctx, request)
		if e != nil {
			return r, e
		}
		if e = l.Wallets[to].ReceiveDirect(out, cert, 0); e != nil {
			return r, e
		}
		r.LatencyMS = append(r.LatencyMS, float64(time.Since(started).Nanoseconds())/1e6)
		previous = cert
		previousOutput = out
		owner = to
		r.CertifiedFaceAmountCAL += out.Amount
	}
	// Drain handlers before auditing the four independent local ledgers.
	for _, server := range l.Servers {
		server.Close()
	}
	r.MemberCALReserved, e = l.CALReservations()
	if e != nil {
		return r, e
	}
	sorted := append([]float64(nil), r.LatencyMS...)
	sort.Float64s(sorted)
	r.P50MS = sorted[(len(sorted)-1)*50/100]
	r.P95MS = sorted[(len(sorted)-1)*95/100]
	return r, nil
}
