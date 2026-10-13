"""Reverse only recorded Stage10 changes; preserve all earlier accepted stages."""
import pathlib, difflib
root = pathlib.Path(__file__).resolve().parents[4]
saved = pathlib.Path(__file__).resolve().parent
# Archived copies in saved use .go.txt; the names below intentionally address
# live internal/gateway source files, not the archived copies.
files = ['direct_relay.go','direct_outbox_cache.go','direct_install_bytes_test.go','direct_outbox_cache_test.go']
texts = {n:(root/'internal/gateway'/n).read_text() for n in files}
def replace(n, old, new):
    assert texts[n].count(old) == 1, (n,old)
    texts[n] = texts[n].replace(old,new)
replace('direct_outbox_cache.go','\t"sync"\n','')
replace('direct_outbox_cache.go','\tprepare func() (directPreparedPayment, error)\n','')
replace('direct_outbox_cache.go','directOutboxEntry{key: k, raw: bytes.Clone(raw), pending: pending, size: size}','directOutboxEntry{k, bytes.Clone(raw), pending, size}')
n='direct_outbox_cache.go'; start=texts[n].index('// preparation is called only');end=texts[n].index('// Callers own returned slices',start);texts[n]=texts[n][:start]+texts[n][end:]
n='direct_relay.go'
replace(n,'// Encoded representations belong to the same verified scheduling snapshot.\n// Retained bytes stay private; transports receive their own copies.','// Both representations belong to the same verified scheduling snapshot.\n// installBytes remains immutable while concurrent HTTP targets read it.')
replace(n,'type directPreparedPayment struct {\n\tinstallBytes    []byte\n\tsubmissionBytes []byte\n\tsubmissionErr   error\n\tissuer          protocol.Hash\n}','type directPreparedPayment struct {\n\tpayment protocol.DirectPayment\n\tinstallBytes []byte\n}')
replace(n,'*decode = decoded.preparation(state.Key(state.KeyOutbox, p.Fact[:]), p, r.prepareDirectPending)','*decode = sync.OnceValues(func() (directPreparedPayment, error) { return r.prepareDirectPending(p) })')
replace(n,'// Share immutable preparation among this payment\'s INSTALL targets.','// Share immutable decoded evidence among this payment\'s INSTALL targets.')
replace(n,'\tif task.target < 0 {\n\t\tif prepared.submissionErr != nil {\n\t\t\treturn prepared.submissionErr\n\t\t}', '\tpayment := prepared.payment\n\tc := payment.Certificate\n\tif task.target < 0 {\n\t\traw, err := payment.Submission().MarshalBinary()\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}')
replace(n,'r.Public.Submit(ctx, bytes.Clone(prepared.submissionBytes))','r.Public.Submit(ctx, raw)')
replace(n,'r.Members[prepared.issuer][task.target]','r.Members[c.Summary.Issuer][task.target]')
replace(n,'encoded.InstallDirectBytes(ctx, bytes.Clone(prepared.installBytes))','encoded.InstallDirectBytes(ctx, prepared.installBytes)')
replace(n,'\t\t\tpayment, decodeErr := protocol.DecodeDirectPayment(bytes.Clone(prepared.installBytes))\n\t\t\tif decodeErr != nil {\n\t\t\t\treturn decodeErr\n\t\t\t}\n','')
replace(n,'\tpublicBytes, publicErr := payment.Submission().MarshalBinary()\n\treturn directPreparedPayment{installBytes: p.Certificate, submissionBytes: publicBytes, submissionErr: publicErr, issuer: payment.Certificate.Summary.Issuer}, nil','\treturn directPreparedPayment{payment: payment, installBytes: p.Certificate}, nil')
n='direct_install_bytes_test.go';texts[n]=texts[n][:texts[n].index('func TestDirectPreparedEncodingIsolatesMutableConsumers')]
replace(n,'if !bytes.Equal(raw, expected) || &raw[0] == &prepared.installBytes[0] {\n\t\t\tt.Fatal("install did not use an independently owned verified snapshot")','if !bytes.Equal(raw, expected) || &raw[0] != &buffers[0][0] {\n\t\t\tt.Fatal("install did not share owned verified snapshot")')
replace(n,'\ttyped, err := protocol.DecodeDirectPayment(bytes.Clone(snapshot.Certificate))\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\ttyped.Tx.Auth[0].Signature[0] ^= 1','\tprepared.payment.Tx.Auth[0].Signature[0] ^= 1')
replace(n,'\tstartDirectRelay(t, r)\n\tfor range members {','\tstartDirectRelay(t, r)\n\tvar first []byte\n\tfor range members {')
replace(n,'\t\tcase raw := <-installed:\n\t\t\tif !bytes.Equal(raw, p.Certificate) {\n\t\t\t\tt.Fatal("scheduled siblings did not use verified preparation")','\t\tcase raw := <-installed:\n\t\t\tif first == nil { first = raw }\n\t\t\tif !bytes.Equal(raw, p.Certificate) || &raw[0] != &first[0] {\n\t\t\t\tt.Fatal("scheduled siblings did not share verified preparation")')
n='direct_outbox_cache_test.go';texts[n]=texts[n][:texts[n].index('func TestDirectOutboxPreparationReuseBoundAndReplacement')]
for n, text in texts.items():
    (root/'internal/gateway'/n).write_text(text)
print('Restored only recorded Stage10 edits in four gateway source/test files.')
