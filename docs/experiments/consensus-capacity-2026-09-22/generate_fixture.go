//go:build ignore

// Offline workload preparation only. Real owner and 3-member laboratory
// signatures are created before timing; committee nodes still verify all bytes.
// No signing key is printed or exported. This does not benchmark organization
// approval, wallet persistence, or the fast-receipt path.
package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

func readJSON(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func key(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.NewKeyFromSeed(b[:32]), b) {
		return nil, fmt.Errorf("invalid laboratory signing key")
	}
	return ed25519.PrivateKey(b), nil
}

func generate(labdir, output string, count int, cycle bool) error {
	var lab struct {
		Network string
		Owners  [2]string
		Nodes   []struct{ Name, Config string }
	}
	if err := readJSON(filepath.Join(labdir, "lab.json"), &lab); err != nil {
		return err
	}
	var network struct {
		Genesis       state.Genesis
		Organizations []protocol.OrgConfig
		Direct        *rules.DirectSettings
		Schedule      rules.Schedule
	}
	if err := readJSON(lab.Network, &network); err != nil {
		return err
	}
	if network.Direct == nil || len(network.Organizations) != 2 {
		return protocol.ErrRule
	}
	policy, err := network.Direct.Policy(network.Schedule, network.Organizations)
	if err != nil {
		return err
	}
	owner, err := key(lab.Owners[0])
	if err != nil {
		return err
	}
	recipient, err := key(lab.Owners[1])
	if err != nil {
		return err
	}
	owners := [2]ed25519.PrivateKey{owner, recipient}
	var publicOwners [2]protocol.PublicKey
	var memberKeys [2][3]ed25519.PrivateKey
	for oi, org := range network.Organizations {
		copy(publicOwners[oi][:], owners[oi][32:])
		for i := range memberKeys[oi] {
			for _, node := range lab.Nodes {
				if node.Name != fmt.Sprintf("org%d-member%d", oi, i) {
					continue
				}
				var config struct{ KeyFile string }
				if err := readJSON(node.Config, &config); err != nil {
					return err
				}
				memberKeys[oi][i], err = key(config.KeyFile)
				if err != nil {
					return err
				}
			}
			if len(memberKeys[oi][i]) != 64 || !bytes.Equal(memberKeys[oi][i][32:], org.Members[i][:]) {
				return protocol.ErrAuth
			}
		}
	}
	var origins []state.OriginOutput
	for _, origin := range network.Genesis.Outputs {
		if origin.Output.Recipient.Owner == publicOwners[0] || origin.Output.Recipient.Owner == publicOwners[1] {
			origins = append(origins, origin)
		}
	}
	if len(origins) == 0 || (!cycle && count > len(origins)) {
		return fmt.Errorf("only %d eligible genesis outputs for %d payments", len(origins), count)
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	written := 0
	for written < count {
		slot := written % len(origins)
		origin := origins[slot]
		oi := 0
		if origin.Output.Recipient.Owner == publicOwners[1] {
			oi = 1
		}
		org := network.Organizations[oi]
		target := protocol.NewDescriptor(network.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: network.Organizations[1-oi].Org}, owners[1-oi])
		body := protocol.TxBody{Wire: 4, Version: 4, Network: network.Genesis.Network, Kind: protocol.FastTransfer,
			Subject: publicOwners[oi], Certifier: org.Org, Config: org.Hash(), Epoch: org.Epoch, Rules: policy.Rules(),
			Inputs:  []protocol.Input{{Kind: protocol.FinalInput, Output: origin.ID, Evidence: origin.Fact}},
			Outputs: []protocol.Output{{Asset: protocol.AssetCAL, Amount: origin.Output.Amount, Recipient: target}},
			Fee:     protocol.FeeTerms{Source: protocol.OrgReserve, Account: org.Org, Version: 1, Maximum: 1000},
			Work:    protocol.WorkLimit{Execution: 100000, Bytes: 10000000, Depth: 1, Ancestors: 1}}
		for _, grant := range network.Genesis.Grants {
			if grant.Organization != org.Hash() {
				continue
			}
			body.Admission = append(body.Admission, protocol.AdmissionRef{Key: grant.Key, Grant: grant.ID})
			if grant.Key.Kind == protocol.ResourcePolicy {
				body.Fee.Policy = grant.Key.Account
			}
		}
		nonce := protocol.Digest("COMMITTEE_TPS_FIXTURE", origin.ID[:])
		copy(body.Nonce[:], nonce[:])
		body.Intent = body.IntentID()
		tx, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: origin.Output}}, policy.Key)
		if err != nil {
			return err
		}
		tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), owners[oi])}
		vector, err := rules.PrepareDirectVector(tx, policy)
		if err != nil {
			return err
		}
		certificate := protocol.OutputCertificate{Summary: protocol.SummaryFor(tx, vector)}
		certificate.QC.Fact = certificate.Summary.Fact()
		for i, private := range memberKeys[oi] {
			certificate.QC.Votes = append(certificate.QC.Votes, protocol.SignSpend(certificate.QC.Fact, uint16(i), private))
		}
		if err := certificate.Verify(org); err != nil {
			return err
		}
		raw, err := (protocol.DirectPayment{Tx: tx, Certificate: certificate}).Submission().MarshalBinary()
		if err != nil {
			return err
		}
		if err := encoder.Encode(struct {
			Raw    []byte `json:"raw"`
			Fact   string `json:"fact"`
			Height int64  `json:"height"`
		}{raw, protocol.Hash(certificate.QC.Fact).String(), 0}); err != nil {
			return err
		}
		written++
		if cycle {
			origins[slot] = state.OriginOutput{ID: certificate.Summary.OutputID(0), Output: tx.Body.Outputs[0], Fact: protocol.CreationIdentity(network.Genesis.Network, tx.ID(), 0, 0)}
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	fmt.Printf("prepared %d signed public payments; roots=%d cycle=%t; offline, outside all timing\n", written, len(origins), cycle)
	return nil
}

func main() {
	if len(os.Args) != 4 && len(os.Args) != 5 {
		panic("usage: generate-committee-fixture <lab> <new-output.jsonl> <count> [cycle]")
	}
	count, err := strconv.Atoi(os.Args[3])
	if err == nil && count > 0 {
		err = generate(os.Args[1], os.Args[2], count, len(os.Args) == 5 && os.Args[4] == "cycle")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
