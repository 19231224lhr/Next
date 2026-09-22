# Two-hour optimization
Base 3d091b0. No consensus/validation/budget changes. Keep 256 fast / 2048 unfinished limits.
H1: gateway database persistence/read interactions starve relay and HTTP reuse; compare existing ephemeral store (experiment only) with synchronous bbolt, same 48k genesis and 800/s 60s development load.
H2: committee app commit disk operations limit block advance; consider separately after H1 evidence.
H3: relay scheduling/HTTP admission needs change only if diagnostic spans identify it.
Held-out acceptance: fresh 144k payments 800/s (three minutes offered); then 1000/s only if supported. Four equal committee states, no failed execution, outboxes empty, financial audit. Do not promote by short-run latency alone.
Record every attempt; execute serially (user prefers no subagents). Resource cleanup only exact completed experiment directories with reports preserved.
