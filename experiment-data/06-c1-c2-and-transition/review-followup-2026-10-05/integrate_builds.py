"""One-shot integration of measured build boundaries and current chain headline."""
from pathlib import Path
p = Path(__file__).resolve().parents[2] / 'paper/review-2026-10-02'
for prefix in ('', 'zh-'):
    path = p / (prefix + 'evaluation.tex')
    s = path.read_text(encoding='utf-8')
    if not prefix:
        s = s.replace('Current continuation, P, and R & \\texttt{f856c7b} & Six chain totals; three P runs; one R run',
          'Continuation and finite CAL & \\texttt{8937df90}$^*$ & Nine chains; repeated capital--delay cells \\\\\nRepresentative P and R & \\texttt{f856c7b} & Three P runs; one R run')
        a = s.index('All ten current network runs'); b = s.index('\\noindent\\textbf{Setup and measurement.}', a)
        s = s[:a] + ('The continuation and finite-capital trials use one frozen source bundle '
          '($^*$SHA-256 prefix in Table~\\ref{tab:eval-builds}); their manifests record actual binaries and configurations. '
          'The representative P/R trials retain their measured \\texttt{f856c7b} build. '
          'Relative to that build, this bundle adds the offline receipt verifier and client instrumentation; '
          'the existing node production files and materialized Comet production sources are unchanged. '
          'Supplement~S4 records the file-level comparison, original build fingerprints, and archived chain results. '
          'Historical timings retain their measured versions.\n\n') + s[b:]
        s = s.replace('implements the identity and installation rules. Network trials use',
                      'implements the identity and installation rules. Except for the finite-capital experiment specified below, network trials use')
        s = s.replace('and use sufficient fixed funding without top-up.',
                      'and use fixed funding without top-up; the finite-capital experiment varies the organization budget.')
        s = s.replace('The median M/F ratio is 13.98 when computed from the two mode medians;',
                      'The ratio of the M and F mode medians is 13.98;')
    else:
        s = s.replace('当前连续链、P 与 R & \\texttt{f856c7b} & 六轮链总时长；三轮 P；一轮 R',
          '连续链与有限 CAL & \\texttt{8937df90}$^*$ & 九轮链；重复资本--延迟组合 \\\\\n代表性 P 与 R & \\texttt{f856c7b} & 三轮 P；一轮 R')
        a=s.index('十轮当前网络实验');b=s.index('\\noindent\\textbf{设置与测量。}',a)
        s=s[:a]+('连续链与有限资本试验使用同一冻结源码包（表~\\ref{tab:eval-builds} 中 $^*$ 标注其 SHA-256 前缀），清单记录实际二进制与配置。'
          '代表性 P/R 保留实测的 \\texttt{f856c7b} 构建。相对于该构建，新包增加离线收据验证器与客户端埋点；'
          '已有节点生产文件及实际 Comet 生产源码未变。补充材料~S4 记录逐文件比对、原构建指纹及归档连续链数据；历史计时保留实测版本。\n\n')+s[b:]
        s=s.replace('网络试验在签名之前使用同步的 bbolt 成员提交；',
                    '除下文单独说明的有限资本实验外，网络试验在签名之前使用同步的 bbolt 成员提交；')
        s=s.replace('并使用充足的固定资金而不充值。', '并使用固定资金而不充值；有限资本实验改变组织预算。')
    path.write_text(s,encoding='utf-8')
for prefix in ('','zh-'):
    for stem in ('abstract','introduction','conclusion'):
        path=p/(prefix+stem+'.tex');s=path.read_text(encoding='utf-8')
        if not prefix:
            if stem=='abstract':
                s=s.replace('versus 64.307\\,s with per-hop waiting.', 'versus 64.307\\,s when each successor waits for wallet-verified public execution and three members\' observation of its parent.')
            elif stem=='introduction':
                s=s.replace('versus 64.307\\,s when waiting for public execution between\nhops. On the same current build, three fourteen-service P runs complete',
                    "versus 64.307\\,s when each successor waits for wallet-verified public execution and three members' observation of its parent. The member-ready control eliminates failed retries without closing this gap. Three fourteen-service P runs on the recorded \\texttt{f856c7b} build complete")
            else:
                s=s.replace('when each hop waits for public execution\nin an end-to-end comparison including retries and evidence observation.',
                    "when each hop waits for wallet-verified public execution and three members' parent observation. Both of these modes complete without failed retries;")
                s=s.replace('; Reserve-backed', '; reserve-backed')
        else:
            if stem=='abstract':
                s=s.replace('逐跳等待模式为 64.307\\,s', '同时等待钱包验证公开执行及三名成员观察到父交易的模式为 64.307\\,s')
            else:
                # Headline endpoint details are stated fully in the adjacent evaluation.
                s=s.replace('等待公开执行', '等待公开执行与三名成员观察到父交易')
                s=s.replace('同一当前构建', '所标注的构建')
        path.write_text(s,encoding='utf-8')
