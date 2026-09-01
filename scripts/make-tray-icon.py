#!/usr/bin/env python3
"""Draws the menu-bar template icon.

macOS template images are alpha-only: the system paints them black or white to
match the menu bar, so the artwork is a silhouette and nothing else. It is
generated rather than hand-drawn so the shape is reviewable as code and can be
regenerated at another size without a design tool.

    python3 scripts/make-tray-icon.py

Writes desktop/src-tauri/icons/tray-Template.png (36x36, 4x supersampled).
"""

import pathlib
import struct
import zlib

SIZE = 36
SS = 4  # supersampling factor, for antialiased edges


def quad(p0, p1, p2, t):
    u = 1.0 - t
    return (
        u * u * p0[0] + 2 * u * t * p1[0] + t * t * p2[0],
        u * u * p0[1] + 2 * u * t * p1[1] + t * t * p2[1],
    )


def in_polygon(pt, poly):
    x, y = pt
    inside = False
    j = len(poly) - 1
    for i in range(len(poly)):
        xi, yi = poly[i]
        xj, yj = poly[j]
        if (yi > y) != (yj > y) and x < (xj - xi) * (y - yi) / (yj - yi) + xi:
            inside = not inside
        j = i
    return inside


def in_ellipse(pt, cx, cy, rx, ry):
    return ((pt[0] - cx) / rx) ** 2 + ((pt[1] - cy) / ry) ** 2 <= 1.0


def near_curve(pt, p0, p1, p2, r0, r1, steps=64):
    for i in range(steps + 1):
        t = i / steps
        cx, cy = quad(p0, p1, p2, t)
        r = r0 + (r1 - r0) * t
        if (pt[0] - cx) ** 2 + (pt[1] - cy) ** 2 <= r * r:
            return True
    return False


# The goat, in a unit box. A face that tapers to a muzzle, a beard under the
# chin, two ears, and two horns swept back over the skull.
FACE = [
    (0.32, 0.36), (0.68, 0.36), (0.67, 0.55), (0.62, 0.68),
    (0.575, 0.78), (0.545, 0.86), (0.455, 0.86), (0.425, 0.78),
    (0.38, 0.68), (0.33, 0.55),
]
BEARD = [(0.455, 0.82), (0.545, 0.82), (0.525, 0.97), (0.475, 0.97)]
HORN_L = ((0.375, 0.375), (0.20, 0.30), (0.115, 0.10))
HORN_R = ((0.625, 0.375), (0.80, 0.30), (0.885, 0.10))
EYES = [(0.425, 0.50), (0.575, 0.50)]


def covered(pt):
    if in_polygon(pt, FACE) or in_polygon(pt, BEARD):
        return True
    if in_ellipse(pt, 0.245, 0.455, 0.10, 0.05):
        return True
    if in_ellipse(pt, 0.755, 0.455, 0.10, 0.05):
        return True
    if near_curve(pt, *HORN_L, 0.058, 0.016):
        return True
    if near_curve(pt, *HORN_R, 0.058, 0.016):
        return True
    return False


def alpha_at(px, py):
    """Coverage of one output pixel, sampled SS x SS."""
    hits = 0
    for sy in range(SS):
        for sx in range(SS):
            u = (px + (sx + 0.5) / SS) / SIZE
            v = (py + (sy + 0.5) / SS) / SIZE
            if not covered((u, v)):
                continue
            # Eyes are punched out of the silhouette, not drawn on top of it:
            # a template image has one colour, so a hole is the only contrast
            # available.
            if any(in_ellipse((u, v), ex, ey, 0.032, 0.042) for ex, ey in EYES):
                continue
            hits += 1
    return round(255 * hits / (SS * SS))


def png(path, rows):
    def chunk(tag, payload):
        body = tag + payload
        return struct.pack(">I", len(payload)) + body + struct.pack(">I", zlib.crc32(body))

    raw = b"".join(b"\x00" + row for row in rows)
    out = b"\x89PNG\r\n\x1a\n"
    out += chunk(b"IHDR", struct.pack(">IIBBBBB", SIZE, SIZE, 8, 6, 0, 0, 0))
    out += chunk(b"IDAT", zlib.compress(raw, 9))
    out += chunk(b"IEND", b"")
    path.write_bytes(out)


def main():
    rows = []
    for py in range(SIZE):
        row = bytearray()
        for px in range(SIZE):
            a = alpha_at(px, py)
            row += bytes((0, 0, 0, a))  # black + coverage; macOS recolours it
        rows.append(bytes(row))

    target = pathlib.Path(__file__).resolve().parent.parent / "desktop/src-tauri/icons/tray-Template.png"
    png(target, rows)
    print(f"wrote {target} ({SIZE}x{SIZE})")


if __name__ == "__main__":
    main()
