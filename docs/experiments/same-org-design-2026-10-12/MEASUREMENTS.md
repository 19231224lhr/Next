# Four-arm measurement summary

Each cell is the median of eight run-level statistics; each run has 100 dependent payments.
All variants within a condition share exactly the same presigned workload. No INSTALL prewarming.
Latency includes collector quorum collection and receiving-wallet verification/commit; transaction construction and sender request saving are outside this timer.
Paced conditions are synthetic independent per-member payload links, not a WAN deployment.

## same-memory.json

| Mode | p50 ms | p95 ms | Paired p50 reduction | Faster rounds | Request bytes/100 | Response bytes/100 | Fallbacks |
|---|---:|---:|---:|---:|---:|---:|---:|
| baseline | 2.7964 | 4.7229 | 0.00% | 0/8 | 1,119,404 | 771,987 | 0 |
| continuation | 2.7889 | 4.5413 | -0.19% | 4/8 | 908,237 | 771,987 | 0 |
| compact-response | 2.7176 | 4.8054 | 2.02% | 5/8 | 1,119,404 | 126,800 | 0 |
| combined | 2.7525 | 4.6350 | 1.24% | 6/8 | 908,237 | 126,800 | 0 |

## cross-memory.json

| Mode | p50 ms | p95 ms | Paired p50 reduction | Faster rounds | Request bytes/100 | Response bytes/100 | Fallbacks |
|---|---:|---:|---:|---:|---:|---:|---:|
| baseline | 2.8725 | 4.6893 | 0.00% | 0/8 | 1,119,404 | 771,914 | 0 |
| continuation | 2.9575 | 4.8106 | -2.22% | 3/8 | 1,119,404 | 771,914 | 0 |
| compact-response | 2.8023 | 4.4793 | 0.94% | 5/8 | 1,119,404 | 126,800 | 0 |
| combined | 2.8113 | 4.7787 | 1.07% | 5/8 | 1,119,404 | 126,800 | 0 |

## same-paced-1mbps.json

| Mode | p50 ms | p95 ms | Paired p50 reduction | Faster rounds | Request bytes/100 | Response bytes/100 | Fallbacks |
|---|---:|---:|---:|---:|---:|---:|---:|
| baseline | 41.8961 | 43.8240 | 0.00% | 0/8 | 1,119,404 | 771,699 | 0 |
| continuation | 36.1987 | 37.9463 | 13.45% | 8/8 | 908,237 | 771,699 | 0 |
| compact-response | 28.8953 | 30.7655 | 31.05% | 8/8 | 1,119,404 | 126,800 | 0 |
| combined | 23.3360 | 25.1308 | 44.35% | 8/8 | 908,237 | 111,750 | 0 |

## same-paced-10mbps.json

| Mode | p50 ms | p95 ms | Paired p50 reduction | Faster rounds | Request bytes/100 | Response bytes/100 | Fallbacks |
|---|---:|---:|---:|---:|---:|---:|---:|
| baseline | 6.9307 | 8.4602 | 0.00% | 0/8 | 1,119,404 | 771,947 | 0 |
| continuation | 6.4778 | 7.6311 | 6.87% | 8/8 | 908,237 | 771,947 | 0 |
| compact-response | 5.9143 | 7.4466 | 15.64% | 8/8 | 1,119,404 | 126,800 | 0 |
| combined | 5.3462 | 6.9475 | 22.85% | 8/8 | 908,237 | 126,800 | 0 |

## cross-paced-10mbps.json

| Mode | p50 ms | p95 ms | Paired p50 reduction | Faster rounds | Request bytes/100 | Response bytes/100 | Fallbacks |
|---|---:|---:|---:|---:|---:|---:|---:|
| baseline | 7.0039 | 8.5777 | 0.00% | 0/8 | 1,119,404 | 771,830 | 0 |
| continuation | 6.9644 | 8.2347 | -0.28% | 4/8 | 1,119,404 | 771,830 | 0 |
| compact-response | 5.9393 | 7.5220 | 14.23% | 8/8 | 1,119,404 | 126,800 | 0 |
| combined | 5.8971 | 7.5194 | 15.59% | 8/8 | 1,119,404 | 126,800 | 0 |

## same-durable.json

| Mode | p50 ms | p95 ms | Paired p50 reduction | Faster rounds | Request bytes/100 | Response bytes/100 | Fallbacks |
|---|---:|---:|---:|---:|---:|---:|---:|
| baseline | 7.1730 | 8.8896 | 0.00% | 0/8 | 1,119,404 | 771,952 | 0 |
| continuation | 7.1649 | 9.2943 | -0.09% | 4/8 | 908,237 | 771,952 | 0 |
| compact-response | 6.9983 | 8.8918 | 0.57% | 6/8 | 1,119,404 | 126,800 | 0 |
| combined | 7.1998 | 9.3934 | 1.19% | 5/8 | 908,237 | 126,800 | 0 |
