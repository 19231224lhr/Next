//go:build comet_v3

package redaction_test

import (
	"bytes"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"time"
	"utxo/crypto/chameleon"
	"utxo/finality"
	"utxo/internal/redaction"
	"utxo/internal/rules"
	"utxo/protocol"
)

const offlineN = "N*-lean"
const offlineB = "B-lean"

type offlineRow struct {
	TargetHeight             int64
	Transaction, Input       uint32
	ConsumerTx               protocol.TxID
	ConsumerFact, ParentFact protocol.SpendFactID
	Output                   protocol.OutputID
	Organization             protocol.Hash
	Amount                   uint64
	Decision, Debit          protocol.Hash
	ExecutionHeight          int64
}

func TestOfflineReceiptMatchedRows(t *testing.T) {
	f := newReceiptFixture(t, 3, 2, true)
	cfg := offlineConfigBytes(t, f)
	want := offlineExpected(t, f, 2)
	for _, mode := range []string{offlineN, offlineB} {
		raw := offlineExport(t, f, 2, mode)
		got, err := offlineVerify(cfg, raw, mode)
		readerOK(t, err)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s differs from original payments and executed effects", mode)
		}
	}
	if _, err := offlineVerify(cfg, offlineExport(t, f, 2, offlineN), offlineB); err == nil {
		t.Fatal("caller-requested B guarantee downgraded by N packet")
	}
}

func TestOfflineReceiptRejectsInvalidAndCachesOnlyVerified(t *testing.T) {
	f := newReceiptFixture(t, 3, 2, true)
	cfg := offlineConfigBytes(t, f)
	for _, mode := range []string{offlineN, offlineB} {
		raw := offlineExport(t, f, 2, mode)
		for _, kind := range []string{"result", "cross-parent", "parent-signature"} {
			bad := offlineTamper(t, raw, kind)
			if _, err := offlineVerify(cfg, bad, mode); err == nil {
				t.Fatalf("%s accepted %s", mode, kind)
			}
			cache := newOfflineCache()
			if _, err := cache.admit(cfg, bad, mode); err == nil || len(cache.rows) != 0 {
				t.Fatal("unverified rows entered cache")
			}
		}
		cache := newOfflineCache()
		handle, err := cache.admit(cfg, raw, mode)
		readerOK(t, err)
		got := cache.lookup(handle)
		if !reflect.DeepEqual(got, offlineExpected(t, f, 2)) {
			t.Fatal("cached rows differ")
		}
		got[0].Amount++
		if reflect.DeepEqual(got, cache.lookup(handle)) {
			t.Fatal("lookup exposes mutable cache rows")
		}
	}
}

// Transport contains public protobuf/configuration bytes only. It carries no
// signer shares, fixture objects, database paths, cached hashes or chosen mode.
type offlinePublicConfig struct {
	ChainID        string
	Network        protocol.Hash
	Validators     []byte
	Base           rules.Schedule
	TimeoutSeconds int64
	RepairCost     uint64
	Modulus        []byte
	Organizations  []protocol.OrgConfig
}
type offlineExecution struct {
	Block     []byte
	Results   [][]byte
	Successor []byte
}
type offlineParent struct {
	Certificate []byte
	Index       uint32
}
type offlinePacket struct {
	Compensation   offlineExecution
	Indices        []uint32
	Parents        []offlineParent   `json:",omitempty"`
	Body           []byte            `json:",omitempty"`
	OriginalCommit []byte            `json:",omitempty"`
	Repair         *offlineExecution `json:",omitempty"`
	CommandIndex   uint32            `json:",omitempty"`
}

