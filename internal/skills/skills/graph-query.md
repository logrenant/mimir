# Grafik sorgusu

Bilgi grafiğine soru sorarken bu disiplin zorunludur. Grafik, düğüm başlıklarını
harfi harfine eşleştirir: kök bulma yok, eşanlamlı yok, diller arası eşleşme yok.
Sorunun kelimeleri grafiğin kelimelerinden farklıysa sonuç sıfır çıkar ve cevap
gürültüye döner.

## Sıra

1. **Genişlet.** Sorguyu grafiğin **kendi kelime dağarcığından** seçilmiş en fazla
   12 token'a çevir.
2. **Uydurma.** Yalnız dağarcıkta gerçekten bulunan token seçilir. Bir kavramın
   karşılığı yoksa o kavram atlanır — hafızadan yakın bir eşanlamlı konmaz.
3. **Göster.** Seçilen token'lar cevabın içinde yazılır. Cevabın neye dayandığı
   görünmüyorsa cevap denetlenemez.
4. **Boşsa dur.** Hiçbir token eşleşmiyorsa "bu grafikte bu konuda kelime yok" de
   ve dur. Boş bir arama uydurma.
5. **Yalnız grafikte olanı söyle.** Bir olguyu anarken kaynağını (dosya ve satır)
   yaz. Grafik yetmiyorsa yetmediğini söyle; kenar icat etme.

## Yön önemlidir

"X'i kim çağırıyor" ile "X ne çağırıyor" farklı sorulardır ve farklı okumalardır.
Etki analizi (`affected`) ters yönde gezer; komşuluk okuması yönü umursamaz.
Yanlışını kullanmak doğru görünen yanlış bir cevap üretir.

## Geri besleme

Bir cevap işe yaradıysa ya da çıkmaz sokak olduysa bunu kaydet. Grafik, hangi
düğümün gerçekten cevap verdiğini ancak böyle öğrenir.
