#!/usr/bin/env python3
"""Package static binaries, source provenance and dependency redistribution notices."""
import gzip
import hashlib
import io
import json
import os
import pathlib
import subprocess
import tarfile

root = pathlib.Path(__file__).resolve().parent.parent
go = os.environ['RELEASE_GO']
version = os.environ['RELEASE_VERSION']
out = root / 'dist' / version
commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
epoch = int(subprocess.check_output(['git', 'show', '-s', '--format=%ct', 'HEAD'], text=True))
modules = subprocess.check_output([go, 'list', '-deps', '-f', '{{if .Module}}{{.Module.Path}} {{.Module.Version}} {{.Module.Dir}}{{end}}', './cmd/sep47idx'], text=True)
licenses = {}
for line in sorted(set(modules.splitlines())):
    fields = line.split()
    if len(fields) != 3:
        continue
    module, module_version, directory = fields
    for path in pathlib.Path(directory).iterdir():
        if path.is_file() and path.name.lower().startswith(('license', 'copying', 'notice')):
            licenses['licenses/' + module + '@' + module_version + '/' + path.name] = path.read_bytes()
goroot = pathlib.Path(subprocess.check_output([go, 'env', 'GOROOT'], text=True).strip())
licenses['licenses/Go-LICENSE'] = (goroot / 'LICENSE').read_bytes()
manifest = json.dumps({'version': version, 'source_commit': commit, 'source_tree_dirty': bool(subprocess.check_output(['git', 'status', '--porcelain'], text=True)), 'compiler': subprocess.check_output([go, 'version'], text=True).strip()}, indent=2).encode() + b'\n'
checksums = []
for target in ('linux_amd64', 'linux_arm64', 'darwin_amd64', 'darwin_arm64'):
    name = f'sep47idx_{version}_{target}.tar.gz'
    files = dict(licenses, **{'sep47idx': (out / target / 'sep47idx').read_bytes(), 'LICENSE': (root / 'LICENSE').read_bytes(), 'BUILD.json': manifest})
    with (out / name).open('wb') as destination:
        with gzip.GzipFile(filename='', fileobj=destination, mode='wb', mtime=epoch) as compressed:
            with tarfile.open(fileobj=compressed, mode='w') as archive:
                for filename, data in sorted(files.items()):
                    info = tarfile.TarInfo(filename)
                    info.size = len(data)
                    info.mtime = epoch
                    info.mode = 0o755 if filename == 'sep47idx' else 0o644
                    archive.addfile(info, io.BytesIO(data))
    checksums.append(hashlib.sha256((out / name).read_bytes()).hexdigest() + '  ' + name)
(out / 'SHA256SUMS').write_text('\n'.join(checksums) + '\n')
(out / 'BUILD.json').write_bytes(manifest)
print('\n'.join(checksums))
