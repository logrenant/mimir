# -*- coding: utf-8 -*-
import io, os

T = dict(
  ground="#0b0c0e", panel="#141619", raised="#1c1f24", overlay="#23262c",
  sunken="#08090a", edge="#232730", edge2="#333844", text="#eef0f2",
  muted="#8a9099", lime="#c6f04a", ink="#0b0c0e", electric="#3d63ff", bad="#ff5c5c",
)

HEAD = '''<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <script src="./support.js"></script>
</head>
<body>
<x-dc>
<helmet>
  <link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Aldrich&family=Open+Sans:wght@300..700&display=swap">
  <style>
    body {{ margin: 0; }}
    a {{ color: {lime}; text-decoration: none; }}
    a:hover {{ color: {text}; }}
    .lbl {{ font-size: 11px; line-height: 1; font-weight: 600; letter-spacing: 0.06em; color: {muted}; }}
    .mono {{ font-family: ui-monospace, "SF Mono", Menlo, monospace; }}
  </style>
</helmet>
'''.format(**T)

FOOT = '''</x-dc>
</body>
</html>
'''

ROOT = ('display:flex;width:1440px;height:900px;box-sizing:border-box;background:{ground};'
        "color:{text};font-family:'Open Sans',-apple-system,sans-serif;font-size:13px;"
        'line-height:1.55;overflow:hidden').format(**T)

def icon(d, size=15, color=None, fill="none"):
    c = color or T["muted"]
    return ('<svg width="{s}" height="{s}" viewBox="0 0 24 24" fill="{f}" stroke="{c}" '
            'stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" '
            'style="flex:none">{d}</svg>').format(s=size, c=c, d=d, f=fill)

ICONS = dict(
  grid='<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/>',
  board='<rect x="3" y="4" width="5" height="16" rx="1"/><rect x="10" y="4" width="5" height="11" rx="1"/><rect x="17" y="4" width="4" height="7" rx="1"/>',
  term='<polyline points="5 8 9 12 5 16"/><line x1="12" y1="16" x2="19" y2="16"/>',
  brain='<circle cx="7" cy="7" r="2.5"/><circle cx="17" cy="9" r="2.5"/><circle cx="10" cy="17" r="2.5"/><line x1="9" y1="8" x2="15" y2="9"/><line x1="8" y1="9" x2="10" y2="14"/>',
  sliders='<line x1="4" y1="8" x2="20" y2="8"/><line x1="4" y1="16" x2="20" y2="16"/><circle cx="9" cy="8" r="2"/><circle cx="16" cy="16" r="2"/>',
  search='<circle cx="11" cy="11" r="6"/><line x1="20" y1="20" x2="16" y2="16"/>',
  folder='<path d="M3 7a1 1 0 0 1 1-1h5l2 2h9a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z"/>',
  chev='<polyline points="6 9 12 15 18 9"/>',
  back='<polyline points="14 6 8 12 14 18"/>',
  fwd='<polyline points="10 6 16 12 10 18"/>',
  up='<line x1="12" y1="19" x2="12" y2="6"/><polyline points="6 11 12 5 18 11"/>',
  check='<polyline points="4 12 10 18 20 6"/>',
  x='<line x1="6" y1="6" x2="18" y2="18"/><line x1="18" y1="6" x2="6" y2="18"/>',
  pulse='<polyline points="3 12 7 12 10 5 14 19 17 12 21 12"/>',
  cols='<rect x="3" y="4" width="18" height="16" rx="1.5"/><line x1="9" y1="4" x2="9" y2="20"/><line x1="15" y1="4" x2="15" y2="20"/>',
  tag='<path d="M11 4H5a1 1 0 0 0-1 1v6l9 9 7-7-9-9z"/><circle cx="8" cy="8" r="1.2"/>',
  dl='<line x1="12" y1="4" x2="12" y2="15"/><polyline points="7 11 12 16 17 11"/><line x1="5" y1="20" x2="19" y2="20"/>',
)

def rail_row(label, ic, on=False, count=None):
    bar = ('<span style="position:absolute;top:6px;bottom:6px;left:0;width:3px;'
           'border-radius:999px;background:{lime}"></span>').format(**T) if on else ''
    bg = 'background:{raised};font-weight:500;color:{text};box-shadow:inset 0 1px 0 rgb(238 240 242 / 0.05);'.format(**T) if on else 'color:{muted};'.format(**T)
    cnt = ('<span class="mono" style="font-size:11px;color:{muted}">{c}</span>'.format(c=count, **T)) if count is not None else ''
    return ('<div style="position:relative;display:flex;align-items:center;gap:10px;min-height:36px;'
            'border-radius:10px;padding:6px 10px 6px 12px;{bg}">{bar}{ic}'
            '<span style="flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{l}</span>{c}</div>'
           ).format(bg=bg, bar=bar, ic=icon(ICONS[ic], color=T["lime"] if on else T["muted"]), l=label, c=cnt)