func offlineJSON(t *testing.T, x any) []byte {
	t.Helper()
	raw, err := json.Marshal(x)
	readerOK(t, err)
	return raw
}
func offlineConfigBytes(t *testing.T, f *receiptFixture) []byte {
	t.Helper()
	pb, err := f.trust.Validators.ToProto()
	readerOK(t, err)
	validators, err := pb.Marshal()
	readerOK(t, err)
	c := offlinePublicConfig{ChainID: f.trust.ChainID, Network: f.trust.Network, Validators: validators,
		Base: f.policy.Base, TimeoutSeconds: f.policy.TimeoutSeconds, RepairCost: f.policy.RepairCost, Modulus: f.policy.Key.Modulus()}
	for _, org := range f.policy.Organizations {
		c.Organizations = append(c.Organizations, org)
	}
	sort.Slice(c.Organizations, func(i, j int) bool {
		a, b := c.Organizations[i].Hash(), c.Organizations[j].Hash()
		return bytes.Compare(a[:], b[:]) < 0
	})
	return offlineJSON(t, c)
}
func offlineDecodeConfig(raw []byte) (finality.Trust, rules.DirectPolicy, error) {
	var c offlinePublicConfig
	var t finality.Trust
	var p rules.DirectPolicy
	if err := json.Unmarshal(raw, &c); err != nil {
		return t, p, err
	}
	var pb cmtproto.ValidatorSet
	if err := pb.Unmarshal(c.Validators); err != nil {
		return t, p, err
	}
	validators, err := types.ValidatorSetFromProto(&pb)
	if err != nil {
		return t, p, err
	}
	t = finality.Trust{ChainID: c.ChainID, Network: c.Network, Validators: validators}
	if err = t.Validate(); err != nil {
		return t, p, err
	}
	key, err := chameleon.NewPublic(&rsa.PublicKey{N: new(big.Int).SetBytes(c.Modulus), E: 65537})
	if err != nil {
		return t, p, err
	}
	p = rules.DirectPolicy{Base: c.Base, TimeoutSeconds: c.TimeoutSeconds, RepairCost: c.RepairCost, Key: key, Organizations: map[protocol.Hash]protocol.OrgConfig{}}
	if err = protocol.ValidateOrganizations(c.Network, c.Organizations); err != nil {
		return t, p, err
	}
	for _, org := range c.Organizations {
		p.Organizations[org.Hash()] = org
	}
	if err = p.Base.Validate(); err != nil {
		return t, p, err
	}
	if err = types.ConfigureRedaction(t.ChainID, p.Key); err != nil {
		return t, p, err
	}
	return t, p, nil
}
func offlineEncodeExecution(t *testing.T, b finality.BlockData) offlineExecution {
	t.Helper()
	w := offlineExecution{Block: receiptBody(t, b.Block)}
	for _, result := range b.Results {
		raw, err := result.Marshal()
		readerOK(t, err)
		w.Results = append(w.Results, raw)
	}
	var err error
	w.Successor, err = b.Next.ToProto().Marshal()
	readerOK(t, err)
	return w
}
func offlineDecodeExecution(w offlineExecution) (finality.BlockData, error) {
	var out finality.BlockData
	var pb cmtproto.Block
	if err := pb.Unmarshal(w.Block); err != nil {
		return out, err
	}
	var err error
	out.Block, err = types.BlockFromProto(&pb)
	if err != nil {
		return out, err
	}
	for _, raw := range w.Results {
		result := new(abci.ExecTxResult)
		if err = result.Unmarshal(raw); err != nil {
			return out, err
		}
		out.Results = append(out.Results, result)
	}
	var header cmtproto.SignedHeader
	if err = header.Unmarshal(w.Successor); err != nil {
		return out, err
	}
	signed, err := types.SignedHeaderFromProto(&header)
	if err != nil {
		return out, err
	}
	out.Next = *signed
	return out, nil
}
func offlineExport(t *testing.T, f *receiptFixture, m int, mode string) []byte {
	t.Helper()
	w := offlinePacket{Compensation: offlineEncodeExecution(t, f.evidence[2])}
	for i := 0; i < m; i++ {
		if mode == offlineN {
			pay, err := protocol.DecodeDirectSubmission(f.evidence[0].Block.Txs[i])
			readerOK(t, err)
			parent := pay.InputCertificates[0]
			raw, err := parent.Certificate.MarshalBinary()
			readerOK(t, err)
			w.Parents = append(w.Parents, offlineParent{raw, parent.Index})
		}
		w.Indices = append(w.Indices, uint32(i))
	}
	if mode == offlineB {
		r := f.receipts[0]
		w.Body = r.Body
		var err error
		w.OriginalCommit, err = r.OriginalCommit.ToProto().Marshal()
		readerOK(t, err)
		repair := offlineEncodeExecution(t, r.Execution)
		w.Repair = &repair
		w.CommandIndex = r.CommandIndex
	} else if mode != offlineN {
		t.Fatalf("unknown method %q", mode)
	}
	return offlineJSON(t, w)
}
func offlineRows(t finality.Trust, p rules.DirectPolicy, effects []redaction.AuthorizedCompensation, parents []offlineParent) ([]offlineRow, error) {
	if len(effects) == 0 || len(effects) != len(parents) {
		return nil, protocol.ErrAuth
	}
	rows := make([]offlineRow, 0, len(effects))
	seen := map[protocol.Hash]bool{}
	for i, x := range effects {
		c, err := protocol.DecodeOutputCertificate(parents[i].Certificate)
		if err != nil {
			return nil, err
		}
		cfg, ok := p.Organizations[c.Summary.Config]
		index := parents[i].Index
		e, d := x.Effect, x.Decision
		if !ok || c.Verify(cfg) != nil || c.Summary.Network != t.Network || c.Summary.Rules != p.Rules() ||
			int(index) >= len(c.Summary.Outputs) || c.Summary.OutputID(index) != e.Output || c.Summary.Fact() != e.ParentFact ||
			c.Summary.Outputs[index].Amount != e.Amount || e.ConsumerTx == (protocol.TxID{}) || e.ConsumerFact == (protocol.SpendFactID{}) || seen[d.ID()] {
			return nil, protocol.ErrAuth
		}
		seen[d.ID()] = true
		rows = append(rows, offlineRow{TargetHeight: d.Height, Transaction: d.Transaction, Input: d.Input, ConsumerTx: e.ConsumerTx,
			ConsumerFact: e.ConsumerFact, ParentFact: e.ParentFact, Output: e.Output, Organization: c.Summary.Issuer, Amount: e.Amount,
			Decision: d.ID(), Debit: e.Debit, ExecutionHeight: x.ExecutionHeight})
	}
	return rows, nil
}

