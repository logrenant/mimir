# -*- coding: utf-8 -*-
from build import *

def cat_row(name, meta, profile, count, state, tone, when, on=False):
    return ('<div style="display:grid;grid-template-columns:minmax(0,1fr) 128px 80px 168px 112px;'
            'align-items:center;gap:16px;padding:13px 14px;border-radius:10px;'
            'border-left:2px solid {bl};background:{bg}">'
            '<div style="min-width:0;display:flex;flex-direction:column;gap:2px">'
            '<span style="font-size:15px;line-height:1.4;font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{n}</span>'
            '<span class="mono" style="font-size:11px;color:{muted}">{p}</span></div>'
            '<span style="color:{muted};font-size:12px">{pr}</span>'
            '<span class="mono" style="font-size:13px">{c}</span>'
            '<div>{st}</div>'
            '<span style="font-size:12px;color:{muted};text-align:right">{w}</span>'
            '</div>').format(n=name, p=meta, pr=profile, c=count, st=badge(state, tone), w=when,
                             bg=T["raised"] if on else "transparent",
                             bl=T["lime"] if on else "transparent", **T)

head = ('<div style="display:grid;grid-template-columns:minmax(0,1fr) 128px 80px 168px 112px;gap:16px;'
        'padding:0 14px 10px;border-bottom:1px solid {edge}">'
        '<span class="lbl">dosya</span><span class="lbl">profil</span>'
        '<span class="lbl">ürün</span><span class="lbl">durum</span>'
        '<span class="lbl" style="text-align:right">son geçiş</span></div>').format(**T)

rows = "".join([
  cat_row("ikas-urunler.csv", "utf-8 · virgül · CRLF · 118 KB", "IKAS", "51", "1 taslak · 50 bekliyor", "accent", "2 sa önce", on=True),
  cat_row("ceviriler.csv", "utf-8 · virgül · LF · 2,4 MB", "IKAS · geniş", "1013", "hepsi bekliyor", "muted", "dün"),
  cat_row("varyant-ozel-alanlar.csv", "utf-8 · noktalı virgül · CRLF · 402 KB", "IKAS · varyant", "184", "184 onaylandı", "ok", "6 gün önce"),
  cat_row("shopify-export.csv", "utf-8 · virgül · LF · 91 KB", "Shopify", "76", "12 başarısız", "bad", "3 hafta önce"),
])

drop = ('<div style="display:flex;flex-direction:column;align-items:center;justify-content:center;gap:9px;'
        'border:1px dashed {edge2};border-radius:14px;padding:24px;background:{panel};flex:none">'
        '<span style="font-size:13px">Ürün CSV&#39;sini buraya bırakın</span>'
        '<span style="font-size:12px;color:{muted};max-width:56ch;text-align:center;line-height:1.5">'
        'Shopify ya da IKAS export&#39;u. Dosya olduğu gibi okunur — kodlama, ayraç ve tırnaklama korunur.</span>'
        '<div style="margin-top:2px">{b}</div></div>').format(b=btn("dosya seç", "ghost", "up"), **T)

body = sidebar("Katalog · Ürün içeriği") + (
  '<main style="flex:1;min-width:0;display:flex;flex-direction:column;min-height:0">'
  + masthead("Kataloglar", "4 dosya · 1 324 ürün", btn("CSV yükle", "primary", "up"))
  + '<div style="flex:1;min-height:0;display:flex;flex-direction:column;gap:14px;padding:4px 28px 24px">'
  + drop
  + card('<div style="display:flex;flex-direction:column;gap:2px">' + head + rows + '</div>',
         pad="16px 8px 12px")
  + '</div></main>')

write("Kataloglar.dc.html", body)
