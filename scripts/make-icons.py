#!/usr/bin/env python3
"""Generates every Mimir app icon from the brand's six-armed asterisk.

The mark is the brand's shorthand — "a mark that travels alone", used for the
favicon, the app icon and the avatar (Brand System Guide, p.7). It is drawn from
the geometry of `mimir 1.svg`'s asterisk rather than traced by hand, so every
size is the same shape and a new size costs nothing.

    python3 scripts/make-icons.py

Writes:
  desktop/src-tauri/icons/mimirTemplate.png   menu-bar icon (alpha only; macOS recolours it)
  desktop/src-tauri/icons/icon.png            1024px app icon, Mist mark on Carbon
  desktop/src-tauri/icons/icon.icns           the .icns Tauri bundles
  desktop/public/favicon.png                  the WebView tab icon
"""

import math
import pathlib
import struct
import subprocess
import tempfile
import zlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
ICONS = ROOT / "desktop/src-tauri/icons"

# Brand System Guide, p.8. Four colours, no fifth.
CARBON = (0x10, 0x11, 0x14)
MIST = (0xEE, 0xF0, 0xF2)

SS = 4  # supersampling factor per axis

# The mark, verbatim from the brand asset `electric.svg` — a 15-point polygon in
# a 104x99 viewBox. Traced by hand it would be a different shape at every size;
# taken from the file it is the same one the deck prints.
MARK_VIEWBOX = (104.0, 99.0)
MARK_POINTS = [
    (30.0137, 98.7852), (13.8252, 86.9688), (37.9307, 55.8916), (0.0, 45.0205),
    (6.2627, 25.9961), (43.248, 39.4668), (41.8301, 0.0), (61.918, 0.0),
    (60.5, 39.4668), (97.4854, 25.9961), (103.748, 45.0205), (65.8174, 55.8916),
    (90.041, 86.9688), (73.8525, 98.7852), (51.7559, 65.8174),
]


def _normalised() -> list[tuple[float, float]]:
    """The polygon centred on the origin and scaled so its longer side spans 1."""
    w, h = MARK_VIEWBOX
    span = max(w, h)
    return [((x - w / 2) / span, (y - h / 2) / span) for x, y in MARK_POINTS]


POLY = _normalised()


def covered(x: float, y: float) -> bool:
    """Is this point (centred, the mark spanning 1 unit) inside the mark?"""
    inside = False
    j = len(POLY) - 1
    for i, (xi, yi) in enumerate(POLY):
        xj, yj = POLY[j]
        if (yi > y) != (yj > y) and x < (xj - xi) * (y - yi) / (yj - yi) + xi:
            inside = not inside
        j = i
    return inside


def coverage(px: int, py: int, size: int, inset: float) -> float:
    """Antialiased coverage of one pixel, sampled SS x SS."""
    hits = 0
    span = size * inset
    for sy in range(SS):
        for sx in range(SS):
            # Map pixel space onto the mark's unit box, `inset` of the icon wide.
            x = ((px + (sx + 0.5) / SS) - size / 2) / span
            y = ((py + (sy + 0.5) / SS) - size / 2) / span
            if covered(x, y):
                hits += 1
    return hits / (SS * SS)


def write_png(path: pathlib.Path, size: int, rows: list[bytes]) -> None:
    def chunk(tag: bytes, payload: bytes) -> bytes:
        body = tag + payload
        return struct.pack(">I", len(payload)) + body + struct.pack(">I", zlib.crc32(body))

    raw = b"".join(b"\x00" + row for row in rows)
    out = b"\x89PNG\r\n\x1a\n"
    out += chunk(b"IHDR", struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0))
    out += chunk(b"IDAT", zlib.compress(raw, 9))
    out += chunk(b"IEND", b"")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(out)


def render(path: pathlib.Path, size: int, *, mark, ground=None, inset=0.72, radius=0.0):
    """Draws the mark at `size`. `ground=None` leaves the field transparent."""
    rows = []
    corner = size * radius
    for py in range(size):
        row = bytearray()
        for px in range(size):
            a = coverage(px, py, size, inset)
            if ground is None:
                row += bytes((*mark, round(255 * a)))
                continue
            # Rounded-rectangle ground, then the mark composited onto it.
            gx = min(px + 0.5, size - px - 0.5)
            gy = min(py + 0.5, size - py - 0.5)
            if corner and gx < corner and gy < corner:
                d = math.hypot(corner - gx, corner - gy)
                ga = max(0.0, min(1.0, corner - d + 0.5))
            else:
                ga = 1.0
            px_rgb = tuple(round(g * (1 - a) + m * a) for g, m in zip(ground, mark))
            row += bytes((*px_rgb, round(255 * ga)))
        rows.append(bytes(row))
    write_png(path, size, rows)
    try:
        print(f"wrote {path.relative_to(ROOT)} ({size}x{size})")
    except ValueError:
        pass  # a scratch size on the way to the .icns


def build_icns(source_sizes: dict[int, pathlib.Path]) -> None:
    """iconutil wants an .iconset directory; it is the only supported route."""
    with tempfile.TemporaryDirectory() as tmp:
        iconset = pathlib.Path(tmp) / "mimir.iconset"
        iconset.mkdir()
        for size, path in source_sizes.items():
            scale = "" if size <= 512 else "@2x"
            base = size if not scale else size // 2
            (iconset / f"icon_{base}x{base}{scale}.png").write_bytes(path.read_bytes())
        out = ICONS / "icon.icns"
        subprocess.run(["iconutil", "-c", "icns", str(iconset), "-o", str(out)], check=True)
        print(f"wrote {out.relative_to(ROOT)}")


def main() -> None:
    # The menu bar: alpha only, drawn black, recoloured by macOS for light and
    # dark bars alike. 36 px so it is crisp on a Retina 22 pt bar.
    render(ICONS / "mimirTemplate.png", 36, mark=(0, 0, 0), inset=0.86)

    # The app icon: Mist mark on a Carbon rounded square, the "symbol only"
    # lockup on the brand's primary ground.
    with tempfile.TemporaryDirectory() as tmp:
        sizes = {}
        for size in (16, 32, 64, 128, 256, 512, 1024):
            path = pathlib.Path(tmp) / f"{size}.png"
            render(path, size, mark=MIST, ground=CARBON, inset=0.52, radius=0.22)
            sizes[size] = path
        (ICONS / "icon.png").write_bytes(sizes[1024].read_bytes())
        print("wrote desktop/src-tauri/icons/icon.png (1024x1024)")
        (ROOT / "desktop/public").mkdir(parents=True, exist_ok=True)
        (ROOT / "desktop/public/favicon.png").write_bytes(sizes[256].read_bytes())
        print("wrote desktop/public/favicon.png (256x256)")
        build_icns({16: sizes[16], 32: sizes[32], 64: sizes[64], 128: sizes[128],
                    256: sizes[256], 512: sizes[512], 1024: sizes[1024]})


if __name__ == "__main__":
    main()
