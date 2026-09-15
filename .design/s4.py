# -*- coding: utf-8 -*-
from build import *
import s2
header = s2.header

def section(title, hint, inner, aside=""):
    return card(
      ('<div style="display:flex;align-items:start;justify-content:space-between;gap:16px;'
       'padding:16px 20px 12px">'
       '<div style="min-width:0"><h2 style="margin:0;font-size:15px;line-height:1.4;font-weight:600">{t}</h2>'
       '<p style="margin:4px 0 0;font-size:12px;color:{muted};line-height:1.5;max-width:74ch">{h}</p></div>'
       '<div style="display:flex;align-items:center;gap:8px;flex:none">{a}</div></div>'
       '<div style="padding:0 20px 16px">{i}</div>').format(t=title, h=hint, i=inner, a=aside, **T))

def maprow(col, field, ok=True):
    return ('<div style="display:grid;grid-template-columns:minmax(0,1fr) 24px minmax(0,1fr);'
            'align-items:center;gap:12px;padding:8px 0;border-top:1px solid {edge}">'
            '<span class="mono" style="font-size:12px;color:{muted};overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{c}</span>'
            '<span style="color:{edge2};text-align:center">{ar}</span>'
            '<div style="display:flex;align-items:center;gap:8px;height:32px;padding:0 10px;border-radius:8px;'
            'border:1px solid {bc};background:{sunken};box-sizing:border-box">'
            '<span style="flex:1;min-width:0;font-size:12px;color:{fc};overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{f}</span>{ch}</div>'
            '</div>').format(c=col, f=field, ar="→", ch=icon(ICONS["chev"], 13),
                             bc=T["edge"] if ok else T["bad"] + "80",
                             fc=T["text"] if ok else T["muted"] + "99", **T)

mapping = ('<div style="display:grid;grid-template-columns:1fr 1fr;gap:0 28px">'
           '<div>{a}</div><div>{b}</div></div>').format(
  a="".join([maprow("İsim", "Başlık"), maprow("Açıklama", "Açıklama (HTML)"),
             maprow("Meta Başlığı", "SEO başlık"), maprow("Meta Açıklaması", "SEO açıklama")]),
  b="".join([maprow("Etiketler", "Etiketler"), maprow("Kategori", "Kategori"),
             maprow("Çevrilecek Açıklama", "Açıklama · Arapça"),
             maprow("SKU", "eşlenmedi — sütun boş", ok=False)]))

def sw(label, on=True, note=""):
    knob = ('<span style="display:block;width:14px;height:14px;border-radius:999px;background:{k};'
            'margin-left:{m}"></span>').format(k=T["ink"] if on else T["muted"], m="14px" if on else "2px")
    return ('<div style="display:flex;align-items:center;gap:10px;padding:9px 0;border-top:1px solid {edge}">'
            '<span style="display:flex;align-items:center;width:32px;height:18px;border-radius:999px;'
            'background:{bg};flex:none">{k}</span>'
            '<span style="font-size:13px;color:{tc}">{l}</span>'
            '<span style="margin-left:auto;font-size:11px;color:{muted}">{n}</span></div>'
           ).format(k=knob, bg=T["lime"] if on else T["raised"], l=label, n=note,
                    tc=T["text"] if on else T["muted"], **T)

fields_grid = ('<div style="display:grid;grid-template-columns:1fr 1fr;gap:0 28px">'
               '<div><p class="lbl" style="margin:0 0 4px">Türkçe</p>{a}</div>'
               '<div><p class="lbl" style="margin:0 0 4px">Arapça · MSA</p>{b}</div></div>').format(
  a=sw("Açıklama") + sw("SEO başlık") + sw("SEO açıklama") + sw("Etiketler") + sw("Başlık", on=False, note="elle yazılıyor"),
  b=sw("Açıklama") + sw("SEO başlık", on=False, note="sütun yok") + sw("SEO açıklama", on=False, note="sütun yok")
    + sw("Etiketler", on=False, note="sütun yok"))

def voice(label, value):
    return ('<div style="display:flex;flex-direction:column;gap:6px">'
            '<span class="lbl">{l}</span>'
            '<div style="border-radius:10px;border:1px solid {edge};background:{sunken};padding:9px 12px;'
            'font-size:12px;line-height:1.6;color:{muted}">{v}</div></div>').format(l=label, v=value, **T)

def vocabchip(t):
    return ('<span class="mono" style="display:inline-flex;align-items:center;height:24px;padding:0 8px;'
            'border-radius:6px;background:{raised};font-size:11px;color:{text}">{t}</span>').format(t=t, **T)

brand = ('<div style="display:flex;flex-direction:column;gap:12px">'
         '<div style="display:grid;grid-template-columns:1fr 1fr;gap:16px">{a}{b}</div>'
         '<div style="display:grid;grid-template-columns:1fr 1fr;gap:16px">'
         '<div style="display:flex;flex-direction:column;gap:7px">'
         '<span class="lbl">dosyadan okunan biçimlendirme</span>'
         '<div style="display:flex;flex-wrap:wrap;gap:6px">{c}</div></div>'
         '<div style="display:flex;flex-direction:column;gap:7px">'
         '<span class="lbl">yasaklı</span>'
         '<div style="display:flex;flex-wrap:wrap;gap:6px">{d}</div></div></div></div>').format(
  a=voice("hitap", "siz — mağazanın 1 013 açıklamasının 968&#39;inde"),
  b=voice("ton", "ölçülü, klinik, abartısız; iddiayı kaynağa bağlar"),
  c="".join(vocabchip(x) for x in ["&lt;h2&gt;", "&lt;h3&gt;", "&lt;p&gt;", "&lt;ul&gt;/&lt;li&gt;", "&lt;strong&gt;", "&lt;div class=&quot;ms-desc&quot;&gt;"]),
  d="".join(vocabchip(x) for x in ["mucize", "%100 garanti", "en iyi", "kesin sonuç"]))

body = sidebar("Katalog · Ürün içeriği") + (
  '<main style="flex:1;min-width:0;display:flex;flex-direction:column;min-height:0">'
  + header("Kurulum", btn("Ürünlere dön", "ghost", "back"))
  + '<div style="flex:1;min-height:0;display:flex;flex-direction:column;gap:12px;padding:14px 28px 18px;overflow:hidden">'
  + section("Sütun eşlemesi",
            "Profil eşleşti, ama bir eşleşme başkasının export&#39;u hakkında bir tahmindir. Yazılacak her alanın bu dosyada bir sütunu olmalı.",
            mapping, badge("7 / 8 eşlendi", "accent"))
  + section("Yazılacak alanlar",
            "Kapalı bir alan, kart onu istese bile yazılmaz. Sütunu olmayan alan kapalı olarak değil, hiç gösterilmez.",
            fields_grid, badge("Türkçe 4 · Arapça 1", "muted"))
  + section("Marka kimliği",
            "Mağazanın kendi geçmiş HTML&#39;inden okundu; her yeniden yazım buna göre ölçülür.",
            brand, btn("yeniden tara", "quiet", "pulse"))
  + '</div></main>')

# The setup screen is taller than a window and scrolls — the artboard shows
# all of it rather than cropping the last section.
import re as _re, io as _io
_p = "Kurulum.dc.html"
write(_p, body)
_s = _io.open(_p, encoding="utf-8").read().replace("height:900px", "height:1000px", 1)
_io.open(_p, "w", encoding="utf-8").write(_s)
