package gateway

import (
	"testing"
	"utxo/protocol"
)

func TestOrderDirectSourcesFirst(t *testing.T) {
	org := protocol.Digest("org")
	cfg := protocol.Digest("cfg")
	parent := protocol.DirectPayment{Tx: protocol.FastTx{Body: protocol.TxBody{Certifier: org, Config: cfg}}, Certificate: protocol.OutputCertificate{QC: protocol.SpendQC{Fact: protocol.SpendFactID{1}}}}
	child := parent
	child.Certificate.QC.Fact = protocol.SpendFactID{2}
	child.InputCertificates = []protocol.InputCertificate{{Certificate: protocol.OutputCertificate{Summary: protocol.OutputSummary{Issuer: org, Config: cfg}, QC: parent.Certificate.QC}}}
	input := []protocol.DirectPayment{child, parent}
	got, err := OrderDirectSourcesFirst(input)
	if err != nil || got[0].Certificate.QC.Fact != parent.Certificate.QC.Fact {
		t.Fatal("parent not prioritized", err)
	}
	if input[0].Certificate.QC.Fact != child.Certificate.QC.Fact {
		t.Fatal("caller queue mutated")
	}
	parent.InputCertificates = []protocol.InputCertificate{{Certificate: protocol.OutputCertificate{Summary: protocol.OutputSummary{Issuer: org, Config: cfg}, QC: child.Certificate.QC}}}
	if _, err := OrderDirectSourcesFirst([]protocol.DirectPayment{child, parent}); err == nil {
		t.Fatal("dependency cycle accepted")
	}
	got, err = OrderDirectSourcesFirst([]protocol.DirectPayment{child})
	if err != nil || len(got) != 1 {
		t.Fatal("missing parent blocked standalone child")
	}
}