// cfg and mode are caller-pinned, never selected from the proof package.
// Each invocation decodes all supplied evidence anew, with no fixture or DB reads.
func offlineVerify(cfg, raw []byte, mode string) ([]offlineRow, error) {
	t, p, err := offlineDecodeConfig(cfg)
	if err != nil {
		return nil, err
	}
	var w offlinePacket
	if err = json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	b, err := offlineDecodeExecution(w.Compensation)
	if err != nil {
		return nil, err
	}
	effects, err := redaction.VerifyCompensationReceipts(t, b, w.Indices)
	if err != nil {
		return nil, err
	}
	var rows []offlineRow
	switch mode {
	case offlineN:
		if len(w.Body) != 0 || len(w.OriginalCommit) != 0 || w.Repair != nil {
			return nil, protocol.ErrAuth
		}
		return offlineRows(t, p, effects, w.Parents)
	case offlineB:
		if w.Repair == nil || len(w.Body) == 0 || len(w.OriginalCommit) == 0 || len(w.Parents) != 0 {
			return nil, protocol.ErrAuth
		}
		repair, err := offlineDecodeExecution(*w.Repair)
		if err != nil {
			return nil, err
		}
		var cp cmtproto.Commit
		if err = cp.Unmarshal(w.OriginalCommit); err != nil {
			return nil, err
		}
		commit, err := types.CommitFromProto(&cp)
		if err != nil {
			return nil, err
		}
		revision, err := redaction.VerifyHistoricalAuthorization(t, p, redaction.LeanHistoricalReceipt{Body: w.Body, OriginalCommit: commit, Execution: repair, CommandIndex: w.CommandIndex})
		if err != nil {
			return nil, err
		}
		if len(effects) == 0 || len(revision.Funding) != len(effects) {
			return nil, protocol.ErrAuth
		}
		var bodyPB cmtproto.Block
		if err = bodyPB.Unmarshal(w.Body); err != nil {
			return nil, err
		}
		seen := map[protocol.Hash]bool{}
		for i, x := range revision.Funding {
			actual := effects[i]
			e, d := actual.Effect, actual.Decision
			row := offlineRow{TargetHeight: d.Height, Transaction: d.Transaction, Input: d.Input, ConsumerTx: e.ConsumerTx,
				ConsumerFact: e.ConsumerFact, ParentFact: e.ParentFact, Output: e.Output, Organization: x.Organization, Amount: e.Amount,
				Decision: d.ID(), Debit: e.Debit, ExecutionHeight: actual.ExecutionHeight}
			if revision.TargetHeight != row.TargetHeight || revision.ExecutionHeight <= row.ExecutionHeight || x.Transaction != row.Transaction || x.Input != row.Input ||
				x.ConsumerTx != row.ConsumerTx || x.Output != row.Output || x.Organization != row.Organization || x.Amount != row.Amount ||
				x.Decision != row.Decision || x.Debit != row.Debit || seen[row.Decision] {
				return nil, protocol.ErrAuth
			}
			// Successful VerifyHistoricalAuthorization above authenticated this
			// exact Body, including every selected payment/parent certificate QC.
			// Decode those same immutable bytes only to link their facts and
			// amount to T1. No external copy or duplicate QC verification needed.
			if int(row.Transaction) >= len(bodyPB.Data.Txs) {
				return nil, protocol.ErrAuth
			}
			pay, err := protocol.DecodeDirectSubmission(bodyPB.Data.Txs[row.Transaction])
			if err != nil {
				return nil, err
			}
			if int(row.Input) >= len(pay.Tx.Body.Inputs) || pay.Summary().Fact() != row.ConsumerFact || pay.Tx.Body.Inputs[row.Input].Evidence != protocol.Hash(row.ParentFact) {
				return nil, protocol.ErrAuth
			}
			matched := false
			for _, parent := range pay.InputCertificates {
				c, index := parent.Certificate, parent.Index
				if int(index) < len(c.Summary.Outputs) && c.Summary.OutputID(index) == row.Output && c.Summary.Fact() == row.ParentFact &&
					c.Summary.Issuer == row.Organization && c.Summary.Outputs[index].Amount == row.Amount {
					matched = true
				}
			}
			if !matched {
				return nil, protocol.ErrAuth
			}
			seen[row.Decision] = true
			rows = append(rows, row)
		}
	default:
		return nil, protocol.ErrAuth
	}
	return rows, nil
}