def sidebar(active):
    rows_a = [("Genel","grid"),("Board","board"),("Terminals","term"),("Brain","brain"),("Ayarlar","sliders")]
    rows_b = [("Coding runner","term"),("Lead-gen · Maps","search"),("Katalog · Ürün içeriği","folder")]
    a = "".join(rail_row(l,i,on=(l==active)) for l,i in rows_a)
    b = "".join(rail_row(l,i,on=(l==active)) for l,i in rows_b)
    return ('<aside style="width:240px;flex:none;display:flex;flex-direction:column;gap:22px;'
            'padding:14px 12px;border-right:1px solid {edge};background:{ground};box-sizing:border-box">'
            '<div style="display:flex;align-items:center;gap:8px;padding:6px 8px 2px">'
            '{star}<span style="font-family:Aldrich,&quot;Open Sans&quot;,sans-serif;font-size:15px;letter-spacing:.04em">mimir</span>'
            '<span style="font-size:11px;color:{muted}">orchestration</span></div>'
            '<nav style="display:flex;flex-direction:column;gap:2px"><p class="lbl" style="margin:0 0 6px 12px">Mimir</p>{a}</nav>'
            '<nav style="display:flex;flex-direction:column;gap:2px"><p class="lbl" style="margin:0 0 6px 12px">Modüller</p>{b}</nav>'
            '<div style="margin-top:auto;display:flex;align-items:center;gap:8px;border-top:1px solid {edge};padding:12px 8px 4px">'
            '<span style="width:20px;height:20px;border-radius:999px;background:{raised};display:block"></span>'
            '<span style="font-size:12px;color:{muted}">salihdevran9@gmail.com</span></div>'
            '</aside>'
           ).format(a=a, b=b, star=icon('<path d="M12 3v18M4.5 7.5l15 9M19.5 7.5l-15 9"/>', 16, T["lime"]), **T)

def btn(label, kind="primary", ic=None):
    base = ('display:inline-flex;align-items:center;justify-content:center;gap:8px;height:32px;'
            'padding:0 12px;border-radius:10px;font-size:13px;font-weight:500;white-space:nowrap;box-sizing:border-box;')
    if kind == "primary":
        s = base + 'background:{lime};color:{ink};border:0;box-shadow:inset 0 1px 0 rgb(238 240 242 / 0.05);'.format(**T)
    elif kind == "ghost":
        s = base + 'border:1px solid {edge};color:{text};background:transparent;'.format(**T)
    elif kind == "quiet":
        s = base + 'color:{muted};background:transparent;border:0;'.format(**T)
    else:
        s = base + 'background:{raised};color:{text};border:0;'.format(**T)
    i = icon(ICONS[ic], 15, T["ink"] if kind=="primary" else T["muted"]) if ic else ''
    return '<div style="{s}">{i}{l}</div>'.format(s=s, i=i, l=label)

def badge(label, tone="muted", solid=False):
    col = dict(muted=T["muted"], accent=T["lime"], bad=T["bad"], warn=T["electric"], ok=T["lime"])[tone]
    if solid:
        s = 'background:{c};color:{ink};outline:0;'.format(c=col, **T)
    else:
        s = 'color:{c};outline:1px solid {c}66;'.format(c=col)
    return ('<span style="display:inline-flex;align-items:center;gap:6px;padding:4px 8px;font-size:11px;'
            'line-height:1;font-weight:500;border-radius:999px;{s}">{l}</span>').format(s=s, l=label)

def chip(label, count, on=False):
    if on:
        s = 'background:{raised};color:{text};border:1px solid {edge2};'.format(**T)
        cc = T["lime"]
    else:
        s = 'background:transparent;color:{muted};border:1px solid {edge};'.format(**T)
        cc = T["muted"]
    return ('<div style="display:inline-flex;align-items:center;gap:8px;height:30px;padding:0 10px;'
            'border-radius:999px;font-size:12px;{s}">{l}'
            '<span class="mono" style="font-size:11px;color:{cc}">{c}</span></div>').format(s=s, l=label, c=count, cc=cc)

def picker(label, value, width=208, placeholder=False):
    col = T["muted"] + "99" if placeholder else T["text"]
    return ('<div style="display:flex;flex-direction:column;gap:6px;width:{w}px">'
            '<span class="lbl">{lab}</span>'
            '<div style="display:flex;align-items:center;gap:8px;height:36px;border-radius:10px;'
            'border:1px solid {edge};background:{sunken};padding:0 12px;box-sizing:border-box">'
            '<span style="flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:{col}">{v}</span>'
            '{ch}</div></div>').format(w=width, lab=label, v=value, col=col, ch=icon(ICONS["chev"], 14), **T)

def card(inner, pad="0", extra=""):
    return ('<div style="background:{panel};border-radius:14px;box-shadow:inset 0 1px 0 rgb(238 240 242 / 0.05);'
            'padding:{p};box-sizing:border-box;{e}">{i}</div>').format(p=pad, i=inner, e=extra, **T)

def masthead(title, count, aside=""):
    return ('<div style="display:flex;align-items:center;justify-content:space-between;gap:24px;'
            'padding:14px 28px 12px">'
            '<div style="display:flex;align-items:baseline;gap:12px;min-width:0">'
            "<h1 style=\"margin:0;font-family:Aldrich,'Open Sans',sans-serif;font-size:30px;line-height:1.05;"
            'font-weight:400;letter-spacing:.01em">{t}</h1>'
            '<span class="mono" style="font-size:12px;color:{muted}">{c}</span></div>'
            '<div style="display:flex;align-items:center;gap:8px">{a}</div></div>').format(t=title, c=count, a=aside, **T)

def write(name, body):
    with io.open(name, "w", encoding="utf-8") as f:
        f.write(HEAD)
        f.write('<div style="{r}">{b}</div>\n'.format(r=ROOT, b=body))
        f.write(FOOT)
    print("wrote", name)
