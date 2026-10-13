"""Sequential final tests and builds. Run only when all timed experiments stop."""
import hashlib
import json
import os
import pathlib
import subprocess
import time

here = pathlib.Path(__file__).resolve().parent
repo = here.parents[2]
out = here / 'final-verification'
out.mkdir(exist_ok=False)
env = dict(os.environ)
for key in ('GOOS', 'GOARCH', 'CGO_ENABLED', 'GOMAXPROCS'):
    env.pop(key, None)
steps = []

def run(label, command, environment):
    start = time.time_ns()
    print('START ' + label, flush=True)
    with (out / (label + '.log')).open('wb') as log:
        code = subprocess.run(command, cwd=repo, env=environment, stdout=log, stderr=subprocess.STDOUT).returncode
    steps.append(dict(label=label, command=command, start_ns=start, end_ns=time.time_ns(), exit=code))
    (out / 'status.json').write_text(json.dumps(steps, indent=2))
    print('END ' + label + ' exit=' + str(code), flush=True)
    if code:
        raise SystemExit(code)

run('go-version', ['go', 'version'], env)
run('format-restored-gateway', ['gofmt', '-w', 'internal/gateway/direct_relay.go',
                              'internal/gateway/direct_outbox_cache.go',
                              'internal/gateway/direct_install_bytes_test.go',
                              'internal/gateway/direct_outbox_cache_test.go'], env)
run('go-all', ['go', 'test', '-tags=comet_v3', '-count=1', './...'], env)
run('go-race', ['go', 'test', '-tags=comet_v3', '-race', '-count=1', './protocol',
               './internal/rules', './internal/member', './internal/gateway', './internal/transport'], env)
binary = repo / '.scratch/c3-speed-final-bin'
binary.mkdir(exist_ok=False)
buildenv = dict(env, GOOS='linux', GOARCH='amd64', CGO_ENABLED='0')
hashes = {}
for name in ('payctl', 'member', 'gateway', 'committee'):
    path = binary / name
    run('build-' + name, ['go', 'build', '-tags=comet_v3', '-o', str(path), './cmd/' + name], buildenv)
    hashes[name] = dict(path=str(path), sha256=hashlib.sha256(path.read_bytes()).hexdigest())
sources = subprocess.check_output(['git', 'diff', '--name-only', '--', 'protocol', 'internal', 'cmd'], cwd=repo, text=True).splitlines()
sources += subprocess.check_output(['git', 'ls-files', '--others', '--exclude-standard', '--', 'protocol', 'internal', 'cmd'], cwd=repo, text=True).splitlines()
manifest = dict(base_head=subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip(),
                build_env={key:buildenv[key] for key in ('GOOS', 'GOARCH', 'CGO_ENABLED')}, binaries=hashes,
                changed_source_sha256={name:hashlib.sha256((repo/name).read_bytes()).hexdigest() for name in sources},
                all_steps_passed=all(step['exit']==0 for step in steps))
(out / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print(json.dumps(manifest, indent=2))
