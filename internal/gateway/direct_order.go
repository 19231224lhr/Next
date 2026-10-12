package gateway

import "utxo/protocol"

// OrderDirectSourcesFirst orders an already selected, bounded background batch.
// It does not authenticate payments or wait for absent parents. The execution
// path must perform its normal validation. Foreground certification is unchanged.
func OrderDirectSourcesFirst(payments []protocol.DirectPayment) ([]protocol.DirectPayment, error) {
	if len(payments) > 4096 {
		return nil, protocol.ErrRule
	}
	positions := make(map[protocol.SpendFactID]int, len(payments))
	for i, p := range payments {
		if _, ok := positions[p.Certificate.QC.Fact]; ok {
			return nil, protocol.ErrRule
		}
		positions[p.Certificate.QC.Fact] = i
	}
	marks := make([]uint8, len(payments))
	ordered := make([]protocol.DirectPayment, 0, len(payments))
	var visit func(int) error
	visit = func(i int) error {
		if marks[i] == 2 {
			return nil
		}
		if marks[i] == 1 {
			return protocol.ErrRule
		}
		marks[i] = 1
		p := payments[i]
		for _, parent := range p.InputCertificates {
			if parent.Certificate.Summary.Issuer != p.Tx.Body.Certifier || parent.Certificate.Summary.Config != p.Tx.Body.Config {
				continue
			}
			if j, ok := positions[parent.Certificate.QC.Fact]; ok {
				if err := visit(j); err != nil {
					return err
				}
			}
		}
		marks[i] = 2
		ordered = append(ordered, p)
		return nil
	}
	for i := range payments {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}
