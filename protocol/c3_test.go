package protocol

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"math"
	"sort"
	"testing"
	"utxo/crypto/chameleon"
)

func c3TxFixture() (FastTx, ed25519.PrivateKey, OrgConfig, []ed25519.PrivateKey) {
	owner := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{42}, 32))
	net := Digest("net")
	org := Digest("issuer")
	cfg := OrgConfig{Network: net, Org: org, Epoch: 1}
	var keys []ed25519.PrivateKey
	for i := 0; i < 4; i++ {
		k := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{byte(i + 1)}, 32))
		copy(cfg.Members[i][:], k.Public().(ed25519.PublicKey))
		keys = append(keys, k)
	}
	desc := NewDescriptor(net, Route{Kind: OrgRoute, Org: org}, owner)
	b := TxBody{Wire: DirectWireVersion, Version: DirectVersion, Kind: FastTransfer, Network: net, Certifier: org, Config: cfg.Hash(), Epoch: 1, Rules: RuleIDs{Fee: Digest("c3"), Work: Digest("work"), Accounting: Digest("accounting")}, Inputs: []Input{{Kind: FinalInput, Output: OutputID(Digest("in")), Evidence: Digest("evidence")}}, Outputs: []Output{{Asset: AssetCAL, Amount: 1, Recipient: desc}}, Fee: FeeTerms{Source: OrgReserve, Account: Digest("sponsor"), Policy: Digest("policy"), Version: 1, Maximum: 10}, Work: WorkLimit{Execution: 1, Bytes: 1, Depth: 1, Ancestors: 1}, Admission: []AdmissionRef{{Key: ResourceKey{Kind: ResourceCAL, Account: org, Version: 1}, Grant: Digest("calgrant")}}}
	copy(b.Subject[:], owner.Public().(ed25519.PublicKey))
	tx := FastTx{Body: b, Claims: []InputClaim{{Output: b.Outputs[0]}}, Funding: []Funding{{Ref: Hash(b.Inputs[0].Output)}}}
	tx.Commitments = make([]chameleon.Commitment, 1)
	tx.Auth = []OwnerAuth{SignOwner(tx.ID(), owner)}
	return tx, owner, cfg, keys
}

func TestC3BindingAndVersionedEncoding(t *testing.T) {
	tx, owner, cfg, keys := c3TxFixture()
	legacyBody := tx.Body.encode()
	legacySummary := SummaryFor(tx, AdmissionVector{{Key: tx.Body.Admission[0].Key, Cap: 1}}).encode()
	tx.Body.Fee.C3 = testC3()
	tx.Body.Fee.C3.Resources = FeeResourcesFor(tx.Body)
	tx.Auth = []OwnerAuth{SignOwner(tx.ID(), owner)}
	if e := tx.VerifyAuth(); e != nil {
		t.Fatal(e)
	}
	raw, e := tx.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := DecodeFastTx(raw)
	if e != nil || decoded.Body.Fee.C3 != tx.Body.Fee.C3 {
		t.Fatalf("tx roundtrip %v", e)
	}
	s := SummaryFor(tx, AdmissionVector{{Key: tx.Body.Admission[0].Key, Cap: 1}})
	if s.C3 != tx.Body.Fee.C3 {
		t.Fatal("summary drops commitment")
	}
	if binary.BigEndian.Uint64(s.encode()) != C3SummaryVersion {
		t.Fatal("legacy decoder cannot distinguish C3")
	}
	cert := OutputCertificate{Summary: s, QC: SpendQC{Fact: s.Fact()}}
	for i := 0; i < 3; i++ {
		cert.QC.Votes = append(cert.QC.Votes, SignSpend(s.Fact(), uint16(i), keys[i]))
	}
	raw, e = cert.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	got, e := DecodeOutputCertificate(raw)
	if e != nil || got.Verify(cfg) != nil {
		t.Fatalf("QC roundtrip %v", e)
	}
	for _, mutate := range []func(*FeeCommitment){func(c *FeeCommitment) { c.Beneficiary = Digest("evil") }, func(c *FeeCommitment) { c.Quote++ }, func(c *FeeCommitment) { c.Pool = Digest("other") }, func(c *FeeCommitment) { c.Revenue = Digest("other") }, func(c *FeeCommitment) { c.Policy = Digest("other") }, func(c *FeeCommitment) { c.Grant = Digest("other") }} {
		changed := tx
		mutate(&changed.Body.Fee.C3)
		if changed.VerifyAuth() == nil {
			t.Fatal("owner tamper accepted")
		}
		bad := got
		mutate(&bad.Summary.C3)
		if bad.Verify(cfg) == nil {
			t.Fatal("QC tamper accepted")
		}
	}
	bad := tx
	bad.Body.Fee.C3.Resources.Work.Bytes++
	bad.Auth = []OwnerAuth{SignOwner(bad.ID(), owner)}
	if bad.VerifyAuth() == nil {
		t.Fatal("false resource descriptor")
	}
	bad = tx
	bad.Body.Fee.C3.Sponsor = Digest("unrelated")
	bad.Auth = []OwnerAuth{SignOwner(bad.ID(), owner)}
	if bad.VerifyAuth() == nil {
		t.Fatal("false payer")
	}
	tx.Body.Fee.C3 = FeeCommitment{}
	if !bytes.Equal(tx.Body.encode(), legacyBody) {
		t.Fatal("legacy body changed")
	}
	if !bytes.Equal(SummaryFor(tx, s.Admission).encode(), legacySummary) {
		t.Fatal("legacy summary changed")
	}
	// A C3 summary carrying a disabled payload is noncanonical, not legacy.
	ebuf := new(Encoder)
	ebuf.U16(404)
	payload := append([]byte{}, legacySummary...)
	binary.BigEndian.PutUint64(payload, C3SummaryVersion)
	ebuf.Bytes(payload)
	if _, e := DecodeOutputCertificate(ebuf.Data()); e == nil {
		t.Fatal("missing C3 extension accepted")
	}
}

