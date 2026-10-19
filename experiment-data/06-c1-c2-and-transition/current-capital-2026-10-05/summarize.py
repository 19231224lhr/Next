#!/usr/bin/env python3
"""Final-only offline audit: complete 2×2×3 capital matrix and raw evidence."""
import argparse
import csv
import gzip
import hashlib
import json
from pathlib import Path
from statistics import median


def read(path):
    if path.exists():
        return json.loads(path.read_text(encoding='utf-8-sig'))
    with gzip.open(str(path) + '.gz', 'rt', encoding='utf-8-sig') as handle:
        return json.load(handle)


def sample_lines(path):
    if path.exists():
        return path.open(encoding='utf-8-sig')
    return gzip.open(str(path) + '.gz', 'rt', encoding='utf-8-sig')


def stats(values):
    return {'min': min(values), 'median': median(values), 'max': max(values)}


def csv_write(path, rows):
    if not rows:
        return
    columns = list(dict.fromkeys(key for row in rows for key in row))
    with path.open('w', encoding='utf-8', newline='') as handle:
        writer = csv.DictWriter(handle, fieldnames=columns)
        writer.writeheader()
        writer.writerows(rows)


def resource(snapshot, kind):
    values = [r for r in snapshot['Resources'] if r['Key']['Kind'] == kind]
    assert len(values) == 1, ('ambiguous resource', kind)
    return values[0]


