# -*- coding: utf-8 -*-
from build import *

# --- one header where four bands used to be -------------------------------
def header(active, right=""):
    return ('<div style="display:flex;align-items:center;gap:14px;padding:16px 28px 14px;'
            'border-bottom:1px solid {edge}">'
            '<div style="display:flex;align-items:center;gap:7px;color:{muted};font-size:12px">'
            '{b}<span>kataloglar</span></div>'
            '<span style="color:{edge2}">/</span>'
            '<h1 style="margin:0;font-size:20px;line-height:1.25;font-weight:600">ikas-urunler.csv</h1>'
            '{bd}'
            '<span class="mono" style="font-size:11px;color:{muted}">51 ürün · virgül · utf-8 · CRLF</span>'
            '<div style="margin-left:auto;display:flex;align-items:center;gap:14px">{t}{r}</div>'
            '</div>').format(b=icon(ICONS["back"], 14), bd=badge("IKAS", "muted"),
                             t=tabs(active), r=right, **T)

# --- the three tabs -------------------------------------------------------
def tabs(active):
    out = []
    for name in ("Ürünler", "Kurulum", "Marka"):
        on = name == active
        s = ('background:{raised};color:{text};box-shadow:inset 0 1px 0 rgb(238 240 242 / 0.05);'.format(**T)
             if on else 'background:transparent;color:{muted};'.format(**T))
        out.append('<div style="display:inline-flex;align-items:center;height:32px;padding:0 14px;'
                   'border-radius:999px;font-size:13px;font-weight:500;{s}">{n}</div>'.format(s=s, n=name))
    return ('<div style="display:inline-flex;align-items:center;gap:4px;padding:4px;border-radius:999px;'
            'background:{sunken};outline:1px solid {edge}b3;outline-offset:0">{o}</div>'
           ).format(o="".join(out), **T)

# --- product table --------------------------------------------------------
COLS = "28px minmax(0,1fr) 280px 132px 108px"

def box(on=False):
    if on:
        return ('<span style="display:flex;align-items:center;justify-content:center;width:16px;height:16px;'
                'border-radius:5px;background:{lime}">{c}</span>').format(
                    c=icon('<polyline points="4 12 10 18 20 6"/>', 11, T["ink"]), **T)
    return ('<span style="display:block;width:16px;height:16px;border-radius:5px;'
            'border:1px solid {edge2}"></span>').format(**T)

def prow(title, handle, cat, state, tone, when, on=False, sel=False):
    return ('<div style="display:grid;grid-template-columns:{c};align-items:center;gap:16px;'
            'padding:11px 14px;border-top:1px solid {edge};border-left:2px solid {bl};background:{bg}">'
            '{box}'
            '<div style="min-width:0;display:flex;flex-direction:column;gap:2px">'
            '<span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{t}</span>'
            '<span class="mono" style="font-size:11px;color:{muted};overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{h}</span></div>'
            '<span style="font-size:12px;color:{muted};overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{cat}</span>'
            '<div>{st}</div>'
            '<span style="font-size:12px;color:{muted};text-align:right">{w}</span>'
            '</div>').format(c=COLS, t=title, h=handle, cat=cat, st=badge(state, tone), w=when,
                             box=box(sel), bg=T["raised"] if on else "transparent",
                             bl=T["lime"] if on else "transparent", **T)

thead = ('<div style="display:grid;grid-template-columns:{c};gap:16px;padding:10px 14px;'
         'background:{panel}">{box}<span class="lbl">ürün</span><span class="lbl">kategori</span>'
         '<span class="lbl">durum</span><span class="lbl" style="text-align:right">son geçiş</span></div>'
        ).format(c=COLS, box=box(False), **T)

