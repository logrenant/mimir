# -*- coding: utf-8 -*-
from build import *
import s2  # noqa

def pane(label, meta, inner, accent=False):
    return ('<div style="display:flex;min-height:0;flex:1;flex-direction:column;gap:8px">'
            '<div style="display:flex;align-items:baseline;gap:10px;flex:none">'
            '<span class="lbl" style="color:{c}">{l}</span>'
            '<span style="font-size:11px;color:{muted}">{m}</span></div>'
            '<div style="flex:1;min-height:0;border-radius:12px;border:1px solid {edge};'
            'background:{sunken};padding:18px 20px;overflow:hidden;box-sizing:border-box">{i}</div>'
            '</div>').format(l=label, m=meta, i=inner, c=T["lime"] if accent else T["muted"], **T)

before = ('<div style="display:flex;flex-direction:column;gap:12px;color:{muted}">'
          '<p style="margin:0;font-size:15px;font-weight:700;color:{text}">Features</p>'
          '<p style="margin:0;font-size:13px;font-weight:600;color:{text}">Benefits</p>'
          '<p style="margin:0;font-size:13px;line-height:1.7">The Mossi London 6 Months Flacon Plus Hair Set or '
          'The Mossi London 6 Months Set was created with the assistance of a number of doctors and professionals '
          'in the field of treating hair problems, as well as a significant contribution from Mossi London '
          'laboratories.</p>'
          '<p style="margin:0;font-size:13px;line-height:1.7">And although The Mossi London Sets is regarded as one '
          'of the first and best products, the main difference between this series and other hair care products…</p>'
          '</div>').format(**T)

after = ('<div style="display:flex;flex-direction:column;gap:12px">'
         '<p style="margin:0;font-size:15px;font-weight:700">Genel Bakış</p>'
         '<p style="margin:0;font-size:13px;line-height:1.7;color:{muted}">The Mossi London 6 Months Flacon Plus Hair Set, '
         'saç sorunlarının tedavisi alanında çalışan doktor ve uzmanların katkısıyla ve Mossi London '
         'laboratuvarlarının desteğiyle geliştirilmiştir. Set, saç dökülmesini yavaşlatmayı ve yeni çıkan '
         'telleri güçlendirmeyi hedefler.</p>'
         '<p style="margin:0;font-size:13px;font-weight:600">Kullanım</p>'
         '<p style="margin:0;font-size:13px;line-height:1.7;color:{muted}">Haftada üç kez, nemli saç derisine '
         'uygulayın ve iki dakika masaj yapın. Altı aylık kür boyunca ara vermeden sürdürün.</p>'
         '</div>').format(**T)

def field(label, value, count=None, warn=False):
    c = ('<span class="mono" style="font-size:11px;color:{cc}">{n}</span>'.format(
            n=count, cc=T["bad"] if warn else T["muted"]) if count else '')
    return ('<div style="display:flex;flex-direction:column;gap:6px">'
            '<div style="display:flex;align-items:baseline;justify-content:space-between">'
            '<span class="lbl">{l}</span>{c}</div>'
            '<div style="border-radius:10px;border:1px solid {edge};background:{sunken};'
            'padding:9px 12px;font-size:12px;line-height:1.5;color:{text}">{v}</div></div>'
           ).format(l=label, v=value, c=c, **T)

fields = ('<div style="display:grid;grid-template-columns:1fr 1fr;gap:12px 16px">{a}{b}{c}{d}</div>').format(
    a=field("SEO başlık", "Mossi London 6 Aylık Flacon Plus Saç Seti", "48 / 60"),
    b=field("SEO açıklama", "Doktor katkısıyla geliştirilen altı aylık saç bakım kürü. Dökülmeyi yavaşlatır, yeni telleri güçlendirir.", "132 / 160"),
    c=field("Etiketler", "saç bakımı · saç dökülmesi · kür · Mossi London"),
    d=field("Kategori", "Hair Care › HAIR LOSS › Hair Loss Treatment"))