// Ground truth is derived from original valid owner payments/source certificates
// and independently authenticated actual compensation execution, never from B.
func offlineExpected(t *testing.T, f *receiptFixture, m int) []offlineRow {
	t.Helper()
	verified, err := finality.VerifyBlock(f.trust, f.evidence[2])
	readerOK(t, err)
	actual := verified.Transactions()
	rows := make([]offlineRow, 0, m)
	for i := 0; i < m; i++ {
		pay, err := protocol.DecodeDirectSubmission(f.evidence[0].Block.Txs[i])
		readerOK(t, err)
		_, err = rules.VerifyDirectSubmission(pay, f.policy)
		readerOK(t, err)
		parent := pay.InputCertificates[0]
		cfg := f.policy.Organizations[parent.Certificate.Summary.Config]
		readerOK(t, parent.Certificate.VerifyOutput(cfg, parent.Index, pay.Tx.Claims[0].Output))
		decision, err := protocol.DecodeCompensationDecision(actual[i].Bytes)
		readerOK(t, err)
		result, err := protocol.DecodeCompensationResult(actual[i].Data)
		readerOK(t, err)
		if actual[i].Code != 0 || !result.Applied || len(result.Effects) != 1 || result.Decision != decision.ID() {
			t.Fatal("nonexecuted ground truth")
		}
		e := result.Effects[0]
		output := parent.Certificate.Summary.OutputID(parent.Index)
		if e.Output != output || e.ParentFact != parent.Certificate.Summary.Fact() || e.ConsumerTx != pay.Tx.ID() || e.ConsumerFact != pay.Summary().Fact() ||
			e.Amount != pay.Tx.Claims[0].Output.Amount || decision.Height != 1 || decision.Transaction != uint32(i) || decision.Input != 0 || e.Debit != protocol.ReserveDebitIdentity(f.trust.Network, output) {
			t.Fatal("execution disagrees with original payments")
		}
		rows = append(rows, offlineRow{TargetHeight: 1, Transaction: uint32(i), Input: 0, ConsumerTx: pay.Tx.ID(), ConsumerFact: pay.Summary().Fact(),
			ParentFact: parent.Certificate.Summary.Fact(), Output: output, Organization: parent.Certificate.Summary.Issuer, Amount: pay.Tx.Claims[0].Output.Amount,
			Decision: decision.ID(), Debit: e.Debit, ExecutionHeight: verified.Height()})
	}
	return rows
}
func offlineTamper(t *testing.T, raw []byte, kind string) []byte {
	t.Helper()
	var w offlinePacket
	readerOK(t, json.Unmarshal(raw, &w))
	switch kind {
	case "result":
		w.Compensation.Results[0][len(w.Compensation.Results[0])-1] ^= 1
	case "cross-parent":
		if w.Repair == nil {
			w.Parents[0] = w.Parents[1]
		} else {
			var pb cmtproto.Block
			readerOK(t, pb.Unmarshal(w.Body))
			pay, err := protocol.DecodeDirectSubmission(pb.Data.Txs[0])
			readerOK(t, err)
			other, err := protocol.DecodeDirectSubmission(pb.Data.Txs[1])
			readerOK(t, err)
			pay.InputCertificates[0] = other.InputCertificates[0]
			pb.Data.Txs[0], err = pay.MarshalBinary()
			readerOK(t, err)
			w.Body, err = pb.Marshal()
			readerOK(t, err)
		}
	case "parent-signature":
		if w.Repair == nil {
			c, err := protocol.DecodeOutputCertificate(w.Parents[0].Certificate)
			readerOK(t, err)
			c.QC.Votes[0].Signature[0] ^= 1
			w.Parents[0].Certificate, err = c.MarshalBinary()
			readerOK(t, err)
		} else {
			var pb cmtproto.Block
			readerOK(t, pb.Unmarshal(w.Body))
			pay, err := protocol.DecodeDirectSubmission(pb.Data.Txs[0])
			readerOK(t, err)
			pay.InputCertificates[0].Certificate.QC.Votes[0].Signature[0] ^= 1
			pb.Data.Txs[0], err = pay.MarshalBinary()
			readerOK(t, err)
			w.Body, err = pb.Marshal()
			readerOK(t, err)
		}
	case "body":
		w.Body[len(w.Body)-1] ^= 1
	default:
		t.Fatal("unknown tamper")
	}
	return offlineJSON(t, w)
}

