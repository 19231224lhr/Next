package rules

import (
	"bytes"
	"encoding/json"
	"sort"
	"utxo/protocol"
)

// C3Settings is a genesis-frozen registry. Current selects new quotes only;
// every accepted historical policy remains independently authenticated.
type C3Policy struct {
	ID       protocol.Hash
	Schedule protocol.FeeSchedule
}
type C3Issuer struct {
	Config               protocol.Hash
	Funding              protocol.AdmissionRef
	Revenue, Beneficiary protocol.Hash
}
type C3Settings struct {
	Current  protocol.Hash
	Policies []C3Policy
	Issuers  []C3Issuer
}

func (s *C3Settings) clone() *C3Settings {
	if s == nil {
		return nil
	}
	b, _ := json.Marshal(s)
	var c C3Settings
	_ = json.Unmarshal(b, &c)
	return &c
}
func (s *C3Settings) Validate(orgs map[protocol.Hash]protocol.OrgConfig) error {
	if s == nil {
		return nil
	}
	policies := map[protocol.Hash]bool{}
	for _, p := range s.Policies {
		if p.ID == (protocol.Hash{}) || policies[p.ID] {
			return protocol.ErrRule
		}
		policies[p.ID] = true
	}
	if !policies[s.Current] || len(s.Issuers) == 0 {
		return protocol.ErrRule
	}
	configs := map[protocol.Hash]bool{}
	pools := map[protocol.Hash]bool{}
	for _, i := range s.Issuers {
		if _, ok := orgs[i.Config]; !ok || configs[i.Config] || i.Funding.Key.Kind != protocol.ResourceFUEL || i.Funding.Key.Version == 0 || i.Funding.Key.Account == (protocol.Hash{}) || i.Funding.Grant == (protocol.Hash{}) || i.Revenue == (protocol.Hash{}) || i.Beneficiary == (protocol.Hash{}) {
			return protocol.ErrRule
		}
		configs[i.Config] = true
		pools[i.Funding.Key.Account] = true
	}
	for _, i := range s.Issuers {
		if pools[i.Revenue] || pools[i.Beneficiary] || i.Revenue == i.Beneficiary {
			return protocol.ErrRule
		}
	}
	return nil
}
func (p DirectPolicy) C3Issuer(config protocol.Hash) (C3Issuer, bool) {
	if p.C3 != nil {
		for _, i := range p.C3.Issuers {
			if i.Config == config {
				return i, true
			}
		}
	}
	return C3Issuer{}, false
}
func (p DirectPolicy) IsC3Pool(account protocol.Hash) bool {
	if p.C3 != nil {
		for _, i := range p.C3.Issuers {
			if i.Funding.Key.Account == account {
				return true
			}
		}
	}
	return false
}
func (p DirectPolicy) ValidateC3(c protocol.FeeCommitment, config protocol.Hash) error {
	i, required := p.C3Issuer(config)
	if !c.Enabled() {
		if c != (protocol.FeeCommitment{}) || required {
			return protocol.ErrRule
		}
		return nil
	}
	if !required || c.Validate() != nil || c.Funding() != i.Funding || c.Revenue != i.Revenue || c.Beneficiary != i.Beneficiary || p.Organizations[config].Org != c.Issuer || p.IsC3Pool(c.Sponsor) || c.Sponsor == c.Revenue || c.Sponsor == c.Beneficiary {
		return protocol.ErrAuth
	}
	for _, policy := range p.C3.Policies {
		if policy.ID == c.Policy && policy.Schedule == c.Schedule {
			return nil
		}
	}
	return protocol.ErrAuth
}
func ValidateC3Summary(s protocol.OutputSummary, p DirectPolicy) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if err := p.ValidateC3(s.C3, s.Config); err != nil {
		return err
	}
	if !s.C3.Enabled() {
		return nil
	}
	c := s.C3
	if c.Resources.Outputs != uint32(len(s.Outputs)) || c.Resources.Admissions != uint32(len(s.Admission)) || c.Resources.Work.Depth != 1 || c.Resources.Work.Ancestors != 1 {
		return protocol.ErrRule
	}
	var cal uint64
	for _, out := range s.Outputs {
		var e error
		cal, e = protocol.Add(cal, out.Amount)
		if e != nil {
			return e
		}
	}
	seen := map[protocol.ResourceKind]int{}
	funded := false
	for j, a := range s.Admission {
		seen[a.Key.Kind]++
		switch a.Key.Kind {
		case protocol.ResourceCAL:
			if a.Key.Account != s.Issuer || a.Cap != cal {
				return protocol.ErrRule
			}
		case protocol.ResourceFUEL:
			if a.Key == c.Funding().Key {
				if a.Cap != c.Budget || s.Grants[j] != c.Grant {
					return protocol.ErrRule
				}
				funded = true
			} else if c.Sponsor == (protocol.Hash{}) || a.Key.Account != c.Sponsor || a.Cap != c.Quote {
				return protocol.ErrRule
			}
		case protocol.ResourcePolicy:
			if c.Sponsor == (protocol.Hash{}) || a.Cap != c.Quote {
				return protocol.ErrRule
			}
		case protocol.ResourceExecution:
			if a.Key.Account != s.Issuer || a.Cap != uint64(c.Resources.Work.Execution) {
				return protocol.ErrRule
			}
		case protocol.ResourceBytes:
			if a.Key.Account != s.Issuer || a.Cap != uint64(c.Resources.Work.Bytes) {
				return protocol.ErrRule
			}
		default:
			return protocol.ErrRule
		}
	}
	fuel, policy := 1, 0
	if c.Sponsor != (protocol.Hash{}) {
		fuel, policy = 2, 1
	}
	if !funded || seen[protocol.ResourceCAL] != 1 || seen[protocol.ResourceFUEL] != fuel || seen[protocol.ResourceExecution] != 1 || seen[protocol.ResourceBytes] != 1 || seen[protocol.ResourcePolicy] != policy {
		return protocol.ErrRule
	}
	return nil
}