rows = "".join([
  prow("The Mossi London Hair Loss Shampoo - Buy 2 Get 1 FREE", "the-mossi-london-hair-loss-shampoo---buy-2-get-1-free",
       "Hair Care › HAIR LOSS › Hair Loss Treatment", "bekliyor", "muted", "—", sel=True),
  prow("The Mossi London Hair Vitamin - Buy 2 Get 1 FREE", "the-mossi-london-hair-vitamin---buy-2-get-1-free",
       "Hair Care › HAIR LOSS › Hair Loss Treatment", "bekliyor", "muted", "—", sel=True),
  prow("The Mossi London 6 Months Flacon Plus Hair Set", "the-mossi-london-6-months-flacon-plus-hair-set",
       "Hair Care › HAIR LOSS › Hair Loss Treatment", "taslak", "accent", "2 sa önce", on=True, sel=True),
  prow("The Mossi London 2 Months Flacon Plus Hair Set", "the-mossi-london-2-months-flacon-plus-hair-set",
       "Hair Care › HAIR LOSS › Hair Loss Treatment", "bekliyor", "muted", "—"),
  prow("The Mossi London Hair Vitamin 120 Tablets", "the-mossi-london-hair-vitamin-120-tablets",
       "Hair Care › HAIR LOSS › Hair Loss Treatment", "bekliyor", "muted", "—"),
  prow("The Mossi London Hair Loss Therapy Serum Set 10ml x6", "the-mossi-london-hair-loss-therapy-serum-set-10ml-x6",
       "Hair Care › HAIR LOSS › Hair Loss Treatment", "bekliyor", "muted", "—"),
  prow("The Mossi London Ozonized Oil Complex 30ml", "the-mossi-london-ozonized-oil-complex-30ml",
       "Skin Care › CARE › Facial Cleansing", "bekliyor", "muted", "—"),
  prow("The Mossi London Hair Loss Shampoo 200ml", "the-mossi-london-hair-loss-shampoo-200ml",
       "Hair Care › HAIR LOSS › Hair Loss Treatment", "bekliyor", "muted", "—"),
  prow("The Mossi London Hair Repair Set", "the-mossi-london-hair-repair-set",
       "Hair Care › HAIR STYLING › Conditioner", "bekliyor", "muted", "—"),
  prow("The Mossi London Hair Repair Set - Buy 2 Get 1 FREE", "the-mossi-london-hair-repair-set---buy-2-get-1-free",
       "Hair Care › HAIR STYLING › Conditioner", "bekliyor", "muted", "—"),
])

# --- filters: chips, not a 13rem rail ------------------------------------
filters = ('<div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">'
           '{c1}{c2}{c3}{c4}{c5}'
           '<span style="width:1px;height:20px;background:{edge};margin:0 4px"></span>'
           '{sel}'
           '<div style="margin-left:auto;display:flex;align-items:center;gap:8px;height:32px;'
           'padding:0 12px;border-radius:10px;border:1px solid {edge};background:{sunken};'
           'color:{muted}99;font-size:12px;min-width:200px;box-sizing:border-box">{s}<span>ürün ara</span></div>'
           '</div>').format(
             c1=chip("hepsi", 51, on=True), c2=chip("bekliyor", 50), c3=chip("taslak", 1),
             c4=chip("onaylandı", 0), c5=chip("başarısız", 0),
             sel=('<div style="display:inline-flex;align-items:center;gap:8px;height:30px;padding:0 10px;'
                  'border-radius:999px;border:1px solid {edge};color:{muted};font-size:12px">'
                  'Hair Care › HAIR LOSS{ch}</div>').format(ch=icon(ICONS["chev"], 13), **T),
             s=icon(ICONS["search"], 14), **T)

# --- the selection bar: appears with the selection it belongs to ----------
selbar = ('<div style="display:flex;align-items:center;gap:12px;padding:10px 14px;border-radius:12px;'
          'background:{raised};box-shadow:inset 0 1px 0 rgb(238 240 242 / 0.05);'
          'border-left:2px solid {lime}">'
          '<span style="font-size:13px;font-weight:600">3 ürün seçildi</span>'
          '<span style="font-size:12px;color:{muted}">4 alan · Türkçe</span>'
          '<div style="margin-left:auto;display:flex;align-items:center;gap:10px">'
          '<span style="font-size:12px;color:{muted}">model</span>'
          '<div style="display:flex;align-items:center;gap:8px;height:32px;padding:0 12px;border-radius:10px;'
          'border:1px solid {edge};background:{sunken};box-sizing:border-box">{p}'
          '<span style="font-size:12px">Claude · Sonnet 5</span>{ch}</div>'
          '{b}</div></div>').format(p=icon(ICONS["pulse"], 14, T["lime"]), ch=icon(ICONS["chev"], 13),
                                    b=btn("yeniden yaz (3)", "primary"), **T)

body = sidebar("Katalog · Ürün içeriği") + (
  '<main style="flex:1;min-width:0;display:flex;flex-direction:column;min-height:0">'
  + header("Ürünler", btn("Dışa aktar", "ghost", "dl"))
  + '<div style="flex:1;min-height:0;display:flex;flex-direction:column;gap:12px;padding:14px 28px 20px">'
  + filters
  + selbar
  + card('<div style="display:flex;flex-direction:column">' + thead + rows + '</div>',
         pad="0", extra="flex:1;min-height:0;overflow:hidden")
  + '</div></main>')

write("Main.dc.html", body)