def audit_case(folder, summary):
    config = read(folder / 'configuration.json')
    report = read(folder / 'reports/budget-v4.json')
    budget = read(folder / 'reports/budget-audit.json')
    ledger = read(folder / 'reports/audit.json')
    snapshots = read(folder / 'last-snapshots.json')
    environment = read(folder / 'environment.json')
    network = read(folder / 'network.json')
    assert report['OwnerFuel'] and config['gateway_parent'] and not config['serial_signing']
    assert environment['UTXO_EXPERIMENT_DISABLE_SOURCE_RECOVERY'] == '1'
    assert environment['UTXO_EXPERIMENT_SERIAL_DIRECT'] == '0'
    assert config['workers_per_member'] == 1 and config['pending_units'] == 128
    assert config['grant'] == summary['grant'] and config['delay_s'] == summary['delay_s']
    assert all(g['Key']['Kind'] not in [2, 5] for g in network['Genesis']['Grants'])
    assert all(a['Asset'] != 2 for a in network['Accounts'])
    committees = [r for r in ledger if r['Name'].startswith('committee')]
    assert len(committees) == 4 and len({r['StateHash'] for r in committees}) == 1
    assert all(int(r['Gap']) == 0 for r in committees), 'nonzero final public gap'
    payments = budget['Payments']
    public = [p for p in payments if p['Public']]
    partial = [p for p in payments if not p['Public'] and not p['Ready'] and p['Signers'] in [1, 2]]
    hidden_qc = [p for p in payments if not p['Public'] and p['Signers'] >= 3]
    assert len(partial) == budget['PartialApprovals'] == summary['partial_approval_payments']
    assert len(public) == summary['public_payments']
    assert all(p['FeeSource'] == 2 and p['FeeInputAmount'] == 10000 for p in public)
    assert all(p['Fee']['Closed'] and p['Fee']['Held'] == 0 for p in public)
    assert all(not p['Refund'] or p['WalletRefundFinal'] for p in public)
    obligations = [p['Obligation'] for p in payments if p.get('Obligation')]
    assert all(o['Amount'] == 100 for o in obligations), 'fixture gap curve requires fixed100'
    assert len(obligations) == summary['actual_obligations']
    assert all(o['Status'] == 1 for o in obligations), 'non-Fulfilled normal source'
    fee_paid = sum(p['Fee']['Rewards'] + p['Fee']['Burned'] for p in public)
    assert fee_paid == summary['user_fee_actual'] == 94 * len(public)
    assert sum(p['FeeInputAmount'] for p in public) == (
        sum(p['Change'] + p['Refund'] for p in public) + fee_paid)
    assert snapshots['committee0']['CAL'] == summary['grant']
    assert snapshots['committee0']['FUEL'] == 0
    assert report['Admitted'] + report['NotStarted'] == report['Offered']
    assert report['Admitted'] == len(report['Units']) == summary['admitted_units']
    start, duration = report['StartedUnixNS'], report['DurationSeconds']
    window_end = start + int(duration * 1e9)
    stop = report['StoppedUnixNS']
    child_success = [u for u in report['Units'] if u['Child']['ReadyUnixNS'] > 0]
    closed = [u for u in report['Units'] if u['Parent']['MemberClosedUnixNS'] > 0
              and u['Child']['MemberClosedUnixNS'] > 0]
    assert len(closed) == summary['closed_units']
    assert all(not u['FirstSubmitUnixNS'] or u['FirstSubmitDelayNS'] >= int(config['delay_s'] * 1e9)
               for u in report['Units'])
    members = []
    local_remaining_total = 0
    for name in sorted(snapshots):
        if not name.startswith('org0-member'):
            continue
        snapshot = snapshots[name]
        cal = resource(snapshot, 1)
        available = sum(s['Available'] for s in cal['Slices'])
        reserved = sum(s['Reserved'] for s in cal['Slices'])
        quota = (2 * cal['Grant']) // 3
        assert available + reserved == quota
        debit = next(d for d in snapshot['Detail']['Debits'] if d['Key'] == cal['Key'])
        assert debit['Charged'] - debit['Released'] - debit['NetSpent'] == reserved
        assert debit['NetSpent'] == 0
        local_remaining_total += reserved
        members.append({'case': folder.name, 'member': name, 'quota_CAL': quota,
                        'final_available_CAL': available, 'final_reserved_CAL': reserved,
                        'unsettled_approval_records': snapshot['Detail']['Approvals'] - snapshot['Detail']['Settled'],
                        'CAL_refusal_calls': snapshot['Limited'][1],
                        'Execution_refusal_calls': snapshot['Limited'][3],
                        'Bytes_refusal_calls': snapshot['Limited'][4]})
        for kind in [3, 4]:
            r = resource(snapshot, kind)
            assert r['Grant'] >= 10**12
            assert snapshot['Limited'][kind] == 0, 'other resource refused'
            members[-1]['final_' + ('Execution' if kind == 3 else 'Bytes') + '_reserved'] = sum(
                s['Reserved'] for s in r['Slices'])
    assert len(members) == 4
    votes = sum(p['Signers'] for p in partial)
    residual_explained_by_partial = not hidden_qc and votes * 100 == local_remaining_total
    locked_fees = {tuple(p['FeeInput']): p for p in payments
                   if not p['Public'] and p.get('FeeLockedMembers', 0) > 0}
    row = dict(summary)
    row.update(final_public_gap_CAL=0, final_public_CAL_reserved=resource(snapshots['committee0'], 1)['Usage']['Reserved'],
               final_public_CAL_spent=resource(snapshots['committee0'], 1)['Usage']['Spent'],
               partial_one_vote=sum(p['Signers'] == 1 for p in partial),
               partial_two_votes=sum(p['Signers'] == 2 for p in partial),
               residual_partial_member_votes=votes, hidden_qc_nonpublic=len(hidden_qc),
               total_overlapping_member_CAL_residual=local_remaining_total,
               residual_fully_explained_by_partial_approvals=residual_explained_by_partial,
               all_four_member_CAL_slices_full=all(m['final_available_CAL'] == 0 for m in members),
               member_quota_CAL=(2 * summary['grant']) // 3,
               sampled_member_CAL_peak=summary['occupancy']['1']['max_member_reserved'],
               sampled_member_CAL_peak_quota_fraction=summary['occupancy']['1']['max_member_reserved'] / ((2 * summary['grant']) // 3),
               actual_child_success_count=len(child_success), child_offered_denominator=report['Offered'],
               admitted_incomplete_at_bounded_stop=report['Admitted'] - len(closed),
               closed_units_within_input_window=sum(max(u['Parent']['MemberClosedUnixNS'], u['Child']['MemberClosedUnixNS']) <= window_end for u in closed),
               child_public_observed_within_input_window=sum(0 < u['Child']['FinalUnixNS'] <= window_end for u in report['Units']),
               unique_nonpublic_fee_inputs_locked=len(locked_fees),
               nonpublic_fee_locked_FUEL=sum(p['FeeInputAmount'] for p in locked_fees.values()))
    row['all_member_work_reservations_zero'] = all(m['final_Execution_reserved'] == 0
        and m['final_Bytes_reserved'] == 0 for m in members)
    unit_rows = []
    for u in report['Units']:
        completed = u['Parent']['MemberClosedUnixNS'] > 0 and u['Child']['MemberClosedUnixNS'] > 0
        finish = max(u['Parent']['MemberClosedUnixNS'], u['Child']['MemberClosedUnixNS']) if completed else None
        unit_rows.append({'case': folder.name, 'unit': u['Index'],
                          'planned_s': (u['PlannedUnixNS'] - start) / 1e9,
                          'started_s': (u['StartedUnixNS'] - start) / 1e9,
                          'child_ready_s': (u['Child']['ReadyUnixNS'] - start) / 1e9 if u['Child']['ReadyUnixNS'] else None,
                          'closed_s': (finish - start) / 1e9 if finish else None,
                          'censored': not completed,
                          'bounded_stop_s': (stop - start) / 1e9})
    curve_rows = []
    sampled_member_cal_peak = 0
    for text in sample_lines(folder / 'samples.jsonl'):
        sample = json.loads(text)
        snapshot = sample.get('snapshot')
        if not snapshot:
            continue
        seconds = (snapshot['UnixNS'] - start) / 1e9
        detail = snapshot.get('Detail')
        for r in snapshot['Resources']:
            kind = r['Key']['Kind']
            if kind not in [1, 3, 4]:
                continue
            member = sample['node'].startswith('org0-member')
            reserved = sum(s['Reserved'] for s in r.get('Slices', [])) if member else None
            quota = (2 * r['Grant']) // 3 if member else None
            if member and kind == 1 and 0 <= seconds <= duration:
                sampled_member_cal_peak = max(sampled_member_cal_peak, reserved)
            curve_rows.append({'case': folder.name, 'node': sample['node'],
                              'snapshot_unix_ns': snapshot['UnixNS'], 'seconds': seconds,
                              'input_window': 0 <= seconds <= duration, 'resource_kind': kind,
                              'grant': r['Grant'], 'member_quota': quota,
                              'member_reserved': reserved,
                              'member_occupancy_fraction': reserved / quota if member else None,
                              'member_available': sum(s['Available'] for s in r.get('Slices', [])) if member else None,
                              'public_reserved': r['Usage']['Reserved'] if not member else None,
                              'public_spent': r['Usage']['Spent'] if not member else None,
                              'issuer_reserve_CAL': snapshot['CAL'] if not member else None,
                              'detailed_open_obligations': detail['Open'] if detail and not member else None,
                              'detailed_open_principal_CAL_fixed100': detail['Open'] * 100 if detail and not member else None})
    assert sampled_member_cal_peak == row['sampled_member_CAL_peak'], 'peak disagrees with raw samples'
    return row, members, unit_rows, curve_rows


METRICS = ['offered_units', 'admitted_units', 'closed_units', 'not_started',
           'partial_approval_payments', 'actual_obligations', 'actual_obligation_fraction',
           'public_payments', 'user_fee_actual', 'child_fast_p50_ms', 'child_fast_p95_ms',
           'sampled_member_CAL_peak', 'sampled_member_CAL_peak_quota_fraction',
           'closed_units_within_input_window', 'child_public_observed_within_input_window',
           'unique_nonpublic_fee_inputs_locked', 'admitted_incomplete_at_bounded_stop']


def format_range(value):
    return f"{value['median']:g} [{value['min']:g}, {value['max']:g}]"


def make_report(groups, runs):
    lines = ['# 当前构建有限资本实验：完整离线审计', '',
             '已纳入预算3,600/28,800 CAL × 来源公开延迟1/3秒的全部十二格，每格三轮；单单位校准不计入矩阵。每轮计划1,200个两跳单位，20单位/秒，输入窗口60秒、最多60秒排空；三个钱包各自保存真实凭证，用户以独立最终FUEL输入付费。自动来源补交在全部四成员处关闭，单入口顺序取证和动态补资关闭。该结果是受控正常来源闭合实验，不是赔付压力、崩溃恢复或生产默认来源恢复测量。', '',
             '下表数值为三轮中位数 [最小值, 最大值]；一次两跳单位只算一个业务单位。', '',
             '| CAL备付 | 延迟 | 计划 | 启动 | 完整闭环 | 未启动 | 未成证部分批准 | 真实缺失责任 |',
             '|---:|---:|---:|---:|---:|---:|---:|---:|']
    for group in groups:
        values = group['metrics']
        lines.append('| ' + ' | '.join([str(group['grant']), str(group['delay_s']) + 's',
                     *[format_range(values[m]) for m in ['offered_units', 'admitted_units', 'closed_units',
                                                       'not_started', 'partial_approval_payments', 'actual_obligations']]]) + ' |')
    lines += ['', '公共责任与签署占用必须分开解释。所有四委员在每轮最终状态一致，公共缺口为零；这一事实仅说明已进入公共执行的经济责任关闭，不说明全部计划请求得到服务，也不释放未成证批准。']
    for grant, delay in [(3600, 1), (3600, 3), (28800, 1), (28800, 3)]:
        subset = [r for r in runs if r['grant'] == grant and r['delay_s'] == delay]
        full = all(r['all_four_member_CAL_slices_full'] for r in subset)
        explained = all(r['residual_fully_explained_by_partial_approvals'] for r in subset)
        peaks = stats([r['sampled_member_CAL_peak'] for r in subset])
        quota = (2 * grant) // 3
        text = f'预算{grant}、延迟{delay}s：单成员CAL额度为{quota}，输入期采样成员峰值为{format_range(peaks)}（峰值/额度中位数{median(r["sampled_member_CAL_peak_quota_fraction"] for r in subset):.2%}）。'
        if full and explained:
            text += '三轮最终四成员均Available=0、Reserved=2400；未公开且未READY的1/2票批准按票数×100恰好解释全部重叠残余占用，未发现三票以上但未公开的QC残留。公共已执行付款全部关闭，支持“低额组织签署容量被未成证局部批准分散占满”的诊断。'
        elif all(r['total_overlapping_member_CAL_residual'] == 0 and r['closed_units'] == r['offered_units']
                 for r in subset):
            text += '三轮全部1,200单位闭环，四成员最终Available=19200、Reserved=0；无部分批准或隐藏QC残余，CAL/Execution/Bytes临时占用归零。完整逐成员数字见members.csv。'
        else:
            text += f'最终四成员均占满={full}，残余全由部分批准解释={explained}；完整逐成员数字见members.csv。'
        lines += ['', text]
    lines += ['', '费用审计逐笔确认所有公共付款FeeSource=OwnerFinalUTXO，组织代付为零；每笔实际94 FUEL，立即找零与托管退款均绑定原付款，最大预留=实际费用+已退款+最终Held，四副本账本一致。未公开费用输入被锁定不等于已支出；其唯一UTXO数量和FUEL金额另列。父延迟1/3s均低于30s赔付超时，本矩阵义务必须全为Fulfilled，不据此声称赔付或永久Intent失败恢复。', '',
              '成功子付款从客户端子请求计时起点到钱包验证保存READY的P50/P95如下。该起点在请求构造完成后，随后仍包含PrepareDirectVector、派发及全部重试，不能称HTTP实际发送时刻；父请求计时起点还早于SaveDirectRequest，两者也不能混用。低额组大量计划任务未启动或已启动未完成，分位数只覆盖成功者；表中保留成功数/计划分母，不能将低成功者的中位数用作整体服务质量。', '',
              '| 备付 | 延迟 | 成功子付款/计划 | READY P50(ms) | READY P95(ms) |',
              '|---:|---:|---:|---:|---:|']
    for group in groups:
        subset = [r for r in runs if r['grant'] == group['grant'] and r['delay_s'] == group['delay_s']]
        lines.append(f"| {group['grant']} | {group['delay_s']}s | {format_range(stats([r['actual_child_success_count'] for r in subset]))} / 1200 | {format_range(group['metrics']['child_fast_p50_ms'])} | {format_range(group['metrics']['child_fast_p95_ms'])} |")
    lines += ['', '| 备付 | 延迟 | 60秒内完整闭环 | 60秒内子付款公共观察 | 最终闭环 | 真实费用FUEL |',
              '|---:|---:|---:|---:|---:|---:|']
    for group in groups:
        values = group['metrics']
        lines.append('| ' + ' | '.join([str(group['grant']), str(group['delay_s']) + 's',
            *[format_range(values[m]) for m in ['closed_units_within_input_window',
                'child_public_observed_within_input_window', 'closed_units', 'user_fee_actual']]]) + ' |')
    lines += ['', '60秒窗口内的钱包公共观察数和完整成员闭环数与排空后最终闭环数分别保存；结束窗口未闭环单位在实际停止时点右删失，不将超时当作完成，不给未完成请求填零。128单位在途窗口满时驱动跳过计划启动，因此这是有背压的预定输入，非压力下无限制纯开环负载。重试沿用既有200ms策略，成员额度拒绝计调用次数，不能当作同样数量的失败付款。', '',
              '曲线定义：curves.csv保留每个节点实际snapshot UnixNS相对驱动start的时间。CAL/Execution/Bytes分别记录单成员Reserved和Available，并以floor(2B/3)归一；四成员权限重叠，不作为四倍备付。公共Reserved是注册覆盖量，不等同一般协议的实际缺口。采样API未直接输出一般Gap；仅详细样本的Open×100表示本实验固定100-CAL义务的未闭合本金，稀疏详细点不可伪装成逐块曲线。units.csv的closed_s取父子成员闭合时间最大值，可绘完整单位累计阶梯曲线；计划曲线为min(20t,1200)，未启动单位没有伪造开始时间。', '',
              '结论范围：三轮只提供本配置的描述性中位数和范围；不估计最低资本、最大TPS、一般多入口活性、经济激励或持有者无条件退出。28,800 CAL充足条件的峰值余量不可反推低预算能同等工作，因为局部部分批准与重试会改变路径和真实义务比例。']
    return '\n'.join(lines) + '\n'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--results', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path, help='new final audit directory')
    args = parser.parse_args()
    results = args.results.resolve()
    supplied = read(results / 'analysis.json')
    summaries = [s for s in supplied if not s.get('pilot')]
    assert len(summaries) == 12, 'refusing partial results: need twelve matrix runs'
    assert len({s['case'] for s in summaries}) == 12
    for grant in [3600, 28800]:
        for delay in [1, 3]:
            cell = [s for s in summaries if s['grant'] == grant and s['delay_s'] == delay]
            assert sorted(s['repetition'] for s in cell) == [1, 2, 3], 'missing repetition'
            assert all(s['offered_units'] == 1200 for s in cell)
    completion = read(results / 'completion.json')
    assert not completion['failures'], 'runner failures need separate diagnosis'
    runs, members, units, curves = [], [], [], []
    for summary in summaries:
        assert read(results / summary['case'] / 'configuration.json')['duration_s'] == 60
        values = audit_case(results / summary['case'], summary)
        runs.append(values[0]); members.extend(values[1]); units.extend(values[2]); curves.extend(values[3])
    # The fixtures are clones of one pristine source/genesis. Only org0 CAL
    # grant/account balance and GenesisTime may differ across the matrix.
    fixtures = []
    for summary in summaries:
        network = read(results / summary['case'] / 'network.json')
        network.pop('GenesisTime', None)
        org = network['Organizations'][0]['Org']
        config = network['Organizations'][0]
        cal_grants = [g for g in network['Genesis']['Grants']
                      if g['Key']['Kind'] == 1 and g['Key']['Account'] == org]
        assert len(cal_grants) == 1
        cal_grants[0]['Amount'] = 0
        accounts = [a for a in network['Accounts'] if a['Owner'] == org and a['Asset'] == 1]
        assert len(accounts) == 1
        accounts[0]['Balance'] = 0
        fixtures.append(json.dumps(network, sort_keys=True, separators=(',', ':')))
    assert len(set(fixtures)) == 1, 'matrix genesis differs beyond declared CAL/start-time controls'
    groups = []
    for grant in [3600, 28800]:
        for delay in [1, 3]:
            cell = [r for r in runs if r['grant'] == grant and r['delay_s'] == delay]
            groups.append({'grant': grant, 'delay_s': delay,
                           'metrics': {m: stats([r[m] for r in cell]) for m in METRICS}})
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    (output / 'summary.json').write_text(json.dumps({'complete_matrix': True, 'runs': runs,
                                                   'groups': groups}, indent=2) + '\n')
    csv_write(output / 'runs.csv', [{k: r[k] for k in ['case', 'grant', 'delay_s', 'repetition',
               *METRICS, 'final_public_gap_CAL', 'partial_one_vote', 'partial_two_votes',
               'hidden_qc_nonpublic', 'all_four_member_CAL_slices_full',
               'residual_fully_explained_by_partial_approvals']} for r in runs])
    csv_write(output / 'members.csv', members)
    csv_write(output / 'units.csv', units)
    csv_write(output / 'curves.csv', curves)
    (output / 'report-zh.md').write_text(make_report(groups, runs), encoding='utf-8')
    metadata = {'results': str(results), 'runs': 12, 'samples': len(curves),
                'script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    for name in ['manifest.json', 'analysis.json', 'completion.json']:
        metadata[name + '_sha256'] = hashlib.sha256((results / name).read_bytes()).hexdigest()
    (output / 'audit-provenance.json').write_text(json.dumps(metadata, indent=2) + '\n')
    print(json.dumps(metadata))


if __name__ == '__main__':
    main()
