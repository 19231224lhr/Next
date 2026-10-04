"""Apply the reviewed, C3-only manuscript revision; retain the previous text."""
from pathlib import Path
import re
import shutil

out = Path(__file__).resolve().parent
root = out.parent
changed = []

def edit(name, fn):
    path = root / name
    backup = out / "before" / name
    old = (backup if backup.exists() else path).read_text(encoding="utf-8")
    new = fn(old)
    if new == old:
        raise RuntimeError(f"No edit: {name}")
    backup.parent.mkdir(parents=True, exist_ok=True)
    if not backup.exists():
        shutil.copy2(path, backup)
    path.write_text(new, encoding="utf-8", newline="\n")
    changed.append(name)

def one(s, old, new):
    assert s.count(old) == 1, old[:90]
    return s.replace(old, new)

c3 = r"""\noindent\textbf{C3: Decision-authorized funding-reference repair.} A committed compensation decision closes an obligation economically; C3 reflects that decision in stored history. Restricted chameleon-hash commands write its reserve-debit reference into the designated input. Exact revisions and tasks authorize the replacement while owner authorizations, output and successor references, and the complete BlockID remain unchanged. Original commands are retained for replay, and representation writes no accounting state. This supports local exception review and re-export of a revised block under its existing identity, without making economic closure wait for historical repair."""
motivation = r"""Economic closure leaves a separate record question: a compensated input still cites its missing output in the original block. Ledger auditing already uses saved execution evidence to revisit historical records~\cite{ia-ccf}; Reparo also returns repaired transaction data through the old transaction hash~\cite{reparo}. We develop this workflow for compensation: an archival replica can export a revised funding reference at the original payment coordinates. An index supplies the same economic answer. The additional task is to authorize a replacement body while retaining the complete identity referenced by existing commits and successor headers~\cite{comet-blockid}, without executing the payment again."""

def intro(s):
    start = s.index(r"\noindent\textbf{C3:")
    end = s.index("\n\nOn one M4", start)
    s = s[:start] + c3 + s[end:]
    marker = "The design has three contributions, centered on direct responsibility for missing sources."
    return one(s, marker, "\n\n" + motivation + "\n\nThe design has three complementary contributions.")
edit("introduction.tex", intro)

c3zh = r"""\noindent\textbf{C3：由赔付决定授权的资金引用修复。}已提交的赔付决定在经济上关闭义务，C3 则将该决定落实到存储的历史中。受限变色龙哈希命令将其备付扣款引用写入指定输入。精确的修订与任务授权这一替换，同时保持所有者授权、输出及后继引用和完整 BlockID 不变。原始命令保留用于重放，表示操作不写入核算状态。这支持在既有区块身份下进行本地异常复核与修订区块再导出，而不让经济闭合等待历史修复。"""
motivationzh = r"""经济闭合之后，还有一个独立的记录问题：已赔付输入在原始区块中仍引用缺失输出。账本审计已有利用保存的执行证据回访历史记录的工作流~\cite{ia-ccf}；Reparo 也通过原交易哈希返回修复后的交易数据~\cite{reparo}。我们将这一工作流用于赔付：归档副本可在原付款坐标处导出修订后的资金引用。索引能够提供相同的经济答案。新增的技术任务是授权替换正文，同时保持既有提交证据和后继区块头所引用的完整身份~\cite{comet-blockid}，且不重新执行该支付。"""
def introzh(s):
    start = s.index(r"\noindent\textbf{C3：")
    end = s.index("\n\n在一台 M4", start)
    s = s[:start] + c3zh + s[end:]
    return one(s, "该设计包含三项贡献，核心是对缺失来源的直接责任。", "\n\n" + motivationzh + "\n\n该设计包含三项相互补充的贡献。")
edit("zh-introduction.tex", introzh)

