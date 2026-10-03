"""Apply the reviewed, bilingual terminology corrections (no protocol change)."""
from pathlib import Path

root = Path(__file__).resolve().parent.parent

def replace(file, old, new):
    path = root / file
    text = path.read_text(encoding="utf-8")
    if old not in text and new in text:
        return
    assert text.count(old) == 1, (file, old, text.count(old))
    path.write_text(text.replace(old, new), encoding="utf-8")

replace("protocol.tex", "Public verification binds each input certificate to its network, issuing organization, fact, and claimed output and rejects duplicates. It accepts exactly one certificate for each certificate input and rejects any further certificate.", "Public verification binds each input certificate to its network, issuer, fact, and output index. It requires exactly one certificate--index entry per certified input, rejects duplicate OutputIDs and extraneous entries, and permits distinct outputs of one parent certificate. Coverage is registered once per parent fact.")
replace("zh-protocol.tex", "公开验证将每份输入证书绑定到其网络、发行组织、事实和被声明的输出，并拒绝重复。它对每个证书输入恰好接受一份证书，并拒绝任何多余的证书。", "公开验证将每份输入证书绑定到其网络、发行方、事实和输出索引。每个认证输入必须恰有一个证书与索引条目；重复的 OutputID 和多余条目被拒绝，同一父证书的不同输出可以分别使用。覆盖按父事实登记一次。")
replace("security.tex", "locked an ordinary or user-funded fee input instance", "locked a principal (final or certified) or user-funded fee input instance")
replace("security.tex", "atomically lock ordinary", "atomically lock principal")
replace("zh-security.tex", "锁定了普通输入或用户付费输入实例", "锁定了本金输入（最终或认证输入）或用户付费输入实例")
replace("zh-security.tex", "锁定普通输入和用户付费输入实例", "锁定本金输入和用户付费输入实例")
replace("protocol.tex", "consumes an ordinary or user-funded fee\ninput instance", "consumes a principal or user-funded fee\ninput instance")
replace("zh-protocol.tex", "锁定的普通输入实例或用户支付的费用输入实例", "锁定的本金输入实例或用户支付的费用输入实例")
replace("supplement-security.tex", "the exact ordinary or user-fee input instance", "the exact principal (final or certified) or user-fee input instance")
replace("zh-supplement-security.tex", "精确的普通输入或\n用户费用输入实例", "精确的本金输入（最终或认证输入）或\n用户费用输入实例")
for name in ("security.tex", "zh-security.tex"):
    path = root / name
    text = path.read_text(encoding="utf-8")
    text = text.replace("$D_x$", r"$\mathsf{Gap}_x$")
    text = text.replace("\nD_x&", "\n\\mathsf{Gap}_x&")
    text = text.replace(r"a\le D_x\le A_x", r"a\le\mathsf{Gap}_x\le A_x")
    path.write_text(text, encoding="utf-8")