tabstrip = ('<div style="display:inline-flex;align-items:center;gap:4px;padding:4px;border-radius:999px;'
            'background:{sunken};outline:1px solid {edge}b3">{a}{b}{c}</div>').format(
    a=('<div style="display:inline-flex;align-items:center;height:32px;padding:0 14px;border-radius:999px;'
       'background:{raised};font-size:13px;font-weight:500;box-shadow:inset 0 1px 0 rgb(238 240 242 / 0.05)">Önizleme</div>').format(**T),
    b=('<div style="display:inline-flex;align-items:center;height:32px;padding:0 14px;border-radius:999px;'
       'color:{muted};font-size:13px">HTML</div>').format(**T),
    c=('<div style="display:inline-flex;align-items:center;height:32px;padding:0 14px;border-radius:999px;'
       'color:{muted};font-size:13px">Alanlar</div>').format(**T), **T)

navbtn = ('<div style="display:flex;align-items:center;gap:2px">'
          '<div style="display:flex;align-items:center;justify-content:center;width:32px;height:32px;'
          'border-radius:10px;border:1px solid {edge}">{p}</div>'
          '<div style="display:flex;align-items:center;justify-content:center;width:32px;height:32px;'
          'border-radius:10px;border:1px solid {edge}">{n}</div>'
          '<span style="margin-left:8px;font-size:12px;color:{muted}">3 / 51</span></div>'
         ).format(p=icon(ICONS["back"], 15), n=icon(ICONS["fwd"], 15), **T)

changed = ('<div style="display:flex;align-items:center;gap:10px;flex-wrap:wrap">'
           '<span class="lbl" style="color:{electric}">değişen</span>'
           '<span style="font-size:12px;color:{electric}">Açıklama · SEO başlık · SEO açıklama · Etiketler</span>'
           '<span style="width:1px;height:16px;background:{edge}"></span>'
           '<span class="mono" style="font-size:11px;color:{muted}">content-v1@claude/claude-sonnet-5 · 2 sa önce</span>'
           '</div>').format(**T)

footer = ('<div style="display:flex;align-items:center;gap:10px;flex:none;border-top:1px solid {edge};'
          'padding:14px 28px;background:{panel}">'
          '{s}{a}{r}'
          '<span style="margin-left:auto;font-size:12px;color:{muted}">Yalnız onaylananlar dışa aktarılır.</span>'
          '</div>').format(s=btn("taslağı kaydet", "secondary"), a=btn("onayla", "primary", "check"),
                           r=btn("reddet", "quiet", "x"), **T)

body = sidebar("Katalog · Ürün içeriği") + (
  '<main style="flex:1;min-width:0;display:flex;flex-direction:column;min-height:0">'
  + ('<div style="display:flex;align-items:center;gap:14px;padding:14px 28px 12px;border-bottom:1px solid {edge}">'
     '<div style="display:flex;align-items:center;gap:7px;color:{muted};font-size:12px">{b}<span>ürünler</span></div>'
     '<span style="color:{edge2}">/</span>'
     '<h1 style="margin:0;font-size:20px;line-height:1.25;font-weight:600;min-width:0;overflow:hidden;'
     'text-overflow:ellipsis;white-space:nowrap">The Mossi London 6 Months Flacon Plus Hair Set</h1>'
     '{bd}<div style="margin-left:auto;display:flex;align-items:center;gap:12px">{nav}</div></div>'
    ).format(b=icon(ICONS["back"], 14), bd=badge("taslak", "accent"), nav=navbtn, **T)
  + '<div style="flex:1;min-height:0;display:flex;flex-direction:column;gap:14px;padding:14px 28px 16px">'
  + ('<div style="display:flex;align-items:center;gap:16px;flex:none">{t}{c}</div>'
     .format(t=tabstrip, c='<div style="margin-left:auto">' + changed + '</div>'))
  + ('<div style="flex:1;min-height:0;display:flex;gap:16px">{a}{b}</div>'
     .format(a=pane("şimdiki", "dosyadaki HTML", before),
             b=pane("yeni", "taslak · elle düzenlenebilir", after, accent=True)))
  + '<div style="flex:none">' + fields + '</div>'
  + '</div>' + footer + '</main>')

write("Tezgah.dc.html", body)
