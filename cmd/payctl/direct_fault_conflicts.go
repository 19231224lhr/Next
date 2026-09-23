package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type faultConflictRow struct {
	Kind               string
	Index              int
	Requests           []protocol.DirectRequest
	Certificates       []*protocol.OutputCertificate
	Errors             []string
	ExpectedPublicMax  int
	RejectedChecks     int
	ValidInstallChecks int
	TamperedWire       [][]byte `json:",omitempty"`
}

type partialApproval struct {
	Member    string
	Fact      protocol.SpendFactID
	Remaining []protocol.Allocation
}

func faultConflicts(dir string, lab cfg.Lab, n cfg.Network, count int) error {
	requests, err := faultRequests(lab, n, count*12)
	if err != nil {
		return err
	}
	key, err := cfg.PrivateKey(lab.Owners[0])
	if err != nil {
		return err
	}
	policy, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		return err
	}
	rebuild := func(req protocol.DirectRequest, body protocol.TxBody) (protocol.DirectRequest, error) {
		body.Intent = body.IntentID()
		tx, e := protocol.NewFastTx(body, req.Tx.Claims, policy.Key)
		if e != nil {
			return req, e
		}
		tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), key)}
		req.Tx = tx
		return req, nil
	}
	client := transport.NewHTTPClient(3 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rows := []faultConflictRow{}
	db, err := store.OpenNoSync(filepath.Join(dir, "fault-conflict-wallet.db"), store.Identity{Network: n.ChainID, Role: "e4-wallet", Node: "receiver", Schema: 4})
	if err != nil {
		return err
	}
	defer db.Close()
	receiver, err := wallet.New(db, n.Genesis.Network, requests[0].Tx.Body.Outputs[0].Recipient.Owner, n.Organizations)
	if err != nil {
		return err
	}
	send := func(req protocol.DirectRequest, url string) (*protocol.OutputCertificate, string) {
		raw, e := req.MarshalBinary()
		if e != nil {
			return nil, e.Error()
		}
		c, _, e := faultSend(ctx, client, url+"/v3/transactions", raw, false)
		if e != nil {
			return nil, e.Error()
		}
		if e = c.Verify(n.Organizations[0]); e != nil {
			return nil, e.Error()
		}
		if c.Summary.Tx != req.Tx.ID() {
			return nil, "certificate transaction mismatch"
		}
		return c, ""
	}
	for i := 0; i < count; i++ {
		for kindIndex, kind := range []string{"cal_parallel", "fee_parallel", "after_qc", "duplicate", "tamper", "wrong_org"} {
			a := requests[i*12+kindIndex]
			b := requests[i*12+6+kindIndex]
			body := b.Tx.Body
			if kind == "cal_parallel" || kind == "after_qc" {
				body.Inputs = a.Tx.Body.Inputs
				b.Tx.Claims = a.Tx.Claims
			}
			if kind == "fee_parallel" {
				body.Fee = a.Tx.Body.Fee
			}
			b, err = rebuild(b, body)
			if err != nil {
				return err
			}
			row := faultConflictRow{Kind: kind, Index: i, ExpectedPublicMax: 1}
			switch kind {
			case "cal_parallel", "fee_parallel":
				row.Requests = []protocol.DirectRequest{a, b}
				row.Certificates = make([]*protocol.OutputCertificate, 2)
				row.Errors = make([]string, 2)
				var wg sync.WaitGroup
				for j, r := range row.Requests {
					wg.Add(1)
					go func(j int, r protocol.DirectRequest) {
						defer wg.Done()
						row.Certificates[j], row.Errors[j] = send(r, lab.Gateways[0])
					}(j, r)
				}
				wg.Wait()
			case "after_qc":
				c, e := send(a, lab.Gateways[0])
				if c == nil {
					return fmt.Errorf("control missing QC: %s", e)
				}
				c2, e2 := send(b, lab.Gateways[0])
				row.Requests = []protocol.DirectRequest{a, b}
				row.Certificates = []*protocol.OutputCertificate{c, c2}
				row.Errors = []string{e, e2}
			case "duplicate":
				row.Requests = []protocol.DirectRequest{a}
				var expected protocol.SpendFactID
				for j := 0; j < 4; j++ {
					c, e := send(a, lab.Gateways[0])
					if c == nil {
						return fmt.Errorf("duplicate failed: %s", e)
					}
					if j > 0 && c.QC.Fact != expected {
						return fmt.Errorf("duplicate changed fact")
					}
					expected = c.QC.Fact
					row.Certificates = append(row.Certificates, c)
					row.Errors = append(row.Errors, e)
				}
				payment := protocol.DirectPayment{Tx: a.Tx, Certificate: *row.Certificates[0]}
				raw, e := payment.Submission().MarshalBinary()
				if e != nil {
					return e
				}
				pub := transport.NewCommitteeClient(n.CommitteeURLs[0])
				for j := 0; j < 3; j++ {
					e = pub.Submit(ctx, raw)
					if e != nil {
						row.Errors = append(row.Errors, e.Error())
					} else {
						row.Errors = append(row.Errors, "")
					}
				}
			case "tamper":
				c, e := send(a, lab.Gateways[0])
				if c == nil {
					return fmt.Errorf("tamper control: %s", e)
				}
				row.Requests = []protocol.DirectRequest{a}
				row.Certificates = []*protocol.OutputCertificate{c}
				if err := receiver.ReceiveDirect(a.Tx.Body.Outputs[0], *c, 0); err != nil {
					return err
				}
				for _, url := range n.Members[n.Organizations[0].Org] {
					if e := (&transport.MemberClient{BaseURL: url, HTTP: client}).InstallDirect(ctx, protocol.DirectPayment{Tx: a.Tx, Certificate: *c}); e != nil {
						return fmt.Errorf("valid INSTALL control: %w", e)
					}
					row.ValidInstallChecks++
				}
				for variant := 0; variant < 2; variant++ {
					body := a.Tx.Body
					body.Outputs = append([]protocol.Output(nil), body.Outputs...)
					if variant == 0 {
						body.Outputs[0].Amount++
					} else {
						body.Outputs[0].Recipient = a.Tx.Claims[0].Output.Recipient
					}
					changed, e := rebuild(a, body)
					if e != nil {
						return e
					}
					if receiver.ReceiveDirect(body.Outputs[0], *c, 0) == nil {
						return fmt.Errorf("tampered output verified")
					}
					row.RejectedChecks++
					raw, e := faultTamperedPayment(protocol.DirectPayment{Tx: a.Tx, Certificate: *c}, changed.Tx)
					if e != nil {
						return e
					}
					decoded, e := protocol.DecodeDirectPayment(raw)
					if e != nil {
						return fmt.Errorf("attack not decodable: %w", e)
					}
					if e = decoded.Tx.VerifyInitial(policy.Key); e != nil {
						return fmt.Errorf("attack payer signature invalid: %w", e)
					}
					if decoded.Certificate.QC.Fact != c.QC.Fact || decoded.Tx.ID() != changed.Tx.ID() {
						return fmt.Errorf("attack wire did not preserve intended mismatch")
					}
					row.TamperedWire = append(row.TamperedWire, raw)
					for _, url := range n.Members[n.Organizations[0].Org] {
						req, e := http.NewRequestWithContext(ctx, "POST", url+"/v3/certificates", bytes.NewReader(raw))
						if e != nil {
							return e
						}
						req.Header.Set("Content-Type", transport.MediaType)
						resp, e := client.Do(req)
						if e != nil {
							return e
						}
						message, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
						resp.Body.Close()
						if readErr != nil {
							return readErr
						}
						if resp.StatusCode != 400 || string(bytes.TrimSpace(message)) != "INVALID_AUTH" {
							return fmt.Errorf("tampered INSTALL unexpected HTTP %d", resp.StatusCode)
						}
						row.Errors = append(row.Errors, fmt.Sprintf("%s HTTP %d: %s", url, resp.StatusCode, message))
						row.RejectedChecks++
					}
				}
			case "wrong_org":
				// Keep the valid signed request intact and send it to the wrong
				// authority. Changing Certifier would be rejected by the local
				// encoder before any real gateway received the attack.
				raw, e := a.MarshalBinary()
				if e != nil {
					return e
				}
				c, _, e := faultSend(ctx, client, lab.Gateways[1]+"/v3/transactions", raw, false)
				if e == nil && c != nil {
					return fmt.Errorf("wrong organization accepted")
				}
				row.Requests = []protocol.DirectRequest{a}
				row.ExpectedPublicMax = 0
				row.Errors = []string{fmt.Sprint(e)}
			}
			if kind == "cal_parallel" || kind == "fee_parallel" || kind == "after_qc" {
				valid := 0
				for _, c := range row.Certificates {
					if c != nil {
						valid++
					}
				}
				if valid > 1 {
					return fmt.Errorf("two conflicting QCs in %s %d", kind, i)
				}
			}
			rows = append(rows, row)
		}
	}
	file, err := os.Create(filepath.Join(dir, "reports", "fault-conflicts.json"))
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(rows)
}

