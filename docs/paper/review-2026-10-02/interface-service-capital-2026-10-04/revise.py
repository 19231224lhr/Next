"""Apply the reviewed, narrowly scoped evidence and service-contract revision."""
from pathlib import Path

root = Path(__file__).resolve().parent.parent

def replace(name, old, new):
    path = root / name
    text = path.read_text(encoding="utf-8")
    if new in text:
        return
    assert text.count(old) == 1, (name, old[:80])
    path.write_text(text.replace(old, new), encoding="utf-8", newline="\n")

replace("model.tex", "Table~\\ref{tab:model-contract} separates certified receipt, public\nconsumption, and liability closure.",
        "Receipt, consumption, and closure establish different guarantees\n(Table~\\ref{tab:model-contract}). Receipt authenticates a right to request\ncontinuation; successful public consumption establishes the funded obligation\nthat protects the executed successor. Closure settles the issuer's liability\nand the consumer's fee escrow without creating another principal credit.")
replace("zh-model.tex", "表~\\ref{tab:model-contract} 区分认证到账、公开消费和责任闭合。",
        "到账、消费与闭合分别建立不同的保证（表~\\ref{tab:model-contract}）。到账认证的是请求续付的权利；成功的公开消费建立有资金支持的义务，保护已执行后继的本金效果。闭合结清发行方责任与消费方费用托管，不再产生一次本金入账。")

old = "FUEL follows separate accounting: each escrow maximum equals Held plus cumulative Rewards, Burned, and Refunded, and the global conserved sum counts unspent FUEL, ordinary accounts, current Held, reward balances, and cumulative burn. Unique stage and obligation identities transfer value once, for user-funded and organization-funded fees alike."
new = r"""FUEL follows separate accounting. For escrow maximum $M$, the invariant is
\[
 \mathsf{Held}+\mathsf{Rewards}+\mathsf{Burned}+\mathsf{Refunded}=M.
\]
For $k$ certified inputs, admission requires
\[
 M\ge f_{\mathrm{register}}+f_{\mathrm{settle}}+f_{\mathrm{close}}
       +k f_{\mathrm{repair}}.
\]
Stage identities charge each base fee once, and an obligation leaves Open
once, so at most $k$ repair charges occur. Closure therefore has enough Held
for its fee and refunds the remainder. FUEL and Policy caps equal $M$;
replacing the reserved cap by actual Rewards plus Burned cannot increase
their total usage. Fee-budget exhaustion thus cannot block closure of a
validly admitted payment under these rules. The global conserved sum counts
unspent FUEL, ordinary accounts, Held, reward balances, and cumulative burn,
for both fee modes. Supplement~S1 maps these guards to public-entry,
multi-obligation, and member-accounting regressions."""
replace("security.tex", old, new)
replace("zh-security.tex", "FUEL 单独记账：每笔托管上限等于 Held 加上累计 Rewards、Burned 和 Refunded；全局守恒总量计入未消费 FUEL、普通账户、当前 Held、奖励余额和累计销毁额。唯一的阶段身份和义务身份使价值仅转移一次，对用户付费和组织付费均如此。",r"""FUEL 单独记账。对于托管上限 $M$，不变式为
\[
 \mathsf{Held}+\mathsf{Rewards}+\mathsf{Burned}+\mathsf{Refunded}=M.
\]
若包含 $k$ 个认证输入，准入要求
\[
 M\ge f_{\mathrm{register}}+f_{\mathrm{settle}}+f_{\mathrm{close}}
       +k f_{\mathrm{repair}}.
\]
阶段身份使每项基础费用只扣一次，每项义务只离开 Open 一次，因而至多发生 $k$ 次赔付费用。关闭时 Held 足以支付关闭费用，其余部分退回。FUEL 与 Policy 的上限均为 $M$；用实际 Rewards 加 Burned 替换已预留上限不会增加其总用量。因此，在这些规则下，合法准入支付的关闭不会因费用预算耗尽而受阻。全局守恒总量计入未消费 FUEL、普通账户、Held、奖励余额和累计销毁额，适用于两种费用模式。补充材料~S1 将这些守卫对应到公共入口、多义务和成员记账回归。""")

