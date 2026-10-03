from pathlib import Path
import re
p=Path('docs/paper/review-2026-10-02');d=p/'fourth-revision-2026-10-04'
s=(d/'gpt-proof-table.tex').read_text(encoding='utf-8')
sec=(p/'security.tex').read_text(encoding='utf-8');names={label:title for _,title,label in re.findall(r'\\begin\{(lemma|theorem)\}\[([^]]+)\]\s*\\label\{([^}]+)\}',sec)}
s=re.sub(r'(?:Lemma|Theorem)~\\ref\{([^}]+)\}',lambda m:names[m[1]],s)
# Plural combined entries have a bare second ref.
s=re.sub(r'Lemmas~\\ref\{([^}]+)\}',lambda m:names[m[1]],s)
s=re.sub(r'Theorems~\\ref\{([^}]+)\}',lambda m:names[m[1]],s)
s=re.sub(r'\\ref\{([^}]+)\}',lambda m:names[m[1]],s)
rows=s[s.index('Original-execution agreement'):s.index('\\bottomrule')].strip()
rows=rows.replace('in \\texttt{EvaluateDirectPaymentAt}. &',r'''in \texttt{EvaluateDirectPaymentAt}. The fixed wire4 cases in
\texttt{Check}/\texttt{ExecuteAt} are \texttt{ReserveIncrease},
\texttt{ClockTick}, \texttt{RepairInput}, \texttt{RepairBatch},
\texttt{CompensationDecision}, and \texttt{DirectSubmission};
\texttt{Execute} rejects legacy in direct mode. &''')
rows=rows.replace('distinct-voter, identical-byte-cache,',r'\texttt{TestDirectModeRejects\allowbreak AlternatePaymentEnvelopes}; distinct-voter, identical-byte-cache,')
rows=rows.replace('without erasing existing debits. &',r'''without erasing existing debits. \texttt{Engine.NewEngine} aggregates
CAL/FUEL grants by \texttt{AccountKey} and checks their total against
its balance. Protected CAL includes all organization and CAL-grant accounts. &''')
rows=rows.replace('creates no second\nuser output. &',r'''creates no second user output. Only an undecided target reaches
\texttt{targetSlot}, which requires \texttt{OriginalFunding} matching
its original \texttt{OutputID}, amount, and fixed consumer fields. &''')
rows=rows.replace('initial-state and successful source-closed payment-set conditions;', 'initial-state, conflict-free source-closed payment-set, and successful payment/top-up conditions;')
rows=rows.replace('representation-induced principal effect. &',r'''representation-induced principal effect. Both \texttt{RepairInput} and
\texttt{RepairBatch} use decided-target guards.
\texttt{EvaluateDirectPaymentAt} does not read historical \texttt{Canonical}.
Existing decisions return before historical access; other revised slots
leave an undecided target and its fixed fields unchanged. &''')
rows=rows.replace('business updates and follower progress commit together. &',r'''business updates and follower progress commit together.
\texttt{wallet.ReceiveDirect} checks registered configuration, network,
owner, and \texttt{c.VerifyOutput}, and atomically records
\texttt{DirectCoin}, rejecting inconsistent bodies for one OutputID.
\texttt{member.InstallDirectClassified} verifies the full payment,
rejects Invalidated in its transaction, treats Observed as a no-op,
and records all input/fee consumption, install material, and outbox
without another reservation. Receipt checks alone do not establish FreshRecv. &''')
cn=(d/'gpt-zh-proof-table.tex').read_text(encoding='utf-8');head=cn[:cn.index('Original-execution agreement')]
head=re.sub(r'\\caption\{.*?\}\n\\label', r'\\caption{Selected proof--implementation correspondences for production baseline \\texttt{f856c7b}. The artifact index retains complete paths and test assertions.}'+ '\n'+r'\\label',head,flags=re.S)
head=head.replace('命题（正文英文名称）','Claims (main-text names)').replace('生产入口与证明相关职责','Production entry and proof obligation').replace('代表性回归证据','Representative regression evidence').replace('表~\\thetable\\ （续）','Table~\\thetable\\ (continued)').replace('续下页','Continued on next page')
tail=s[s.index('\\noindent\\textbf{Premises'):]
a=tail.index('Fixed four-member');b=tail.index('Signature unforgeability',a)
tail=tail[:a]+r'''Fixed four-member organizations and a four-validator committee,
three-vote quorums, at most one static Byzantine participant per group,
consistent configuration, and non-rollback of honest signing state remain
model or deployment premises. Aggregate genesis backing and protected-CAL
restrictions map to \texttt{Engine.NewEngine}, the fixed dispatcher,
and the account guards above.
'''+tail[b:]
tail=tail.replace('each case is\nattributed to the guard actually reached, and a codec rejection\nis not reported as evidence that signature verification executed.','a codec rejection does not establish that signature verification executed.')
en=head+rows+'\n\\end{longtable}\n\\endgroup\n\n'+tail
# Long identifier breaks only, preserving meaning.
for label,text in [('supplement-final-evidence.tex',en),('zh-supplement-final-evidence.tex',cn)]:
 text=text.replace('\\texttt{TestPublicAuthorizationAtABCIEntry}',r'\texttt{TestPublicAuthorization\allowbreak AtABCIEntry}').replace('\\texttt{member.InstallDirectClassified}',r'\texttt{member.\allowbreak InstallDirectClassified}')
 f=p/label; f.write_text(f.read_text(encoding='utf-8')+text,encoding='utf-8')