type offlineHandle [32]byte
type offlineCache struct {
	rows map[offlineHandle][]offlineRow
}

func newOfflineCache() *offlineCache { return &offlineCache{rows: map[offlineHandle][]offlineRow{}} }
func (c *offlineCache) admit(cfg, raw []byte, mode string) (offlineHandle, error) {
	rows, err := offlineVerify(cfg, raw, mode)
	if err != nil {
		return offlineHandle{}, err
	}
	// Nested fixed hashes bind exact trusted config, requested property and packet.
	a, b := sha256.Sum256(cfg), sha256.Sum256(raw)
	key := sha256.New()
	key.Write(a[:])
	key.Write([]byte(mode))
	key.Write(b[:])
	var handle offlineHandle
	copy(handle[:], key.Sum(nil))
	c.rows[handle] = append([]offlineRow(nil), rows...)
	return handle, nil
}
func (c *offlineCache) lookup(handle offlineHandle) []offlineRow {
	return append([]offlineRow(nil), c.rows[handle]...)
}

type offlineChildCase struct {
	Mode     string
	Packet   []byte
	Expected []offlineRow
	Invalid  [][]byte
}
type offlineChildInput struct {
	Config []byte
	Cases  []offlineChildCase
}

func TestOfflineReceiptReceiverChild(t *testing.T) {
	if os.Getenv("UTXO_RECEIPT_RECEIVER_CHILD") != "1" {
		t.Skip("subprocess-only verifier")
	}
	var input offlineChildInput
	readerOK(t, json.NewDecoder(os.Stdin).Decode(&input))
	if len(input.Cases) != 2 {
		t.Fatal("expected both methods")
	}
	for _, c := range input.Cases {
		got, err := offlineVerify(input.Config, c.Packet, c.Mode)
		readerOK(t, err)
		if !reflect.DeepEqual(got, c.Expected) {
			t.Fatal("independent receiver rows differ")
		}
		for _, bad := range c.Invalid {
			if _, err = offlineVerify(input.Config, bad, c.Mode); err == nil {
				t.Fatalf("independent receiver accepted invalid %s", c.Mode)
			}
		}
	}
}
func offlineIndependentReceiver(t *testing.T, cfg []byte, packets map[string][]byte, want []offlineRow) {
	t.Helper()
	input := offlineChildInput{Config: cfg}
	for _, mode := range []string{offlineN, offlineB} {
		c := offlineChildCase{Mode: mode, Packet: packets[mode], Expected: want}
		for _, kind := range []string{"result", "parent-signature"} {
			c.Invalid = append(c.Invalid, offlineTamper(t, c.Packet, kind))
		}
		if mode == offlineB {
			c.Invalid = append(c.Invalid, offlineTamper(t, c.Packet, "body"), packets[offlineN])
		}
		input.Cases = append(input.Cases, c)
	}
	exe, err := os.Executable()
	readerOK(t, err)
	cmd := exec.Command(exe, "-test.run=^TestOfflineReceiptReceiverChild$", "-test.count=1", "-test.v")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"UTXO_RECEIPT_RECEIVER_CHILD=1", "PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "TEMP=" + cmd.Dir, "TMP=" + cmd.Dir, "TMPDIR=" + cmd.Dir}
	cmd.Stdin = bytes.NewReader(offlineJSON(t, input))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("independent public-only receiver: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("--- PASS: TestOfflineReceiptReceiverChild")) {
		t.Fatalf("receiver did not execute: %s", out)
	}
}
func TestOfflineReceiptIndependentReceiver(t *testing.T) {
	f := newReceiptFixture(t, 3, 2, true)
	cfg := offlineConfigBytes(t, f)
	want := offlineExpected(t, f, 2)
	packets := map[string][]byte{offlineN: offlineExport(t, f, 2, offlineN), offlineB: offlineExport(t, f, 2, offlineB)}
	readerOK(t, f.db.Close())
	offlineIndependentReceiver(t, cfg, packets, want)
}

