#!/usr/bin/env python3
"""Package final changed Go sources and reproducibility scripts, without applying edits.

Run only after final verification. Requires a fresh output directory; excludes
laboratory keys, configuration snapshots, databases, and unrelated documents.
"""
import argparse
import hashlib
import json
import pathlib
import subprocess
import zipfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parents[3]
    output = args.output.resolve()
    if output.exists():
        raise FileExistsError('output must be new: '+str(output))

    def git(*arguments, allow_failure=False):
        result = subprocess.run(['git', '-c', 'core.autocrlf=false', *arguments],
            cwd=root, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if result.returncode and not allow_failure:
            raise RuntimeError(result.stderr.decode('utf-8', errors='replace'))
        return result

    scopes = ('protocol', 'internal', 'cmd')
    head = git('rev-parse', 'HEAD').stdout.decode().strip()
    tracked = git('diff', '--name-only', '-z', 'HEAD', '--', *scopes).stdout
    untracked = git('ls-files', '--others', '--exclude-standard', '-z', '--', *scopes).stdout
    tracked_paths = [p.decode('utf-8') for p in tracked.split(b'\0') if p]
    new_go = [p.decode('utf-8') for p in untracked.split(b'\0') if p and p.endswith(b'.go')]
    patch = git('diff', '--binary', '--full-index', 'HEAD', '--', *scopes).stdout
    for name in sorted(new_go):
        data = (root/name).read_bytes()
        # Go source is text. Explicit new-file hunks preserve EOF-without-newline
        # and let reverse --check validate untracked files as well as tracked ones.
        data.decode('utf-8')
        lines = data.splitlines(keepends=True)
        new_patch = f'diff --git a/{name} b/{name}\nnew file mode 100644\n'.encode()
        if lines:
            new_patch += f'--- /dev/null\n+++ b/{name}\n@@ -0,0 +1,{len(lines)} @@\n'.encode()
            for line in lines:
                new_patch += b'+'+line
                if not line.endswith(b'\n'):
                    new_patch += b'\n\\ No newline at end of file\n'
        patch += new_patch

    source_names = set(tracked_paths+new_go)
    source_names.update(('go.mod', 'go.sum'))
    experiment = root/'docs/experiments'
    script_dirs = [experiment/'c3-speed-2026-10-14',
                   experiment/'c3-issuer-funded-2026-10-13',
                   experiment/'baseline-design-2026-10-13',
                   experiment/'fastpay-runtime-sensitivity-2026-10-14']
    for directory in script_dirs:
        # Direct children only: never collect run directories or rejected candidate
        # snapshots recursively. Native init-lab generation is in cmd Go sources.
        for extension in ('*.py', '*.sh', '*.ps1'):
            for path in directory.glob(extension):
                source_names.add(path.relative_to(root).as_posix())
    for path in (experiment/'fastpay-runtime-sensitivity-2026-10-14').glob('*.md'):
        source_names.add(path.relative_to(root).as_posix())
    # Runtime sensitivity prepare.py consumes a frozen Rust adapter file rather
    # than generating its source, so include the existing reference adapters.
    for name in ('fastpay_client.rs', 'fastpay_client_default.rs'):
        path = experiment/'baseline-design-2026-10-13'/name
        if path.is_file():
            source_names.add(path.relative_to(root).as_posix())
    reproduce = experiment/'c3-speed-2026-10-14/REPRODUCE.md'
    if reproduce.is_file():
        source_names.add(reproduce.relative_to(root).as_posix())

    forbidden = {'keys', 'config', 'configs', 'db', 'databases', 'paper'}
    files, deleted = {}, []
    for name in sorted(source_names):
        relative = pathlib.PurePosixPath(name)
        if relative.is_absolute() or '..' in relative.parts or forbidden.intersection(relative.parts):
            raise ValueError('forbidden package path: '+name)
        path = root/name
        if not path.is_file():
            if name in tracked_paths:
                deleted.append(name)
                continue
            raise FileNotFoundError(path)
        data = path.read_bytes()
        files[name] = {'sha256':hashlib.sha256(data).hexdigest(), 'bytes':len(data)}

    output.mkdir(parents=True, exist_ok=False)
    patch_path = output/'final-code.patch'
    patch_path.write_bytes(patch)
    # Check only. Never apply either direction or change the checkout/index.
    checked = git('apply', '--reverse', '--check', str(patch_path), allow_failure=True)
    check_log = checked.stdout+checked.stderr
    (output/'reverse-check.log').write_bytes(check_log)
    source_manifest = {
        'base_head':head, 'scope':list(scopes), 'tracked_changes':tracked_paths,
        'new_go_files':sorted(new_go), 'deleted_files':deleted, 'files':files,
        'patch_sha256':hashlib.sha256(patch).hexdigest(),
        'reverse_check_command':['git','apply','--reverse','--check',str(patch_path)],
        'reverse_check_exit':checked.returncode,
    }
    (output/'source-manifest.json').write_text(json.dumps(source_manifest,indent=2)+'\n',encoding='utf-8')
    archive = output/'final-code-source.zip'
    with zipfile.ZipFile(archive,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=9) as bundle:
        for name in sorted(files):
            bundle.write(root/name,name)
        bundle.write(output/'source-manifest.json','source-manifest.json')
        bundle.write(patch_path,'final-code.patch')
    manifest = dict(source_manifest, archive_sha256=hashlib.sha256(archive.read_bytes()).hexdigest(),
                    archive_bytes=archive.stat().st_size)
    (output/'package-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n',encoding='utf-8')
    print(json.dumps({'output':str(output),'files':len(files),'new_go_files':len(new_go),
                     'reverse_check_exit':checked.returncode,'archive_sha256':manifest['archive_sha256']}))
    return checked.returncode


if __name__ == '__main__':
    raise SystemExit(main())
