"""Pinned RCAEval subset -> bounded, label-free observation bundles.

Local only: pip install pandas pyarrow requests numpy.
The extraction function receives telemetry and an opaque ID, never a label or
injection timestamp. Scoring labels are emitted to a separate Go package.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
from collections import Counter
import hashlib
import json
from pathlib import Path
import re

import numpy as np
import pandas as pd
import requests
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry

REVISION = 'afeacb11bcc94dadfd1c8f483ee4377b2b8b614e'
REPOSITORY = 'phamquiluan/RCAEval'
# v4 keeps one rectangular, auditable RE2-OB scope. It adds the fifth root
# service and socket resource stress, whose metric signal exists in every
# selected repetition. Loss/disk remain excluded because this bounded experiment is
# intended to demonstrate explainable known-pattern diagnosis, not every
# fault available in the upstream benchmark.
VERSION = 'rcaeval-ob-known-v4'
EXTRACTOR = 'window-thirds-v2'
KINDS = ('cpu', 'mem', 'delay', 'socket')
SERVICES = ('checkoutservice', 'currencyservice', 'emailservice', 'productcatalogservice', 'recommendationservice')
CASES_PER_SPLIT = len(SERVICES) * len(KINDS)
EXPECTED_SPLITS = {name: CASES_PER_SPLIT for name in ('reference', 'development', 'holdout')}
SESSION = requests.Session()
SESSION.mount('https://', HTTPAdapter(max_retries=Retry(
    total=4, connect=4, read=4, backoff_factor=.8,
    status_forcelist=(429, 500, 502, 503, 504), allowed_methods=('GET',))))


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def download(remote, target, size=None):
    if target.exists():
        if size is not None and target.stat().st_size != size:
            raise ValueError(f'Cached file size mismatch: {target}')
        return
    target.parent.mkdir(parents=True, exist_ok=True)
    partial = target.with_suffix(target.suffix + '.partial')
    with SESSION.get(f'https://huggingface.co/datasets/{REPOSITORY}/resolve/{REVISION}/{remote}', stream=True, timeout=(20, 120)) as r:
        r.raise_for_status()
        with partial.open('wb') as f:
            for chunk in r.iter_content(1024 * 1024):
                f.write(chunk)
    if size is not None and partial.stat().st_size != size:
        raise ValueError(f'Download size mismatch: {remote}')
    partial.replace(target)


def selection():
    items = []
    for repetition, split in ((1, 'reference'), (2, 'development'), (3, 'holdout')):
        for service in SERVICES:
            for fault in KINDS:
                items.append((f're2ob_{service}_{fault}_{repetition}', split, service, fault))
    return items


def validate_selection(items):
    """Fail closed if the bounded v4 selection is accidentally widened."""
    expected_total = CASES_PER_SPLIT * len(EXPECTED_SPLITS)
    if len(items) != expected_total:
        raise ValueError(f'v4 selection must contain {expected_total} cases, got {len(items)}')
    split_counts = Counter(split for _, split, _, _ in items)
    if dict(split_counts) != EXPECTED_SPLITS:
        raise ValueError(f'v4 split counts mismatch: {dict(split_counts)}')
    expected = {
        (split, service, fault)
        for split, repetition in (('reference', 1), ('development', 2), ('holdout', 3))
        for service in SERVICES
        for fault in KINDS
    }
    actual = {(split, service, fault) for _, split, service, fault in items}
    if actual != expected:
        raise ValueError('v4 selection must cover every service/fault pair exactly once per split')


def number(x):
    return round(float(x), 6) if np.isfinite(x) else 0.0


def safe_message(text):
    text = re.sub(r'(?i)(password|token|secret|api_key)\s*[:=]\s*\S+', r'\1=[redacted]', str(text))
    return text[:240]


def observation(opaque_id, paths):
    """No fault labels, file-path labels, injection times, or answer-based column selection."""
    metrics = pd.read_parquet(paths['metrics'])
    start, end = int(metrics.time.min()), int(metrics.time.max())
    early, late = start + (end - start) / 3, end - (end - start) / 3
    reference, current = metrics[metrics.time <= early], metrics[metrics.time >= late]
    sources = [{'name': name + '.parquet', 'sha256': digest(path), 'bytes': path.stat().st_size} for name, path in paths.items()]
    observations = {}
    for col in metrics.columns:
        if col == 'time' or '_' not in col:
            continue
        service, metric = col.rsplit('_', 1)
        series = pd.to_numeric(metrics[col], errors='coerce')
        before = pd.to_numeric(reference[col], errors='coerce').replace([np.inf, -np.inf], np.nan).dropna()
        after = pd.to_numeric(current[col], errors='coerce').replace([np.inf, -np.inf], np.nan).dropna()
        if before.empty or after.empty:
            continue
        a, b = float(before.median()), float(after.median())
        eps = max(abs(a) * .01, .000001)
        ratio = max((max(b, 0) + eps) / (max(a, 0) + eps), .000001)
        entry = observations.setdefault(service, {'name': service, 'metrics': {}, 'logs': {}, 'traces': {}})
        entry['metrics'][metric] = {
            'id': f'{opaque_id}:metric:{service}:{metric}', 'column': col,
            'reference': number(a), 'current': number(b), 'ratio': number(ratio),
            'log_change': number(np.clip(np.log2(ratio), -12, 12)),
            'reference_cv': number(float(before.std()) / max(abs(float(before.mean())), .000001)),
            'missing_fraction': number(series.isna().mean()),
            'reference_count': len(before), 'current_count': len(after),
            'unit': 'source_native_unverified',
            'sparkline': [number(series.iloc[indices].median()) for indices in np.array_split(np.arange(len(series)), 12)],
        }

    logs = pd.read_parquet(paths['logs'])
    for service, group in logs.groupby('container_name', sort=True):
        if service not in observations:
            continue
        a, b = group[group.timestamp <= early], group[group.timestamp >= late]
        suspicious = b[b.message.astype(str).str.contains(r'error|fail|exception|timeout|refused|unavailable|denied|panic', case=False, regex=True)]
        examples = suspicious.head(2) if len(suspicious) else b.head(2)
        observations[service]['logs'] = {
            'id': f'{opaque_id}:logs:{service}', 'reference_count': len(a), 'current_count': len(b),
            'keyword_matches': len(suspicious),
            'examples': [{'timestamp': int(row.timestamp), 'message': safe_message(row.message)} for row in examples.itertuples()],
        }

    traces = pd.read_parquet(paths['traces'])
    timestamp = pd.to_numeric(traces.startTimeMillis, errors='coerce') / 1000
    traces = traces.assign(epoch_seconds=timestamp)
    for service, group in traces.groupby('serviceName', sort=True):
        canonical = service if service in observations else (service.removesuffix('service') if service.removesuffix('service') in observations else service + 'service')
        if canonical not in observations:
            continue
        a, b = group[group.epoch_seconds <= early], group[group.epoch_seconds >= late]
        if len(a) == 0 or len(b) == 0:
            continue
        sample = b.iloc[0]
        observations[canonical]['traces'] = {
            'id': f'{opaque_id}:traces:{canonical}', 'reference_spans': len(a), 'current_spans': len(b),
            'reference_p95': number(pd.to_numeric(a.duration).quantile(.95)),
            'current_p95': number(pd.to_numeric(b.duration).quantile(.95)),
            'nonzero_status_count': int(pd.to_numeric(b.statusCode, errors='coerce').fillna(0).ne(0).sum()),
            'sample_span': str(sample.spanID), 'sample_trace': str(sample.traceID),
            'sample_operation': str(sample.operationName)[:160],
            'sample_start_ms': int(sample.startTimeMillis), 'unit': 'source_native_unverified',
        }
    result = {'id': opaque_id, 'start': start, 'end': end, 'reference_end': int(early), 'current_start': int(late),
              'metric_rows': len(metrics), 'log_rows': len(logs), 'trace_rows': len(traces), 'sources': sources,
              'services': [observations[k] for k in sorted(observations)],
              'warnings': ['比较最早/最晚三分之一窗口，参考段健康尚未独立确认。', '保留原始指标单位；分桶摘要不是完整实时遥测。']}
    if len(json.dumps(result).encode()) > 1024 * 1024:
        raise ValueError('Observation exceeds server budget')
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--cache', type=Path, required=True)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[2])
    args = parser.parse_args()
    selected = selection()
    validate_selection(selected)
    def inspect(case):
        r = SESSION.get(f'https://huggingface.co/api/datasets/{REPOSITORY}/tree/{REVISION}/{case}', timeout=(20, 60))
        r.raise_for_status()
        return [row for row in r.json() if row['path'].endswith('.parquet')]
    metadata = []
    with ThreadPoolExecutor(max_workers=4) as pool:
        for rows in pool.map(inspect, (case for case, *_ in selected)):
            metadata.extend(rows)
    total = sum(row['size'] for row in metadata)
    if total > 1024 ** 3:
        raise ValueError(f'Raw selection exceeds 1 GiB: {total}')
    print(f'Preflight: {len(selected)} cases, {len(metadata)} files, {total:,} bytes', flush=True)
    def fetch(row):
        download(row['path'], args.cache / row['path'], row['size'])
        return row['path']
    with ThreadPoolExecutor(max_workers=2) as pool:
        for path in pool.map(fetch, metadata):
            print('Downloaded/verified', path, flush=True)
    download('README.md', args.cache / 'SOURCE-README.md')
    bundles, references, answers, provenance, catalog = [], [], [], [], []
    metric_hashes = set()
    for ordinal, (case, split, service, fault) in enumerate(selected, 1):
        # Opaque IDs contain no service or fault string. The diagnostic code must not use IDs as features.
        opaque = 'rca-' + hashlib.sha256(case.encode()).hexdigest()[:10]
        paths = {name: args.cache / case / (name + '.parquet') for name in ('metrics', 'logs', 'traces')}
        metric_hash = digest(paths['metrics'])
        if metric_hash in metric_hashes:
            raise ValueError('Duplicate metric file across experiments')
        metric_hashes.add(metric_hash)
        bundle = observation(opaque, paths)
        bundles.append(bundle)
        catalog.append({'id': opaque, 'split': split, 'title': f'观测窗口 {ordinal:02d}', 'start': bundle['start'], 'end': bundle['end']})
        label = {'id': opaque, 'service': service, 'fault': fault, 'supported': True, 'split': split}
        answers.append(label)
        provenance.append({'id': opaque, 'original_case': case, 'split': split, 'sources': bundle['sources']})
        if split == 'reference':
            references.append({'id': opaque, 'service': service, 'fault': fault, 'resolution': 'unknown', 'provenance': 'RCAEval controlled fault injection'})
        print(f'Extracted {ordinal}/{len(selected)}: {opaque} ({split})', flush=True)
    if len({bundle['id'] for bundle in bundles}) != len(selected):
        raise ValueError('opaque case IDs are not unique')
    if any(source['name'] not in {'metrics.parquet', 'logs.parquet', 'traces.parquet'}
           for bundle in bundles for source in bundle['sources']):
        raise ValueError('observation source names must remain anonymous')
    files = {
        'internal/rcaexperiment/data/observations.json': {'version': VERSION, 'extractor': EXTRACTOR, 'revision': REVISION, 'catalog': catalog, 'observations': bundles},
        'internal/rcaexperiment/data/references.json': references,
        'internal/rcascoring/data/answers.json': answers,
        'evals/rcaeval/sources.json': {
            'schema_version': 'rcaeval-source-manifest-v4',
            'dataset_version': VERSION,
            'repository': REPOSITORY,
            'revision': REVISION,
            'raw_parquet': 'local-only-not-committed',
            'selection': {
                'services': list(SERVICES),
                'faults': list(KINDS),
                'repetitions': {'1': 'reference', '2': 'development', '3': 'holdout'},
                'case_count': len(selected),
            },
            'cases': provenance,
        },
    }
    for relative, value in files.items():
        path = args.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value, ensure_ascii=False, indent=2, allow_nan=False) + '\n', encoding='utf-8')
        print('Artifact:', relative, path.stat().st_size, 'SHA256', digest(path), flush=True)
    print('No production access; no model calls; no evaluation predictions generated.', flush=True)


if __name__ == '__main__':
    main()