func TestOfflineReceiptTransportAndCacheIdentity(t *testing.T) {
	f := newReceiptFixture(t, 3, 2, true)
	cfg := offlineConfigBytes(t, f)
	cache := newOfflineCache()
	var first offlineHandle
	for _, mode := range []string{offlineN, offlineB} {
		raw := offlineExport(t, f, 2, mode)
		sum := 0
		for _, size := range offlineComponents(t, raw) {
			sum += size
		}
		if sum != len(raw) {
			t.Fatalf("component bytes %d != actual packet bytes %d", sum, len(raw))
		}
		handle, err := cache.admit(cfg, raw, mode)
		readerOK(t, err)
		if mode == offlineN {
			first = handle
			var packet map[string]json.RawMessage
			readerOK(t, json.Unmarshal(raw, &packet))
			for _, field := range []string{"Body", "OriginalCommit", "Repair", "CommandIndex"} {
				if _, exists := packet[field]; exists {
					t.Fatalf("N packet contains representation evidence %s", field)
				}
			}
		} else {
			if handle == first {
				t.Fatal("cache handle did not bind method and packet")
			}
			var packet map[string]json.RawMessage
			readerOK(t, json.Unmarshal(raw, &packet))
			if _, exists := packet["Parents"]; exists {
				t.Fatal("B duplicated parent certificates already authenticated in Body")
			}
		}
		// A byte-distinct pinned config that decodes to the same settings must
		// receive a distinct handle; arbitrary caller edits cannot reuse it.
		second, err := cache.admit(append(bytes.Clone(cfg), '\n'), raw, mode)
		readerOK(t, err)
		if second == handle {
			t.Fatal("cache handle does not bind exact trusted configuration bytes")
		}
		var wrong offlinePublicConfig
		readerOK(t, json.Unmarshal(cfg, &wrong))
		wrong.Network[0] ^= 1
		before := len(cache.rows)
		if _, err = cache.admit(offlineJSON(t, wrong), raw, mode); err == nil || len(cache.rows) != before {
			t.Fatal("wrong trusted configuration admitted rows")
		}
	}
}

