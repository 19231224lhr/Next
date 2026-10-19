"""Create a transparent lab binary directory with explicit service CLI options."""
import argparse
import hashlib
import json
import pathlib
import shlex

p = argparse.ArgumentParser()
p.add_argument('--source', type=pathlib.Path, required=True)
p.add_argument('--destination', type=pathlib.Path, required=True)
p.add_argument('--compact', action='store_true')
p.add_argument('--same-org', action='store_true')
p.add_argument('--client', type=pathlib.Path, help='common workload client; service binaries stay from source')
a = p.parse_args()
source, destination = a.source.resolve(), a.destination.resolve()
destination.mkdir(parents=True, exist_ok=False)
manifest = {}
for name in ('payctl', 'member', 'gateway', 'committee'):
    binary = a.client.resolve() if name == 'payctl' and a.client else source/name
    options = []
    if name == 'gateway' and a.compact:
        options.append('--compact-direct-approvals')
    if name in ('gateway', 'member') and a.same_org:
        options.append('--same-org-continuation')
    if options:
        target = destination/name
        target.write_text('#!/bin/sh\nexec '+shlex.join([str(binary), *options])+' "$@"\n')
        target.chmod(0o755)
    else:
        (destination/name).symlink_to(binary)
    manifest[name] = {'binary':str(binary), 'sha256':hashlib.sha256(binary.read_bytes()).hexdigest(), 'options':options}
(destination/'variant.json').write_text(json.dumps(manifest, indent=2)+'\n')
print(json.dumps(manifest, indent=2))