// The ordinary encoder correctly refuses a mismatched certificate. An attack
// probe must still reach the server: replace only the signed transaction section
// of a valid wire4 payment, preserving the original quorum certificate bytes.
func faultTamperedPayment(p protocol.DirectPayment, changed protocol.FastTx) ([]byte, error) {
	raw, err := p.MarshalBinary()
	if err != nil {
		return nil, err
	}
	tx, err := changed.MarshalBinary()
	if err != nil {
		return nil, err
	}
	oldSize := int(binary.BigEndian.Uint32(raw[18:22]))
	newSize := int(binary.BigEndian.Uint32(tx[8:12]))
	fixedSize := int(binary.BigEndian.Uint32(raw[8:12]))
	if oldSize != newSize || len(raw)-(16+fixedSize) != len(tx)-(16+newSize) {
		return nil, fmt.Errorf("tamper changed fixed-width layout")
	}
	copy(raw[22:22+oldSize], tx[16:16+newSize])
	copy(raw[16+fixedSize:], tx[16+newSize:])
	return raw, nil
}

func auditFaultConflicts(dir string, lab cfg.Lab, n cfg.Network) error {
	var rows []faultConflictRow
	file, err := os.Open(filepath.Join(dir, "reports", "fault-conflicts.json"))
	if err != nil {
		return err
	}
	defer file.Close()
	if err = json.NewDecoder(file).Decode(&rows); err != nil {
		return err
	}
	policy, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		return err
	}
	type result struct {
		Kind          string
		Index, Public int
		Fees          []rules.Escrow
		Candidates    []protocol.SpendFactID
		Partial       []partialApproval
	}
	out := []result{}
	err = store.Inspect(filepath.Join(dir, "committee0", "committee.db"), func(v state.ReadView) error {
		for _, row := range rows {
			r := result{Kind: row.Kind, Index: row.Index}
			for _, req := range row.Requests {
				vector, e := rules.PrepareDirectVector(req.Tx, policy)
				if e != nil {
					return e
				}
				fact := protocol.SummaryFor(req.Tx, vector).Fact()
				r.Candidates = append(r.Candidates, fact)
				p, found, e := state.Load[rules.DirectPaymentState](v, state.Key(103, fact[:]))
				if e != nil {
					return e
				}
				if found {
					r.Public++
					r.Fees = append(r.Fees, p.Fee)
					if !p.Fee.Closed || p.FeeSource != protocol.OwnerFinalUTXO {
						return fmt.Errorf("fee not closed/self paid")
					}
				}
			}
			if r.Public > row.ExpectedPublicMax {
				return fmt.Errorf("conflict public count %s %d", row.Kind, r.Public)
			}
			if (row.Kind == "after_qc" || row.Kind == "duplicate" || row.Kind == "tamper") && r.Public != 1 {
				return fmt.Errorf("valid control not public")
			}
			out = append(out, r)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, node := range lab.Nodes {
		if node.Binary != "member" {
			continue
		}
		err = store.Inspect(filepath.Join(dir, node.Name, "member.db"), func(v state.ReadView) error {
			for i := range out {
				for _, fact := range out[i].Candidates {
					a, found, e := state.Load[state.Approval](v, state.Key(state.KeyApproval, fact[:]))
					if e != nil {
						return e
					}
					if !found {
						continue
					}
					applied, e := member.AppliedDebits(v, a)
					if e != nil {
						return e
					}
					p := partialApproval{Member: node.Name, Fact: fact}
					for j, d := range a.Debits {
						if applied[j] > d.Cap {
							return fmt.Errorf("release exceeds original debit")
						}
						if applied[j] < d.Cap {
							p.Remaining = append(p.Remaining, protocol.Allocation{Key: d.Key, Cap: d.Cap - applied[j]})
						}
					}
					if len(p.Remaining) > 0 {
						out[i].Partial = append(out[i].Partial, p)
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return cfg.Write(filepath.Join(dir, "reports", "fault-conflict-audit.json"), out)
}