consumer = r"""\noindent\textbf{Use: exception review and historical re-export.}
A local archival service revisits the input of an executed payment
to report its historical reserve advance, authorizing decision, and
repayment status. For example, Bob's payment to Carol consumes Alice's
missing output at $(h,j,k)$. A subsequent decision debits Alice's issuer.
The original input accurately records the object accepted at execution;
the revised funding slot records the later advance.

The service has verified and executed prefix $H$. From the same $V_H$,
it obtains the authorized body, exact Revision and Task, permanent
decision, and current obligation state. It reconstructs the block and
parts with the authorized openings, checks the complete BlockID against
the saved original commit, and exports the original and revised bodies
with their decision and revision context. The commit checks compatibility
with the existing block identity; executed commands and the exact Task
authorize the replacement bytes. Because votes cover both the header
hash and part-set header, preserving the header alone is insufficient.
If Alice's source later repays the issuer, the debit reference remains
a record of the historical advance while the obligation becomes Recovered.

C3 therefore has a correctness target distinct from the amount settled
by C2: even a correct reserve debit must not authorize a body that
attributes the input to another issuer's debit. The repair must preserve
both the permitted funding reference and the fixed payment and block
identities; decision-derived checks reject such a candidate.
Our prototype implements this Next-aware local workflow
(Table~\ref{tab:c3-function}); it requires the executed prefix, rather
than treating an exported body as a standalone remote proof.

An index or ordinary materialized view answers the same economic
questions while keeping the original body. A separately authenticated
revised copy could also retain an old-ID lookup, as illustrated by
Reparo~\cite{reparo}. C3 instead makes the authorized revised body
itself satisfy the original complete BlockID. This is its record-level
service, not an extra condition for payment finality.
Economic closure proceeds with the representation service stopped;
the current build still uses CH transaction and part encodings.

"""
def proto(s):
    a = s.index(r"\noindent\textbf{Use: block-record archival review.}")
    b = s.index(r"\noindent\textbf{Cryptographic instantiation.}", a)
    return s[:a] + consumer + s[b:]
edit("protocol.tex", proto)
consumerzh = r"""\noindent\textbf{用途：异常复核与历史记录再导出。}
本地归档服务回访已执行支付的输入，报告其历史备付垫付、授权决定和偿还状态。例如，Bob 向 Carol 的支付在 $(h,j,k)$ 处消费 Alice 的缺失输出，随后的决定扣减 Alice 所属发行方的备付。原始输入准确记录执行时接受的对象；修订后的资金槽记录后来发生的垫付。

该服务已验证并执行前缀 $H$。它在同一 $V_H$ 中获取授权正文、精确的 Revision 与 Task、永久决定及当前义务状态；使用授权开口重建区块和 part，对照保存的原始提交证据检查完整 BlockID，并连同决定和修订上下文导出原始与修订正文。提交证据检查与既有区块身份的相容性；已执行命令与精确 Task 授权替换字节。投票同时覆盖头部哈希与 part-set header，因此只保持头部哈希并不足够。如果 Alice 的来源随后偿还发行方，扣款引用仍记录历史垫付，而义务状态变为 Recovered。

因此，C3 的正确性目标不同于 C2 所结算的金额：即使备付扣款正确，也不得授权一个将该输入归因于另一发行方扣款的正文。修复必须同时保持被允许的资金引用，以及固定的付款与区块身份；依据决定进行的检查拒绝此类候选。原型实现了这一使用 Next 接口的本地工作流（表~\ref{tab:c3-function}）；它依赖已执行前缀，而不将导出的正文视为独立的远程证明。

索引或普通物化视图保持原始正文，同样能回答经济问题。经过另行认证的修订副本也可保留旧 ID 查询，Reparo 展示了这种访问模式~\cite{reparo}。C3 则使授权修订正文自身仍满足原完整 BlockID。这是其记录层服务，而非支付最终性的额外条件。表示服务停止时，经济闭合仍然推进；当前构建依然使用 CH 交易与 part 编码。

"""
def protozh(s):
    a = s.index(r"\noindent\textbf{用途：以区块记录为单位的归档复核。}")
    b = s.index(r"\noindent\textbf{密码学实例化。}", a)
    return s[:a] + consumerzh + s[b:]
edit("zh-protocol.tex", protozh)

edit("related.tex", lambda s: one(s,
    "Next fixes payment\nauthorizations and outputs in advance:",
    "Its Ethereum implementation also maps an old transaction hash to\nrepaired transaction data, supporting access through an existing record\nidentifier. IA-CCF audits immutable ledger records against saved execution\nreceipts~\\cite{ia-ccf}. These systems motivate review through established\nrecord references; Next specializes the revision authority to a committed\ncompensation decision. Next fixes payment\nauthorizations and outputs in advance:"))
edit("zh-related.tex", lambda s: one(s,
    "Next 预先固定支付授权与输出：",
    r"其 Ethereum 实现还将旧交易哈希映射到修复后的交易数据，支持通过既有记录标识访问。IA-CCF 依据保存的执行收据审计不可变账本记录~\cite{ia-ccf}。这些系统为通过既有记录引用进行复核提供了依据；Next 将修订权限具体约束到已提交的赔付决定。Next 预先固定支付授权与输出："))

