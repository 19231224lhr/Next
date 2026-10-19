"""Measurement reductions; every sample retains its originating run."""
import math


def quantile(values, probability):
    values = sorted(values)
    if not values or not 0 <= probability <= 1 or any(not math.isfinite(v) for v in values):
        raise ValueError('invalid quantile input')
    index = (len(values) - 1) * probability
    lo = math.floor(index)
    hi = math.ceil(index)
    return values[lo] + (values[hi] - values[lo]) * (index - lo)


def distribution(values, prefix):
    if not values:
        return {prefix + suffix: None for suffix in ('_p50', '_p95', '_min', '_max')}
    return {prefix+'_p50': quantile(values, .5), prefix+'_p95': quantile(values, .95),
            prefix+'_min': min(values), prefix+'_max': max(values)}


def mixed_metrics(report, start):
    begin, end = start['FormalNS'], start['EndSendNS']
    if end <= begin:
        raise ValueError('invalid formal window')
    cohort = [s for s in report.get('Samples', []) if begin <= s['ScheduledNS'] < end]
    attempted = [s for s in cohort if s.get('SentNS', 0) > 0]
    receipts = [s for s in attempted if s.get('ReadyNS', 0) > 0]
    public = [s for s in attempted if s.get('PublicNS', 0) > 0]
    closed = [s for s in public if len(s.get('MemberNS', [])) == 4 and all(v > 0 for v in s['MemberNS'])]
    inside = [s for s in receipts if begin <= s['ReadyNS'] < end]
    public_inside = [s for s in public if begin <= s['PublicNS'] < end]
    fast = [s['Monotonic']['FastNS']/1e6 for s in receipts if s.get('Monotonic', {}).get('FastNS', 0) > 0]
    if len(fast) != len(receipts):
        raise ValueError('receipt lacks native monotonic timing')
    seconds = (end-begin)/1e9
    nominal = report.get('Options', {}).get('Rate', 0) * seconds
    return {'formal_samples': len(cohort), 'attempted': len(attempted), 'unsent': len(cohort)-len(attempted),
            'unsent_scope':'formal samples created but never sent; excludes scheduler slots without a sample',
            'nominal_formal_offered':nominal if nominal else None,
            'offered_receipt_fraction':len(receipts)/nominal if nominal else None,
            'offered_closed_fraction':len(closed)/nominal if nominal else None,
            **{k.lower()+'_including_warmup':report.get(k) for k in ('Scheduled','SkippedPacing','NoReady','PendingFull','WorkerFull')},
            'receipts': len(receipts), 'public': len(public), 'closed': len(closed),
            'in_window_receipts': len(inside), 'in_window_public': len(public_inside),
            'send_window_seconds': seconds,
            'receipt_goodput_per_s': len(inside)/seconds, 'public_goodput_per_s': len(public_inside)/seconds,
            'completion_fraction': len(closed)/len(attempted) if attempted else None,
            'completion_denominator':'formal-cohort attempted payments (SentNS>0), after drain',
            'receipt_fraction': len(receipts)/len(attempted) if attempted else None,
            **distribution(fast, 'receipt_ms'),
            **distribution([s['CertificateBytes'] for s in receipts if s.get('CertificateBytes')], 'certificate_bytes')}


def trace_spans(events):
    definitions = {'atomic_evaluation': ('update_started', 'update_evaluated'),
                   'commit_wait': ('commit_requested', 'commit_returned'),
                   'signing': ('commit_returned', 'vote_signed'),
                   'parent_validation': ('vector_prepared', 'parents_verified')}
    active, result = {}, []
    for e in events or []:
        node, stage, instant = e.get('node'), e.get('stage'), e.get('local_ns')
        if node is None or not isinstance(instant, int):
            continue
        for name, (start, finish) in definitions.items():
            key = (node, name)
            if stage == start:
                active[key] = instant
            elif stage == finish and key in active:
                elapsed = instant - active.pop(key)
                if elapsed >= 0:
                    result.append({'node': node, 'span': name, 'duration_ns': elapsed})
    return result