replace("security.tex", "and the round and step guards allow one precommit per round.",
        "and the round and step guards allow one precommit per round. A current-round polka may drive precommit for its exact available candidate even without a matching proposal; this transition retains the round and step guards, including when it precedes the local prevote.")
replace("zh-security.tex", "轮次与步骤守卫则确保每轮只发出一次预提交。",
        "轮次与步骤守卫则确保每轮只发出一次预提交。即使没有匹配的提案，当前轮的 polka 也可使其精确且已可得的候选进入 precommit；该转移仍受轮次与步骤守卫约束，包括发生在本地 prevote 之前的情形。")

replace("related.tex", "Next keeps the user's payment principal\nseparate from additional organizational backing, and its compensation closes\nthe missing source of an already executed child without a second principal\ncredit to that child's recipient.",
        "The service trigger differs: FastPay backs primary-ledger redemption\nwith prepaid funds, and Snappy gives an accepting merchant a collateral claim\nunder its approval rules. In Next, signing reserves issuer backing, but the\npublic obligation arises when an executed successor consumes a missing\ncertified output. Compensation closes that source obligation without a second\nprincipal credit to the child recipient. A terminal holder whose source never\nexecutes and who has no executing successor has no certificate-only redemption\npath (Section~\\ref{sec:model-contract}).")
replace("zh-related.tex", "Next 将用户的支付本金与额外的组织支持分开，其赔付关闭的是已执行子交易所缺失的来源，而不向该子交易的接收方再次计入本金。",
        "服务触发点有所不同：FastPay 以预存资金支持主账本赎回，Snappy 按其批准规则为接受付款的商户提供抵押索赔。在 Next 中，签名预留发行方支持额度，但公共义务在已执行后继消费缺失的认证输出时才产生。赔付关闭该来源义务，不向子交易接收方重复计入本金。若终端持证人的来源始终不执行，且没有已执行后继，则不存在仅凭证书的兑付路径（第~\\ref{sec:model-contract}~节）。")

en = r"""
\noindent\textbf{Capital and admission.}
Each P run uses a fixed CAL grant of $10^{12}$ per organization and no
automatic top-up. The final resource records of all eight members in each run
report zero resource-insufficiency counters and zero remaining CAL and work
reservations; spent FUEL remains charged. These runs establish closure with
fixed, sufficient funding. The coverage theorem concerns all execution
prefixes, including unpublished certificates, whereas these records measure
the finite workload and its endpoint. The 256-outstanding driver limit is
not a bound on all competing or abandoned approvals. The boundary ledgers
below identify when retained capacity stops certification, and
Section~\ref{sec:security-composition} states the separate conditions for
continued admission.
"""
zh = r"""
\noindent\textbf{资本与准入。}
每轮 P 的各组织使用固定 $10^{12}$ CAL grant，不启用自动补资。每轮八名成员的最终资源记录均报告资源不足计数为零，CAL 与工作资源的剩余预留为零；已支出的 FUEL 仍计入费用。这些运行确认了固定且充足资金下的闭合。覆盖定理涉及包括未公开证书在内的所有执行前缀，而这些记录测量有限负载及其终点。驱动的 256 笔未完成上限并不是所有竞争或被放弃批准的上限。下文的边界账本展示滞留额度何时停止新认证，第~\ref{sec:security-composition}~节另行给出持续准入的条件。
"""
replace("evaluation.tex", "659,544~FUEL: 589,384 in rewards and 70,160 burned.\n", "659,544~FUEL: 589,384 in rewards and 70,160 burned.\n"+en)
replace("zh-evaluation.tex", "589,384 为奖励，70,160 被销毁。\n", "589,384 为奖励，70,160 被销毁。\n"+zh)
print("Revised model, service comparison, fee argument and capital interpretation in both languages.")
