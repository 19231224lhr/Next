"""Apply the reviewed GPT drafting blocks without changing protocol prose."""
from pathlib import Path

d = Path(__file__).resolve().parent
p = d.parent
old = (p / 'evaluation.tex').read_text(encoding='utf-8')
draft = (d / 'gpt-evaluation.tex').read_text(encoding='utf-8')
opening, rest = draft.split('% Replace the continuous-spending subsection text.')
chain, mixed = rest.split('% Replace the mixed-load subsection text.')
opening = opening.split('\\noindent')[1]
opening = '\\noindent' + opening
setup = old[old.index('\\noindent\\textbf{Setup and measurement.}'):old.index('\\subsection{Continuous')]
chain = chain.replace('\\subsection{Continuous Spending Without Per-Hop Settlement}', '\\subsection{Continuous Spending Without Per-Hop Settlement}\n\\label{sec:eval-continuation}')
chain = chain.replace('Three independent NoSync wallets', 'Nine services support three independent NoSync wallets')
chain = chain.replace('Fast mode proceeds', 'The three pairs run in fast/wait, wait/fast, fast/wait order after a uniform three-second warm-up. Fast mode proceeds')
chain = chain.replace('including successor construction and retries.', 'including construction of hops 2--100 and retries, but excluding first-payment construction.')
chain = chain.replace('Each chain spends 9,400~FUEL.', 'Last-payment public observation has a median of 5.477\\,s. Each chain spends 9,400~FUEL: 8,400 in rewards and 1,000 burned.')
chain = chain.replace('not pure query overhead.', 'not pure query overhead. The RPC can return the seen commit at the latest height, so this interface does not require an additional wait for $h+2$.')
chain = chain.replace('The verification checkpoint is', 'Each quoted stage statistic is the median of three run-level quantiles over 100 payments; shared-height observations are expanded per payment. The verification checkpoint is')
chain += r'''
\begin{figure}[t]
  \centering
  \includegraphics[width=\columnwidth]{continuation-final.pdf}
  \caption{Current-build 100-hop chains on \texttt{f856c7b}, including
    client timing probes: three runs per mode, synchronous members and
    NoSync wallets. Dots are run totals; bars mark medians. Only wait
    mode requires public observation between hops.}
  \label{fig:continuation}
\end{figure}
'''
mixed = mixed.replace('\\subsection{Source Resolution Under Representative Mixed Load}', '\\subsection{Source Resolution Under Representative Mixed Load}\n\\label{sec:eval-mixed}\n\\label{sec:eval-liabilities}\n\\label{sec:eval-repair}')
mixed = mixed.replace('Parent publication', 'Four committee nodes and two organizations with four members and one gateway each give fourteen services. Ordinary wallets use NoSync; scenario wallets commit synchronously. The driver limits fast requests to 64 and outstanding payments to 256. Pairs start ten seconds after driver launch, one second apart, and scenario wallets follow blocks for at least 55\\,s. Parent publication')
mixed = mixed.replace('it is not a current-build comparison of interference across N/R/C/P.', 'the archived N/R/C/P comparison retains its original build and is reported separately in the supplement.')
table = (d / 'gpt-p-table.tex').read_text(encoding='utf-8').replace('\\label{tab:current-p}', '\\label{tab:current-p}\n\\label{tab:eval-mixed}')
mixed += '\n' + table + r'''
\begin{figure*}[t]
  \centering
  \includegraphics[width=\textwidth]{mixed-load.pdf}
  \caption{Current-build P observations. Left: ordinary-receipt P95
    in the first seven ten-second windows; line and band show three-run
    medians and minima--maxima. Right: eight targets in P-1, with
    compensation and repayment preceding adaptation resumption.
    Public events are observations, not internal commit timestamps.}
  \label{fig:mixed-load}
  \label{fig:c3-resolution}
\end{figure*}
'''
tail = old[old.index('\\subsection{Historical Reading:'):]
tail = tail.replace('The reader is an honest local replica', 'The timed historical-reader series retains build \\texttt{721800c}; the same-consumer functional extension uses the current build. The reader is an honest local replica')
tail = tail.replace('\\subsection{Current-Build Boundary and Composition Checks}', '\\subsection{Boundary and Composition Checks}')
tail = tail.replace('The supplement gives the per-prefix\nledgers, negative-entry tests, and restart assertions.', 'The supplement gives the per-prefix\nledgers, negative-entry tests, and restart assertions. The current-build\nproof-to-code map identifies the entry points, atomic guards, and existing\nregressions supporting each property. An additional raw-byte ABCI test\nrejects unrelated or forged QCs and forged owner signatures at CheckTx,\nProcessProposal, and FinalizeBlock without business-state changes.')
(p / 'evaluation.tex').write_text('\\section{Implementation and Evaluation}\n\\label{sec:evaluation}\n\n'+opening.strip()+'\n\n'+setup+chain+mixed+tail)

for name in ['abstract.tex', 'introduction.tex', 'conclusion.tex', 'zh-abstract.tex', 'zh-introduction.tex', 'zh-conclusion.tex']:
    text = (p/name).read_text(encoding='utf-8').replace('4.557', '4.707').replace('64.476', '64.479')
    if name == 'introduction.tex':
        a = text.index('A fourteen-service mixed workload')
        b = text.index('In a twelve-run study', a)
        text = text[:a] + 'On the same current build, a fourteen-service mixed workload completes 21,048 payments while compensation and repayment precede the resumption of paused adaptation services. ' + text[b:]
    (p/name).write_text(text)

s = (p/'supplement-current.tex').read_text(encoding='utf-8').replace('Per-Run Evidence for the Frozen Experimental Build', 'Archived Per-Run Evidence: Build 721800c').replace('used by the main evaluation', 'retained as historical evidence; current-build continuation and representative P results are reported separately').replace('Final-build continuation;', 'Archived build-721800c continuation;')
(p/'supplement-current.tex').write_text(s)
s = (p/'supplement-revision.tex').read_text(encoding='utf-8').replace('Current-Build Revision Validation', 'Boundary and Network Validation: Build 9879f64')
(p/'supplement-revision.tex').write_text(s)
print('Applied English evaluation and shared headline numbers.')
