"""Apply audited sidebar drafts to the frozen paper baseline (run once)."""
from pathlib import Path
import json

ROOT=Path(__file__).resolve().parents[3]
P=ROOT/'docs/paper/review-2026-10-02'
E=Path(__file__).resolve().parent
G=json.loads((E/'gpt-latex-blocks.json').read_text(encoding='utf-8'))
C=json.loads((E/'claude-latex-blocks.json').read_text(encoding='utf-8'))

def read(n): return (P/n).read_text(encoding='utf-8')
def write(n,s): (P/n).write_text(s.rstrip()+'\n',encoding='utf-8')
def between(s,start,end,new):
    a=s.index(start); b=s.index(end,a)
    return s[:a]+new.rstrip()+'\n\n'+s[b:]

s=read('security.tex')
s=between(s,'If $T_2$ cannot execute',r'\begin{lemma}[Compensation',G[0])
s=between(s,'Continuation requires',r'\begin{lemma}[Economic closure',G[1])
s=s[:s.index(r'\noindent\textbf{Availability and guarantor risk.}')]+G[2]
write('security.tex',s)
s=read('model.tex')
s=s.replace(r'\noindent\textbf{Faults and cryptography.}',G[5]+'\n\n'+r'\noindent\textbf{Faults and cryptography.}')
s=s.replace('\\end{aligned}\\]\n, which','\\end{aligned}\\],\nwhich')
# Architecture choices are responsibilities and costs, not unmeasured scalability.
s=s.replace('Next aims at continuation before public execution, funded direct obligations, source resubmission from public evidence, and preserved successors across source resolution. Economic closure uses a public committee decision; representation follows outside the fast path.',
'''Next separates certification from public ordering so that a recipient can
request a successor after one organization round. The direct issuer supplies
additional backing for a missing source. This separation requires accounting
for commitments outside the public state: members reserve capacity before
signing, and the liability proof includes hidden certificates. Economic
closure uses a committee decision; historical representation follows on its
own evidence. The measurements evaluate these payment paths on the stated
deployment, rather than scale-out capacity.''')
write('model.tex',s)
s=read('protocol.tex')
# Retention is summarized in the security/resource subsection to keep the
# protocol specification and its existing exact invalidation guards compact.
s=s[:s.index('The three results are not interchangeable.')]+G[4]
s+='''
\n\\noindent\\textbf{Executable archival consumer.}
A local example exports the original and authorized body bytes at $(h,j,k)$,
the complete BlockID, Revision and Task identifiers, historical funding,
and current obligation state. It checks the exact Task body, reconstructed
parts, owner authorization, QC, and input opening. After repayment the
authorized bytes stay unchanged while the obligation becomes Recovered.
This byte-export contract supplements the economic tuple; the permanent
decision remains necessary. The example operates on an already verified,
unpruned replica and is not a remote proof protocol.

\\noindent\\textbf{Cryptographic instantiation.}
The implementation uses RSA chameleon hashing with a 2048-bit modulus,
$e=65537$, and context-bound SHAKE256 full-domain hashing into
$\\mathbb Z_N^*$. A trusted dealer generates a three-of-four threshold RSA
key using CIRCL~v1.6.5~\\cite{circl}, following Shoup's threshold
construction~\\cite{shoup}. Public adaptation ratios do not convey edit
authority; committed decisions, exact Tasks, and complete-identity guards
do. The supplement specifies the encoding, parameter assumptions, and
raw-group adaptation interface.
'''
write('protocol.tex',s)
# Resource table belongs next to the conditional progress statement.
s=read('security.tex').replace(r'\noindent\textbf{Conditional progress.}',G[3]+'\n\n'+r'\noindent\textbf{Conditional progress.}')
write('security.tex',s)

