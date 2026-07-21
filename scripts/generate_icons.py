#!/usr/bin/env python3
"""Generate minimal 32x32 placeholder PNG icons. Replace with real art before shipping."""
import struct, zlib, os

def make_png(r, g, b, size=32):
    def chunk(tag, data):
        crc = zlib.crc32(tag + data) & 0xffffffff
        return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", crc)
    row = bytes([0]) + bytes([r, g, b, 255] * size)
    raw = row * size
    ihdr = struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", ihdr)
            + chunk(b"IDAT", zlib.compress(raw))
            + chunk(b"IEND", b""))

icons = {
    "connected.png":    (0x29, 0xA3, 0x5E),
    "disconnected.png": (0x88, 0x88, 0x88),
    "connecting.png":   (0xF5, 0xA6, 0x23),
    "warning.png":      (0xE5, 0x53, 0x3A),
}

out = os.path.join(os.path.dirname(__file__), "..", "assets", "icons")
os.makedirs(out, exist_ok=True)
for name, (r, g, b) in icons.items():
    path = os.path.join(out, name)
    with open(path, "wb") as f:
        f.write(make_png(r, g, b))
    print(f"  wrote {path}")
