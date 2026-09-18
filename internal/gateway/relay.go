package gateway

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync"
	"time"
	"utxo/finality"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type PublicClient interface {
	Submit(context.Context, []byte) error
	Receipt(context.Context, protocol.FactKind, protocol.Hash) (finality.FactProof, error)
	Certificate(context.Context, protocol.SpendFactID) (protocol.TXCer, error)
}
type Relay struct {
	DB            store.Store
	Public        PublicClient
	Trust         finality.Trust
	Organizations map[protocol.Hash]protocol.OrgConfig
	Members       map[protocol.Hash][4]MemberClient
	ApplyReceipts func(protocol.TXCer, []finality.FactProof) (rules.ReceiptProgress, error)
	// Reconciles a certificate obtained from the committee when only an original
	// local vote remained. A local member supplies Install for its own issuer.
	Install   func(protocol.TXCer) error
	installMu sync.Mutex
	installs  map[protocol.SpendFactID]installProgress
}
type installProgress struct {
	acked uint8
	next  time.Time
}

func (r *Relay) installTargets(id protocol.SpendFactID) uint8 {
	r.installMu.Lock()
	defer r.installMu.Unlock()
	if r.installs == nil {
		r.installs = make(map[protocol.SpendFactID]installProgress)
	}
	p := r.installs[id]
	if time.Now().Before(p.next) {
		return 0
	}
	p.next = time.Now().Add(time.Second)
	r.installs[id] = p
	return 15 &^ p.acked
}
func (r *Relay) installAck(id protocol.SpendFactID, index int) {
	r.installMu.Lock()
	defer r.installMu.Unlock()
	p := r.installs[id]
	p.acked |= 1 << index
	r.installs[id] = p
}
func (r *Relay) forgetInstall(id protocol.SpendFactID) {
	r.installMu.Lock()
	defer r.installMu.Unlock()
	delete(r.installs, id)
}