type offlineSample struct {
	Method    string `json:"method"`
	Iteration int    `json:"iteration"`
	ExportNS  int64  `json:"exportNS"`
	VerifyNS  int64  `json:"verifyNS"`
	TotalNS   int64  `json:"totalNS"`
}
type offlineCachedSample struct {
	Method         string `json:"method"`
	Iteration      int    `json:"iteration"`
	CachedLookupNS int64  `json:"cachedLookupNS"`
}
type offlineMethodReport struct {
	Method              string         `json:"method"`
	PacketBytes         int            `json:"packetBytes"`
	PacketBytesPerSlot  float64        `json:"packetBytesPerSlot"`
	Components          map[string]int `json:"components"`
	RetainedPacketBytes int            `json:"retainedPacketBytes"`
	RetainedCacheBytes  int            `json:"retainedCacheBytes"`
}
type offlineConfigReport struct {
	N                   int                   `json:"n"`
	M                   int                   `json:"m"`
	SetupTimingNS       map[string]int64      `json:"setupTimingNS"`
	PublicConfigBytes   int                   `json:"publicConfigBytes"`
	IndependentReceiver bool                  `json:"independentReceiver"`
	ExpectedRows        []offlineRow          `json:"expectedRows"`
	Methods             []offlineMethodReport `json:"methods"`
	Samples             []offlineSample       `json:"samples"`
	CachedSamples       []offlineCachedSample `json:"cachedSamples"`
}
type offlineReport struct {
	Schema         string                `json:"schema"`
	Environment    map[string]any        `json:"environment"`
	Contracts      map[string]string     `json:"contracts"`
	ColdSamples    int                   `json:"coldSamples"`
	Prewarm        int                   `json:"prewarm"`
	CachedSamples  int                   `json:"cachedSamples"`
	Configurations []offlineConfigReport `json:"configurations"`
}

