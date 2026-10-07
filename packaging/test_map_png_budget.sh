#!/usr/bin/bash
# Separate processes keep the memory threshold independent of other Qt tests.
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
probe=$(mktemp -d /tmp/weather-png-budget.XXXXXXXX)
trap 'rm -rf -- "$probe"' EXIT
python3 - "$probe" "$root" <<'PY'
import json, pathlib, struct, sys, zlib
out, repo = map(pathlib.Path, sys.argv[1:])
def chunk(kind, data):
    return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))
header = b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB',256,256,8,2,0,0,0))
pixels = chunk(b'IDAT', zlib.compress((b'\0' + b'\x80'*768)*256))
end = chunk(b'IEND', b'')
(out/'plain.png').write_bytes(header+pixels+end)
bomb = zlib.compress(b'A'*(4*1024*1024))
for name, chunks in {
 'texts': b''.join(chunk(b'zTXt',f'comment{i}'.encode()+b'\0\0'+bomb) for i in range(4)),
 'international': chunk(b'iTXt',b'comment\0\1\0\0\0'+bomb),
 'profile': chunk(b'iCCP',b'profile\0\0'+bomb),
}.items():
    (out/(name+'.png')).write_bytes(header+chunks+pixels+end)
(out/'probe.cpp').write_text('#include '+json.dumps(str(repo/'native/qt/maptiles.cpp'))+'\n'+r'''
#include <QCoreApplication>
#include <QFile>
#include <sys/resource.h>
#include <cstdio>
int main(int argc, char **argv) {
    QCoreApplication app(argc,argv);
    QFile file(argv[1]);
    if (!file.open(QIODevice::ReadOnly)) return 2;
    const auto bytes=file.readAll();
    const bool expected=QString(argv[2])=="accept";
    for (int i=0;i<16;++i)
        if (validTilePNG(bytes)!=expected) return 3;
    struct rusage usage{}; getrusage(RUSAGE_SELF,&usage);
    printf("input=%lld accepted=%d replies=16 maxrss_kib=%ld\n",
        (long long)bytes.size(),expected,usage.ru_maxrss);
    // Qt/library process overhead is included. No exact host RSS equality.
    return usage.ru_maxrss < 64*1024 ? 0 : 4;
}
''')
(out/'probe.pro').write_text('QT += gui network\nCONFIG += console c++17\nTARGET = probe\nSOURCES += probe.cpp\nHEADERS += '+json.dumps(str(repo/'native/qt/maptiles.h'))+'\n')
PY
cd "$probe"
qmake6 probe.pro
make -j2
./probe plain.png accept
for fixture in texts international profile; do ./probe "$fixture.png" reject; done
printf 'PASS: PNG metadata rejected before decoding, including 16-reply aggregate work under 64 MiB process RSS.\n'
