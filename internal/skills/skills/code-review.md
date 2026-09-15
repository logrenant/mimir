# Kod incelemesi

Bir diff'i, bir dosyayı ya da bir değişikliği incelerken bu sırayı izle.

## Önce deponun kendi kuralları

İncelemeye başlamadan önce oku: kökteki `AGENTS.md`, dokunulan her dizindeki en
yakın `AGENTS.md`, ve varsa `docs/AGENT_RULES.md`'deki Strict Directive'ler.
Bir bulgu bir kurala dayanıyorsa kuralı numarasıyla an (örn. "SD-3'ü çiğniyor").
Depo kuralı ile genel iyi uygulama çelişirse **depo kuralı kazanır**.

## Sıra

1. **Doğruluk.** Bu kod ne zaman yanlış cevap verir? Her bulgu için somut bir
   senaryo yaz: şu girdi → şu yanlış çıktı. Senaryosu olmayan bulgu bir histir,
   bulgu değil.
2. **Sınır durumları.** Boş, sıfır, nil, tek eleman, eşzamanlı iki çağrı,
   yarıda kesilen bağlam, yeniden başlatma. Hangisi denenmemiş?
3. **Sözleşme.** Dışa açık imza, JSON alanı, tablo sütunu ya da hata değeri
   değişti mi? Değiştiyse eski çağıran ne oluyor?
4. **Testler.** Değişen davranışın testi var mı? Test, değişiklik geri alınınca
   gerçekten kırılır mı?
5. **Sadeleştirme.** Aynı işi yapan mevcut bir yardımcı var mı? Yeni yazılan şey
   var olanın kopyası mı?

## Ton

Bulguları en ağırdan hafife sırala. Her bulgu tek cümlelik bir iddia, ardından
başarısızlık senaryosu. Emin olmadığını "emin değilim" diye yaz — kesin gibi değil.
Hiçbir şey bulamadıysan bunu söyle; bulgu üretmek için bulgu uydurma.