// Component bytes count the actual JSON/base64 wire, including field names.
// Their sum plus envelope equals packetBytes; binary payload sizes are not
// substituted for transport sizes. Public trust is retained separately.
func offlineComponents(t *testing.T, raw []byte) map[string]int {
	t.Helper()
	var fields map[string]json.RawMessage
	readerOK(t, json.Unmarshal(raw, &fields))
	sizes := map[string]int{"envelope": 2 + len(fields) - 1}
	for key, value := range fields {
		sizes[key] = len(offlineJSON(t, key)) + 1 + len(value)
	}
	return sizes
}
func TestOfflineReceiptExperiment(t *testing.T) {
	dir := os.Getenv("UTXO_RECEIPT_EXPERIMENT")
	if dir == "" {
		t.Skip("set UTXO_RECEIPT_EXPERIMENT to a fresh output directory")
	}
	// No caller-controlled sample-count override: the declared run is 30 paired
	// cold samples per method/config, prewarm 5, and 100 warm handle lookups.
	report := offlineReport{Schema: "c3-offline-receipt-v1", ColdSamples: 30, Prewarm: 5, CachedSamples: 100,
		Environment: map[string]any{"goVersion": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0), "numCPU": runtime.NumCPU(), "pid": os.Getpid()},
		Contracts: map[string]string{
			offlineN:    "T1: real signed compensation execution plus verified parent certificate bound to effect ParentFact/Output; no repaired body, repair command, openings or original body.",
			offlineB:    "Same T1 rows, parents reused from mandatory authenticated Body without external copies or repeated QC verification; LeanHistoricalReceipt + VerifyHistoricalAuthorization authenticates exact applied revised body and original full BlockID compatibility (T2).",
			"trust":     "Caller-pinned chain/network/four public validators/rules/org configuration/CH public modulus; signed correct execution under existing BFT assumptions, positive historical evidence only.",
			"cold":      "Serialize source evidence, decode public config and packet to fresh objects, fully verify, cross-check economic rows. Warm OS/runtime; no cold-disk claim. exportNS and verifyNS are paired, totalNS wall elapsed includes both.",
			"cache":     "Contiguous immutable verified economic rows with prevalidated content/config/method handle. Lookup clones rows. Admission fully verifies; no arbitrary packet acceptance or latestness claim. Same strategy for both methods.",
			"retention": "Actual compact JSON encoding size: packet separately; cache stores handle+verified rows only, not packets or trust. Not RSS or a database estimate. B cache serves economic rows; T2 full-body reuse would require retaining B packet separately.",
			"setup":     "Fixture TimingNS: actual input/part share adaptation+command completion db.View (including immutable reads), repair FinalizeBlock, repair Commit. Excludes transaction fixture construction and block/committee signing.",
		}}
	readerOK(t, os.MkdirAll(filepath.Dir(dir), 0755))
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatalf("output must be a fresh directory: %v", err)
	}
	for _, n := range []int{32, 256} {
		for _, m := range []int{1, 8} {
			t.Run(fmt.Sprintf("n%d-m%d", n, m), func(t *testing.T) {
				f := newReceiptFixture(t, n, m, true)
				cfg := offlineConfigBytes(t, f)
				want := offlineExpected(t, f, m)
				run := offlineConfigReport{N: n, M: m, SetupTimingNS: f.TimingNS, PublicConfigBytes: len(cfg), ExpectedRows: want}
				packets := map[string][]byte{}
				caches := map[string]*offlineCache{}
				handles := map[string]offlineHandle{}
				for _, mode := range []string{offlineN, offlineB} {
					packets[mode] = offlineExport(t, f, m, mode)
					cache := newOfflineCache()
					handle, err := cache.admit(cfg, packets[mode], mode)
					readerOK(t, err)
					caches[mode], handles[mode] = cache, handle
					if !reflect.DeepEqual(cache.lookup(handle), want) {
						t.Fatal("packet output differs from independent expected rows")
					}
					retained := offlineJSON(t, struct {
						Handle offlineHandle
						Rows   []offlineRow
					}{handle, cache.rows[handle]})
					run.Methods = append(run.Methods, offlineMethodReport{Method: mode, PacketBytes: len(packets[mode]), PacketBytesPerSlot: float64(len(packets[mode])) / float64(m), Components: offlineComponents(t, packets[mode]), RetainedPacketBytes: len(packets[mode]), RetainedCacheBytes: len(retained)})
					prefix := fmt.Sprintf("n%d-m%d-%s", n, m, map[string]string{offlineN: "N", offlineB: "B"}[mode])
					readerOK(t, os.WriteFile(filepath.Join(dir, prefix+"-packet.json"), packets[mode], 0644))
				}
				readerOK(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("n%d-m%d-public-config.json", n, m)), cfg, 0644))
				// Closing fixture storage before checking an independently launched receiver
				// makes accidental application-store dependence visible.
				readerOK(t, f.db.Close())
				offlineIndependentReceiver(t, cfg, packets, want)
				run.IndependentReceiver = true
				for i := -report.Prewarm; i < report.ColdSamples; i++ {
					order := []string{offlineN, offlineB}
					if (i+report.Prewarm)%2 != 0 {
						order[0], order[1] = order[1], order[0]
					}
					for _, mode := range order {
						start := time.Now()
						raw := offlineExport(t, f, m, mode)
						exported := time.Now()
						rows, err := offlineVerify(cfg, raw, mode)
						done := time.Now()
						readerOK(t, err)
						if !reflect.DeepEqual(rows, want) {
							t.Fatal("cold verification differs")
						}
						if i >= 0 {
							run.Samples = append(run.Samples, offlineSample{mode, i, exported.Sub(start).Nanoseconds(), done.Sub(exported).Nanoseconds(), done.Sub(start).Nanoseconds()})
						}
					}
				}
				for i := 0; i < report.Prewarm; i++ {
					for _, mode := range []string{offlineN, offlineB} {
						if !reflect.DeepEqual(caches[mode].lookup(handles[mode]), want) {
							t.Fatal("cache prewarm differs")
						}
					}
				}
				for i := 0; i < report.CachedSamples; i++ {
					order := []string{offlineN, offlineB}
					if i%2 != 0 {
						order[0], order[1] = order[1], order[0]
					}
					for _, mode := range order {
						cache, handle := caches[mode], handles[mode]
						start := time.Now()
						rows := cache.lookup(handle)
						elapsed := time.Since(start).Nanoseconds()
						if !reflect.DeepEqual(rows, want) {
							t.Fatal("warm verification differs")
						}
						run.CachedSamples = append(run.CachedSamples, offlineCachedSample{mode, i, elapsed})
					}
				}
				report.Configurations = append(report.Configurations, run)
			})
		}
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	readerOK(t, err)
	readerOK(t, os.WriteFile(filepath.Join(dir, "report.json"), raw, 0644))
	t.Logf("offline receipt raw samples/report: %s", filepath.Join(dir, "report.json"))
}
