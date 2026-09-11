#!/usr/bin/env python3
"""Verify and unpack one downloaded Release executable without rebuilding it."""
import hashlib
import os
from pathlib import Path
import sys
import tarfile
import zipfile

folder = Path(sys.argv[1]).resolve()
archives = list(folder.glob('*.tar.gz')) + list(folder.glob('*.zip')) + list(folder.glob('*.exe'))
if len(archives) != 1:
    raise SystemExit('Expected exactly one platform archive')
archive = archives[0]
checks = {line.split()[1].removeprefix('./'): line.split()[0]
          for line in (folder / 'SHA256SUMS').read_text().splitlines()}
if hashlib.sha256(archive.read_bytes()).hexdigest() != checks.get(archive.name):
    raise SystemExit('Release archive checksum mismatch')
dest = folder / 'unpacked'
dest.mkdir()
if archive.suffix == '.exe':
    import shutil
    shutil.copyfile(archive, dest / 'sshd-lite.exe')
elif archive.suffix == '.zip':
    with zipfile.ZipFile(archive) as bundle:
        for entry in bundle.namelist():
            if Path(entry).name != entry or entry in ('.', '..'):
                raise SystemExit('Unexpected archive path')
        bundle.extractall(dest)
else:
    with tarfile.open(archive) as bundle:
        bundle.extractall(dest, filter='data')
binary = dest / ('sshd-lite.exe' if os.name == 'nt' else 'sshd-lite')
if not binary.is_file():
    raise SystemExit('Expected executable missing from release archive')
print(f'SHA256 verified; downloaded executable: {binary}')
if 'GITHUB_OUTPUT' in os.environ:
    with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
        output.write(f'binary={binary}\n')