// BindC3 replaces admission before NewFastTx/signing; it performs no RPC.
// With no configured C3 issuer it preserves legacy bytes exactly.
func BindC3(b *protocol.TxBody, p DirectPolicy, refs []protocol.AdmissionRef) error {
	i, ok := p.C3Issuer(b.Config)
	if !ok {
		return nil
	}
	c := protocol.FeeCommitment{Version: protocol.C3Version, Policy: p.C3.Current, Issuer: b.Certifier, Pool: i.Funding.Key.Account, Grant: i.Funding.Grant, GrantVersion: i.Funding.Key.Version, Quote: b.Fee.Maximum, Revenue: i.Revenue, Beneficiary: i.Beneficiary}
	for _, policy := range p.C3.Policies {
		if policy.ID == c.Policy {
			c.Schedule = policy.Schedule
		}
	}
	if b.Fee.Source == protocol.OwnerFinalUTXO {
		c.Payer = b.Fee.Refund.Owner
	} else if b.Fee.Source == protocol.OrgReserve {
		c.Sponsor = b.Fee.Account
	} else {
		return protocol.ErrRule
	}
	b.Admission = nil
	for _, r := range refs {
		if (r.Key.Kind == protocol.ResourceCAL || r.Key.Kind == protocol.ResourceExecution || r.Key.Kind == protocol.ResourceBytes) && r.Key.Account == b.Certifier || b.Fee.Source == protocol.OrgReserve && ((r.Key.Kind == protocol.ResourceFUEL && r.Key.Account == b.Fee.Account) || (r.Key.Kind == protocol.ResourcePolicy && r.Key.Account == b.Fee.Policy)) {
			b.Admission = append(b.Admission, r)
		}
	}
	b.Admission = append(b.Admission, i.Funding)
	sort.Slice(b.Admission, func(i, j int) bool {
		return bytes.Compare(b.Admission[i].Key.Encode(), b.Admission[j].Key.Encode()) < 0
	})
	c.Resources = protocol.FeeResourcesFor(*b)
	plan, err := c.Plan()
	if err != nil {
		return err
	}
	c.Budget = plan.Maximum
	b.Fee.C3 = c
	return p.ValidateC3(c, b.Config)
}