s=read('evaluation.tex')
s=between(s,'The local invalidation transition was added',r'\noindent\textbf{Setup',G[6].replace('with additions\nconfined to tests.','with node implementation unchanged. Test and driver additions are fingerprinted separately.'))
s=s.replace('These finite single-host trials do not measure crash recovery,\nsustainable throughput, or minimum capital.',G[7])
# Full decomposition lives in the supplement; the main text keeps its meaning.
anchor='Fast medians are 5.228'
idx=s.index(anchor)
s=s[:idx]+'''The driver polls every 5\\,ms and the follower every 25\\,ms; 250\\,ms
is the commit setting. Across the 99 inter-hop waits per waiting run,
earliest-replica application commit to driver observation totals
29.542, 29.681, and 29.347\\,s. This joint interval includes
successor-header availability, fetching, verification, local recording,
and replica differences. Wallet-internal timestamps do not separate these
terms. Thus 14.15 is the measured end-to-end mode ratio, not a pure
consensus speedup or polling penalty. The supplement gives the matched
intervals and observation-to-next-build times.

'''+s[idx:]
idx=s.index(r'\noindent\textbf{Archived evidence.}')
s=s[:idx]+'''\\subsection{Current-Build Boundary and Composition Checks}
\\label{sec:eval-boundaries}
On production snapshot \\texttt{9879f64}, deterministic regressions use
actual member approvals, ABCI execution and committed successor headers
to authenticate follower updates. Four independent 100-CAL requests
each obtain two votes and exhaust 200-CAL member slices without a QC.
A 150-CAL external top-up enables their completion without erasing locks;
the same top-up cannot resolve an all-honest same-input 2/2 split.

In two disjoint-input Intent-conflict rounds, A's reserve falls
300$\\to$200$\\to$100 CAL while three signers retain 200 CAL each.
The fourth member still has capacity, but cannot form a quorum alone.
The first descendant funds the second A-side request, and the final
descendant is spent through B; B uses fresh initial inputs each round.
User-funded runs retain 20,000 FUEL in complete source inputs, separately
from actual fees. The same trace with authorized organization funding
checks cost attribution. Independent gap, coverage, usage and asset sums
hold at every committed prefix; repeated decisions have no new effects.
These traces establish the stated resource and economic boundaries,
not an attack rate. Detailed ledgers and guard coverage are in the supplement.

Real threshold-adaptation tests cover both compensation--representation--
repayment and compensation--repayment--representation. A replica reopens
its persistent ledger at height 2 and catches up from retained original
commands while the source BlockStore holds revised bodies. Within each
fixed history, every replayed application root and ledger row matches.
A separate synchronous-store boundary interrupts approval after commit
but before signature return; restart preserves the input lock and debit,
and retry signs the same fact without a second reservation. Existing
rollback and restart tests cover follower cursor and restoration atomicity.

'''+s[idx:]
write('evaluation.tex',s)

# Claude draft, audited and shortened: no invented adoption or necessity.
write('abstract.tex',r'''\begin{abstract}
Continuous payments let a recipient spend an output before its creating
transaction executes publicly. Next is a native UTXO ledger in which a
recipient verifies a quorum-signed output certificate (TXCer) after one
certification round and can request a successor while public processing
continues. If the successor executes first, the committee records a funded
obligation against the direct issuer. Original signers can resubmit the
source from its publicly exposed certificate. A deadline-triggered decision
debits the issuer's reserve, and a late source repays it. Optional authorized
history commands then record the reserve reference at the original
coordinates while preserving owner authorizations, successor references,
and the complete BlockID. For four-member organizations and a four-validator
committee, each requiring three votes with at most one static Byzantine
member, we prove unique consumption, coverage of outstanding certificate
liability, and delay-neutral terminal CAL holdings for source-closed,
conflict-free payment sets. On one host with synchronous member commits,
a 100-hop chain reaches its last certified receipt in a median 4.557\,s,
versus 64.476\,s with per-hop waiting. Targeted boundary and restart tests
validate the conditions on capacity, economic closure, and historical reads.
\end{abstract}
\begin{IEEEkeywords}
Blockchain, certified payments, collateral, Byzantine fault tolerance,
chameleon hash.
\end{IEEEkeywords}
''')
s=read('introduction.tex')
c=C[7]
c=c.replace('The design has three contributions; C2 carries the central technical argument.','The design has three contributions, centered on direct responsibility for missing sources.')
c=c.replace('The separation is motivated by the payment path and by the placement of responsibility. We do not claim a scalability benefit for it beyond the measurements reported here.','The separation keeps public ordering off the recipient path and places responsibility at the certificate issuer.')
c=c.replace('Overlapping per-member budgets and budget exhaustion without a quorum certificate have precedents in Stingray~\\cite{stingray}. Our additions are responsibility for certificates that were never published, conservation of the dynamic Paid, Recovered, and Remaining quantities, and closure of the source obligation.','The accounting extends overlapping per-member budgets~\\cite{stingray} to hidden certificate liability, dynamic compensation and repayment, and source closure.')
c=c.replace('We describe it as an optional capability for readers that want the compensated funding source in the stored block. We do not claim that it is required for economic closure or that no alternative layout could serve such readers.','A runnable local archival consumer demonstrates this byte-export contract separately from economic-state queries.')
s=s[:s.index('Our insight')]+c
write('introduction.tex',s)
s=read('related.tex')
s=between(s,'Sui Lutris pairs','Next targets that chain.',C[9].replace("are the direct basis for Next's slice accounting, and we do not present them as new.","provide the counting precedent and a corresponding availability boundary for Next's slice accounting.").replace('Next adds three elements for that chain: responsibility for certificates that were never published, dynamic conservation of the Paid, Recovered, and Remaining quantities of each certificate, and closure of the source obligation.',''))
s=between(s,'Chameleon hashes permit','Reparo provides',C[10].replace('shoup2000','shoup'))
write('related.tex',s)
write('conclusion.tex',C[12])