evalintro = r"""We evaluate the exception-review workflow in
Section~\ref{sec:protocol-reader}: whether revised records preserve economic
answers and the original complete block identity, and what local reading,
storage, and maintenance they cost. The paused-adaptation runs above
separately test that economic closure proceeds before representation.

"""
edit("evaluation.tex", lambda s: one(one(one(s,
    "\\label{sec:eval-reader}\n\n",
    "\\label{sec:eval-reader}\n\n" + evalintro),
    "All use the same executed prefix and authorization records.}",
    "All use the same executed prefix and authorization records.\n    The revised-body row is C3's representation contract.}"),
    "commit-signature construction. Common compensation costs are reported separately.",
    "commit-signature construction. Common compensation costs are reported separately.\nThese figures characterize incremental repair on a CH-enabled build;\nthey do not isolate the steady-state cost of CH encoding against a\nnon-CH build."))
evalintrozh = r"""我们评估第~\ref{sec:protocol-reader}~节的异常复核工作流：修订记录是否保持经济答案和原完整区块身份，以及本地读取、存储和维护需要多少成本。上文暂停适配的运行独立检验了经济闭合先于表示推进。

"""
edit("zh-evaluation.tex", lambda s: one(one(one(s,
    "\\label{sec:eval-reader}\n\n",
    "\\label{sec:eval-reader}\n\n" + evalintrozh),
    "三者使用相同已执行前缀与授权记录。}",
    "三者使用相同已执行前缀与授权记录。修订正文一行对应 C3 的表示契约。}"),
    "公共的赔付成本另行报告。",
    "公共的赔付成本另行报告。这些数值描述启用 CH 的构建上的增量修复工作，并未相对于不使用 CH 的构建隔离其常驻编码开销。"))

edit("abstract.tex", lambda s: one(s,
    "issuer bearing the CAL of publicly consumed outputs. Optional authorized\nhistory commands support local block review by embedding reserve\nreferences at original coordinates while preserving owner authorizations,\nsuccessor references, and the complete BlockID.",
    "issuer bearing the CAL of publicly consumed outputs. Decision-authorized\nfunding-reference repair then records historical reserve debits in designated\ninputs, supporting local exception review under the original block identity.\nIt preserves owner authorizations and successor references without\nre-executing payments or delaying economic closure."))
edit("zh-abstract.tex", lambda s: one(s,
    "签发方承担其已公开消费输出的 CAL 损失。可选的授权历史命令在\n原坐标嵌入储备引用，支持本地区块复核，同时保持所有者授权、\n后继引用及完整 BlockID。",
    "签发方承担其已公开消费输出的 CAL 损失。由赔付决定授权的资金引用修复\n随后将历史备付扣款记录在指定输入中，支持原区块身份下的本地异常复核。\n它保持所有者授权及后继引用，不重新执行支付，也不延迟经济闭合。"))
edit("conclusion.tex", lambda s: one(s,
    "even with adaptation paused. Optional\nrepresentation records the reserve debit under the original block identity.",
    "even with adaptation paused. Decision-authorized funding-reference repair\nthen records the historical reserve debit in the existing block record,\nwithout changing its complete identity or repeating its economic effects."))
edit("zh-conclusion.tex", lambda s: one(s,
    "可选的授权表示把备付借记记录在原区块身份之下。",
    "由赔付决定授权的资金引用修复随后将历史备付扣款写入既有区块记录，而不改变其完整身份或重复产生经济效果。"))

bib = r"""
@inproceedings{ia-ccf,
  author={Alex Shamis and Peter Pietzuch and Burcu Canakci and Miguel Castro and Cedric Fournet and Edward Ashton and Amaury Chamayou and Sylvan Clebsch and Antoine Delignat-Lavaud and Matthew Kerner and Julien Maffre and Olga Vrousgou and Christoph M. Wintersteiger and Manuel Costa and Mark Russinovich},
  title={{IA-CCF: Individual Accountability for Permissioned Ledgers}},
  booktitle={19th USENIX Symposium on Networked Systems Design and Implementation (NSDI 22)},
  year={2022}, pages={467--491}, publisher={USENIX Association},
  url={https://www.usenix.org/conference/nsdi22/presentation/shamis}
}
@misc{comet-blockid,
  author={{CometBFT Contributors}},
  title={{CometBFT Core Data Structures: BlockID and Commit}},
  howpublished={Protocol specification, version 0.38},
  url={https://docs.cometbft.com/v0.38/spec/core/data_structures},
  note={Accessed October 4, 2026}
}
"""
edit("references.bib", lambda s: s + bib)
print("\n".join(changed))
