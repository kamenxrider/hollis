#!/bin/bash
# Verify signal-driven cleanup without requesting inference.
set -euo pipefail
python3 - "$1" <<'PY'
import json
import os
import pathlib
import signal
import struct
import subprocess
import sys
import tempfile
import time
import zlib

binary = pathlib.Path(sys.argv[1]).resolve()
for signum in (signal.SIGTERM, signal.SIGHUP):
    with tempfile.TemporaryDirectory(prefix="hollis-signal-", dir="/tmp") as tmp:
        root = pathlib.Path(tmp).resolve()
        for name in ("home", "state", "tmp"):
            (root / name).mkdir()
        os.link(binary, root / "hollis")
        helper = root / "hollis-native"
        helper.write_text("""#!/usr/bin/env python3
import json,os,pathlib,sys,time
if "--protocol-version" in sys.argv:
 print(4);sys.exit(0)
if "--version" in sys.argv:
 print("hollis-native 0.7.0");sys.exit(0)
request=json.loads(sys.stdin.readline())
print(json.dumps({"event":"status","model":"local","available":True,"version":"0.7.0","protocol":4,"capabilities":{"images":{"supported":True,"max_images":1,"formats":["png","jpeg"],"max_side_px":1000}}}),flush=True)
if request["operation"]!="status":
 pathlib.Path(os.environ["SIGNAL_READY"]).touch()
 time.sleep(300)
""")
        helper.chmod(0o700)
        image = root / "fixture.png"
        def chunk(kind, data):
            return struct.pack(">I",len(data))+kind+data+struct.pack(">I",zlib.crc32(kind+data))
        image.write_bytes(b"\x89PNG\r\n\x1a\n"
                          +chunk(b"IHDR",struct.pack(">IIBBBBB",1,1,8,2,0,0,0))
                          +chunk(b"IDAT",zlib.compress(b"\0\xff\0\0"))+chunk(b"IEND",b""))
        env = {k:v for k,v in os.environ.items() if not k.startswith("HOLLIS_")}
        env.update(HOME=str(root/"home"), HOLLIS_STATE_DIR=str(root/"state"),
                   TMPDIR=str(root/"tmp"), SIGNAL_READY=str(root/"ready"))
        proc = subprocess.Popen([str(root/"hollis"), "respond", "--model", "local",
                                 "--image", str(image), "Describe the fixture"],
                                env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            deadline = time.monotonic() + 15
            while not (root/"ready").exists():
                if proc.poll() is not None or time.monotonic() >= deadline:
                    raise RuntimeError("fixture not ready: " + repr(proc.communicate(timeout=2)))
                time.sleep(0.02)
            assert list((root/"tmp").rglob("hollis-*")), "fixture never staged an attachment"
            proc.send_signal(signum)
            stdout, stderr = proc.communicate(timeout=10)
            assert proc.returncode != 0, (stdout, stderr)
            leftovers = list((root/"tmp").rglob("hollis-*"))
            assert not leftovers, f"{signum}: staged private files remained: {leftovers}"
            print(json.dumps({"signal":signum.name,"staging_removed":True,"inference_requests":0}))
        finally:
            if proc.poll() is None:
                proc.kill()
                proc.communicate(timeout=5)
    assert not root.exists()
PY