func (r *Relay) Run(ctx context.Context) error {
	if r.DB == nil || r.Public == nil || r.Trust.Validate() != nil {
		return protocol.ErrRule
	}
	var cursor []byte
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			entries, e := store.Scan(r.DB, state.Key(state.KeyOutbox), cursor, 16)
			if e != nil {
				return e
			}
			if len(entries) == 0 {
				cursor = nil
				continue
			}
			// Keep the existing ordered scan. Join each group before advancing,
			// so one durable task can never overlap its next retry.
			for start := 0; start < len(entries); start += 4 {
				if ctx.Err() != nil {
					return nil
				}
				end := min(start+4, len(entries))
				done := make(chan error, end-start)
				for _, entry := range entries[start:end] {
					cursor = entry.Key
					go func(key, value []byte) {
						var pending state.Outbox
						if err := json.Unmarshal(value, &pending); err != nil {
							done <- err
							return
						}
						operation, cancel := context.WithTimeout(ctx, 5*time.Second)
						// Failed delivery stays durable for a later pass.
						_ = r.deliver(operation, key, pending)
						cancel()
						done <- nil
					}(entry.Key, entry.Value)
				}
				var failure error
				for i := start; i < end; i++ {
					if err := <-done; err != nil {
						failure = err
					}
				}
				if failure != nil {
					return failure
				}
			}
		}
	}
}
func (r *Relay) deliver(ctx context.Context, key []byte, pending state.Outbox) (err error) {
	var c protocol.TXCer
	var e error
	if len(pending.Certificate) == 0 {
		c, e = r.Public.Certificate(ctx, pending.Fact)
	} else {
		c, e = protocol.DecodeCertificate(pending.Certificate)
	}
	if e != nil {
		return e
	}
	org, known := r.Organizations[c.Tx.Body.Config]
	if !known || c.QC.Fact != pending.Fact {
		return protocol.ErrAuth
	}
	if e = c.Verify(org); e != nil {
		return e
	}
	if r.Install != nil {
		if e = r.Install(c); e != nil {
			return e
		}
	}

	if clients, ok := r.Members[c.Tx.Body.Certifier]; ok {
		mask := r.installTargets(c.QC.Fact)
		installCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{}, 4)
		count := 0
		for i, client := range clients {
			if mask&(1<<i) == 0 || client == nil {
				continue
			}
			count++
			go func(i int, client MemberClient) {
				if client.Install(installCtx, c) == nil {
					r.installAck(c.QC.Fact, i)
				}
				done <- struct{}{}
			}(i, client)
		}
		defer func() {
			cancel()
			for i := 0; i < count; i++ {
				<-done
			}
			if err == nil {
				r.forgetInstall(c.QC.Fact)
			}
		}()
	}

	// Only verified, persisted completion may suppress retries.
	defer func() {
		if err != nil && ctx.Err() == nil {
			if e := r.submitDue(ctx, key, c); e != nil {
				err = errors.Join(err, e)
			}
		}
	}()
	var proofs []finality.FactProof
	var queryErr error
	for _, allocation := range c.Admission {
		kind := protocol.FactCredit
		if allocation.Key.Kind == protocol.ResourceExecution {
			kind = protocol.FactWork
		}
		if allocation.Key.Kind == protocol.ResourceBytes {
			kind = protocol.FactCustody
		}
		receiptKey := (protocol.CreditReceipt{Spend: c.QC.Fact, Resource: allocation.Key}).Key()
		queryCtx, cancel := context.WithTimeout(ctx, time.Second)
		proof, e := r.Public.Receipt(queryCtx, kind, receiptKey)
		cancel()
		if e != nil {
			queryErr = e
			break
		}
		proofs = append(proofs, proof)
	}
	var progress rules.ReceiptProgress
	if r.ApplyReceipts != nil {
		progress, e = r.ApplyReceipts(c, proofs)
	} else {
		var facts []protocol.FinalFact
		for _, proof := range proofs {
			verified, err := finality.Verify(r.Trust, proof)
			if err != nil {
				return err
			}
			facts = append(facts, verified.Fact())
		}
		progress, e = rules.CheckReceipts(c, facts)
		if e == nil && (progress.Complete || progress.PublicComplete) {
			e = r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				if e := rules.FinishOutbox(o, c.QC.Fact, progress); e != nil {
					return nil, e
				}
				return o.Changes(), nil
			})
		}
	}
	if e != nil {
		return e
	}
	if progress.Complete {
		return nil
	}
	if queryErr != nil {
		return queryErr
	}
	return errors.New("public custody not established")
}

const retryInterval = time.Second
const attemptLifetime = 5 * time.Second

func (r *Relay) submitDue(ctx context.Context, key []byte, c protocol.TXCer) error {
	var raw []byte
	err := r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
		p, found, err := state.Load[state.Outbox](v, key)
		if err != nil || !found {
			return nil, err
		}
		if p.Fact != c.QC.Fact {
			return nil, protocol.ErrAuth
		}
		now := time.Now()
		if p.PublicComplete || now.UnixNano() < p.NextSubmitUnixNS {
			return nil, nil
		}
		if len(p.Attempt) == 0 || now.Sub(time.Unix(0, p.AttemptStartedUnixNS)) >= attemptLifetime {
			body, err := c.MarshalBinary()
			if err != nil {
				return nil, err
			}
			attempt := protocol.Submission{Network: c.Tx.Body.Network, Body: body}
			// Identical holders share the initial envelope. Only prolonged lack of
			// progress creates a new nonce to escape a stale Comet transaction cache.
			if len(p.Attempt) != 0 {
				if _, err = rand.Read(attempt.Nonce[:]); err != nil {
					return nil, err
				}
			}
			p.Attempt, err = attempt.MarshalBinary()
			if err != nil {
				return nil, err
			}
			p.AttemptStartedUnixNS = now.UnixNano()
		}
		p.NextSubmitUnixNS = now.Add(retryInterval).UnixNano()
		raw = p.Attempt
		o := state.NewOverlay(v)
		if err := state.Put(o, key, p); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	})
	if err != nil || len(raw) == 0 {
		return err
	}
	return r.Public.Submit(ctx, raw)
}