# Supplement: complete numerical and cryptographic detail, leaving core
# assumptions and the liability proof in the main paper.
crypto=C[11][C[11].index(r'\subsection{Chameleon-Hash Instantiation}'):]
crypto=crypto.replace('shoup2000','shoup').replace("CIRCL's public-key constructor", "Next's public-key constructor").replace('that $e$ is acceptable','that $e=65537$').replace(r'Sec.~\ref{sec:c3-interface}', 'the historical read interface of the main paper')
crypto=crypto.replace(r'\cite{circl}',r'\cite{circl}').replace('The interface itself is ours, and we do not attribute its security properties to the library.','The raw-group interface is checked by its final public relation; the library reference alone is not a proof of that interface or of ledger authorization.')
supp=r'''\section{Current-Build Revision Validation}
\label{supp:major-revision}
Production snapshot \texttt{9879f64} retains the local reclamation patch
\texttt{bf71c4d}. The new fixtures and network driver additions are
fingerprinted independently; the measurements of \texttt{721800c} remain
unchanged. The artifact directory \texttt{major-revision-2026-10-03}
contains source manifests, raw logs, parsed ledgers and review closure.

'''+G[8]+'\n\n'+G[9]+r'''

\noindent\textbf{Fees and locked funds.}
Each successful normal payment in this trace spends 94 FUEL, and a
compensated consumer spends 99. The two rounds plus the final descendant
spend 480 FUEL: 430 rewards and 50 burned, out of escrow maxima totaling
5,000; 4,520 is returned. The user-funded trace separately retains two
10,000-FUEL inputs of the unexecuted sources. The organization-funded
trace debits B's authorized fee account by 480, with A's public FUEL
balance unchanged. A's local fee reservations still remain. CAL loss,
fee expenditure, and fee-input locks are distinct quantities.

\noindent\textbf{Public entry and atomicity coverage.}
The wire4 dispatcher admits DirectSubmission, reserve increases, clock
ticks, and restricted repair commands. DirectSubmission reconstructs and
verifies the current-organization QC, owner authorization and direct-input
certificates before common spend-state checks. Legacy ordinary transfers
require CommitteeRoute and are excluded by the wire4 dispatcher.
Organization-funded fees require a grant whose Policy Subject matches
the payer; unapproved sponsorship is rejected at member and public paths.
The zero/two-vote construction probes fail during canonical encoding;
existing public-verification negative tests separately cover malformed or
mismatched certificates. Compensation and representation cannot provide
an alternative arbitrary owner-spend entry.

The synchronous approval boundary test interrupts immediately after the
real database commit and before control returns to the signer. Reopening
the same store preserves the reservation and input lock; the same request
can be signed without another debit. Existing tests reject conflicting
requests after restart, roll back cursor and state together on failed
follower application, and apply restoration once across restart. These
checks retain committed state rather than simulate its loss.

\subsection{Waiting-Mode Timestamp Audit}
'''+G[10]+r'''

\subsection{Archival Byte-Export Example}
The runnable \texttt{TestHistoricalExportConsumer} emits a local export
with prefix, coordinates, complete BlockID, Revision, Task ID, original
and authorized body bytes, historical funding and current obligation
state. It verifies the exact Task body and revision, original part-set
identity, owner signature, QC and input CH relation. A late repayment
changes the economic answer without changing the authorized bytes.
This is an example consumer of verified local history, not a claimed
deployed client or remote authentication protocol. The permanent decision
and obligation are consulted on both read paths.

'''+crypto
write('supplement-revision.tex',supp)
s=read('supplement-main.tex').replace(r'\input{supplement-current}',r'\input{supplement-current}'+'\n'+r'\input{supplement-revision}')
s=s.replace(r'\end{document}',r'\bibliographystyle{IEEEtran}'+'\n'+r'\bibliography{references}'+'\n'+r'\end{document}')
write('supplement-main.tex',s)
s=read('references.bib')
s+='''
@misc{circl,
  author={{Cloudflare}},
  title={{CIRCL v1.6.5: Threshold RSA implementation}},
  howpublished={Software, tss/rsa package},
  url={https://github.com/cloudflare/circl/tree/v1.6.5/tss/rsa},
  note={Accessed October 3, 2026}
}
'''
write('references.bib',s)