func testC3() FeeCommitment {
	return FeeCommitment{Version: 1, Policy: Digest("c3"), Issuer: Digest("issuer"), Pool: Digest("pool"), Grant: Digest("grant"), GrantVersion: 1, Quote: 10, Sponsor: Digest("sponsor"), Revenue: Digest("revenue"), Beneficiary: Digest("committee"), Resources: FeeResources{Inputs: 1, Outputs: 1, Work: WorkLimit{Execution: 1, Bytes: 1, Depth: 1, Ancestors: 1}}, Schedule: FeeSchedule{AttachReward: 1, ResolveReward: 1, Open: 1, Close: 1, Compensate: 1, Repay: 1}, Budget: 6}
}

func TestC3BudgetAndCanonicalCommitment(t *testing.T) {
	c := testC3()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	p, err := c.Plan()
	if err != nil || p.Maximum != 6 || p.RewardCeiling != 2 {
		t.Fatalf("plan %+v %v", p, err)
	}
	raw, err := c.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeFeeCommitment(raw)
	if err != nil || got != c {
		t.Fatalf("roundtrip %+v %v", got, err)
	}
	for _, bad := range [][]byte{raw[:len(raw)-1], append(append([]byte{}, raw...), 0), append([]byte{2}, raw[1:]...)} {
		if _, err := DecodeFeeCommitment(bad); err == nil {
			t.Fatal("malformed accepted")
		}
	}
	c.Budget++
	if c.Validate() == nil {
		t.Fatal("trusted client budget")
	}
	c = testC3()
	c.Schedule.RegisterPerInput = 3
	c.Schedule.RegisterPerOutput = 5
	c.Schedule.RegisterPerResource = 7
	c.Schedule.RegisterPerExecution = 11
	c.Schedule.RegisterPerByte = 13
	c.Resources.Admissions = 2
	c.Resources.FeeInputs = 1
	p, err = c.Plan()
	if err != nil || p.Maximum != 55 {
		t.Fatalf("resource cost %+v %v", p, err)
	}
	c = testC3()
	c.Schedule.Open = math.MaxUint64
	if _, err := c.Plan(); err == nil {
		t.Fatal("overflow accepted")
	}
	c = testC3()
	c.Resources.Outputs = 33
	if c.Validate() == nil {
		t.Fatal("unbounded outputs")
	}
}

func TestC3ProtectedPoolAndInactiveValues(t *testing.T) {
	for _, field := range []string{"sponsor", "revenue", "beneficiary"} {
		c := testC3()
		switch field {
		case "sponsor":
			c.Sponsor = c.Pool
		case "revenue":
			c.Revenue = c.Pool
		case "beneficiary":
			c.Beneficiary = c.Pool
		}
		if c.Validate() == nil {
			t.Fatalf("protected pool aliases %s", field)
		}
	}
	tx, owner, _, _ := c3TxFixture()
	tx.Body.Fee.C3.Quote = 1
	tx.Auth = []OwnerAuth{SignOwner(tx.ID(), owner)}
	if tx.VerifyAuth() == nil {
		t.Fatal("nonzero disabled commitment silently omitted")
	}
}

func TestC3PolicyVersionAndDistinctSignedFeeOwner(t *testing.T) {
	tx, owner, _, _ := c3TxFixture()
	payer := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, 32))
	tx.Body.Fee = FeeTerms{Source: OwnerFinalUTXO, Maximum: 10, Inputs: []Input{{Kind: FinalInput, Output: OutputID(Digest("feeinput")), Evidence: Digest("feeevidence")}}, Refund: NewDescriptor(tx.Body.Network, Route{Kind: OrgRoute, Org: tx.Body.Certifier}, payer), C3: testC3()}
	c := &tx.Body.Fee.C3
	c.Policy = Digest("accepted-old-policy")
	c.Sponsor = Hash{}
	copy(c.Payer[:], payer.Public().(ed25519.PublicKey))
	c.Resources = FeeResourcesFor(tx.Body)
	tx.Auth = []OwnerAuth{SignOwner(tx.ID(), owner), SignOwner(tx.ID(), payer)}
	sort.Slice(tx.Auth, func(i, j int) bool { return bytes.Compare(tx.Auth[i].Owner[:], tx.Auth[j].Owner[:]) < 0 })
	if e := tx.VerifyAuth(); e != nil {
		t.Fatalf("separate payer and accepted policy: %v", e)
	}
	tx.Auth = []OwnerAuth{SignOwner(tx.ID(), owner)}
	if tx.VerifyAuth() == nil {
		t.Fatal("unsigned fee payer")
	}
}
