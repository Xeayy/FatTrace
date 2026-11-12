"""Generate a neutral demo dataset for screenshots / trying FatTrace.

Creates D:\\FatTraceDemo (no real user data). Usage:
    python scripts/demo_data.py init    # first snapshot source
    python scripts/demo_data.py grow    # mutate, then take a second snapshot

Then:
    fat snap --db <demo.db> D:\\FatTraceDemo   (twice, around `grow`)
    fat serve --db <demo.db> --open
"""
import os
import sys

ROOT = r"D:\FatTraceDemo"
MB = 1024 * 1024


def put(rel, size):
    p = os.path.join(ROOT, rel)
    os.makedirs(os.path.dirname(p), exist_ok=True)
    with open(p, "wb") as f:
        f.write(b"\0" * size)


def init():
    put(r"Games\SomeGame\data.pak", 60 * MB)
    put(r"Games\SomeGame\engine.dll", 8 * MB)
    for i in range(40):
        put(r"AppCache\Browser\Cache\f%03d.bin" % i, 256 * 1024)
    put(r"Logs\app.log", 4 * MB)
    put(r"Projects\app\node_modules\lib-a\index.js", 6 * MB)
    put(r"Projects\app\src\main.go", 64 * 1024)
    put(r"Apps\SomeApp\1.0.0\SomeApp.exe", 18 * MB)
    put(r"Apps\SomeApp\1.0.0\resources.pak", 6 * MB)
    put(r"Documents\report.docx", 2 * MB)


def grow():
    put(r"Logs\app.log", 14 * MB)
    for i in range(40, 90):
        put(r"AppCache\Browser\Cache\f%03d.bin" % i, 256 * 1024)
    put(r"Projects\app\node_modules\lib-b\index.js", 12 * MB)
    put(r"Projects\app\node_modules\lib-a\index.js", 9 * MB)
    put(r"Apps\SomeApp\1.1.0\SomeApp.exe", 20 * MB)
    put(r"Apps\SomeApp\1.1.0\resources.pak", 7 * MB)
    put(r"Downloads\installer-2.0.exe", 26 * MB)
    put(r"Games\SomeGame\patch-1.1.pak", 15 * MB)


if __name__ == "__main__":
    if len(sys.argv) < 2 or sys.argv[1] not in ("init", "grow"):
        print(__doc__)
        sys.exit(2)
    (init if sys.argv[1] == "init" else grow)()
    print("demo", sys.argv[1], "done at", ROOT)
