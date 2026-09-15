# Changelog

Bu dosya [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) biçimini,
sürüm numaraları [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
kuralını izler.

## [Yayımlanmadı]

## [2.13.0] — 2026-09-15

**Katalog: bir Shopify ya da IKAS ürün export'unu okuyup markanın kendi etiket ve
ses sözlüğünü ondan çıkaran katman, onu markanın kendi biçimlendirmesinin içinde
düzenletip dosyayı bayt bayt geri yazan ekran, ve yarıda kaldığında kaldığı
yerden süren bir toplu yeniden yazım.**

### Değiştirildi

- **Ürünler tablosu artık sabit değil, dosyanın kendi şeklini çiziyor
  (task-116).** Sütunlar JSX'e gömülüydü (`ürün · kategori · durum`), oysa her
  export farklı bir tablo demek: ekrandaki IKAS özel-alanlar dosyasının kategori
  sütunu hiç yok ve on satır boyunca `—` çiziliyordu, SKU ise vardı ama
  gösterilmiyordu. Sütunlar artık kaynak dilin `columns` haritasından türüyor —
  dosyanın taşımadığı sütun çizilmiyor, taşıdığı her dil kendi durum sütununu
  alıyor. `handle` ve `group_id` bilerek sütun değil: biri URL (başlığın altında
  okunur), diğeri varyantları gruplayan UUID.
- **Dil bir mod değil, bir sütun.** Ürünler araç çubuğundaki dil şeridi
  seçildiği dilde satırları yeniden çekiyordu; o dilde içerik yoksa `contentFor`
  bilerek boş dönüyor ve ekranda hiçbir şey değişmiyordu — şeridin operatöre
  "işlevsiz" görünmesinin sebebi buydu. Şerit kalktı: her dilin kararı aynı
  satırda kendi hücresinde duruyor (`GET /catalog/products` artık dil başına
  durum haritası taşıyor, `catalog_product_langs`'tan tek ek okuma ile) ve bir
  hücreye tıklamak tezgahı o dilde açıyor. Durum çipleri buna göre yeniden
  tanımlandı: bir satır, taşıdığı dillerden **herhangi biri** o durumdaysa
  eşleşiyor — çok dilli dosyada sayılar bu yüzden satır sayısından fazla
  toplanıyor, tek dilli dosyada davranış birebir aynı.
- **Sütun eşlemesinde dil seçimi kalktı.** Eşlenen tek bir tablo olmasına rağmen
  form dil başına sekmeleniyordu; artık bütün diller tek ekranda, her dil kendi
  başlığının altında bütün satırlarıyla. Bu ekran sıfırdan içerik üretmek için
  değil var olanı eşlemek için, ve formun POST ettiği her anahtarı çizmesi
  "çizmediğini silen kayıt" tuzağını da kapatıyor.
- **Yeniden yaz çubuğu tablonun üstünde kalıcı.** Çubuk yalnızca seçim varken
  çiziliyordu, model ayarları da onunla birlikte kayboluyordu — oysa model
  seçimi tabloyu okurken verilen bir karar. Seçim yokken sağlayıcı ve model
  seçicileri etkin (model seçmek para harcamaz), harcayan düğme kapalı ve
  yanında neden kapalı olduğu yazıyor: açıklamasız devre dışı düğme bırakılmadı.
  Sol kenardaki lime şerit yalnızca seçim varken çiziliyor.

### Eklendi

- **Marka kimliği canlı mağazadan okunuyor (task-116).** Marka sekmesine
  mağazanın adresi veriliyor, daemon ana sayfadan bir ürün sayfası bulup orayı
  ölçüyor: zemin, metin, bağlantı, vurgu ve kenar renkleri, font yığını, punto,
  satır yüksekliği, açıklama kabuğunun ölçüsü ve hizası. Ölçüm crawl4ai'nin
  kendi tarayıcısında çalışan bir probe ile alınıyor (`js_code`, cevap belge
  kökündeki bir nitelikten geri okunuyor). Kabuk markup'ı bilerek
  kopyalanmıyor: o sınıf adları temanın stylesheet'i olmadan ölü, önizleme
  çerçevesi dış stylesheet yükleyemiyor (CSP izin vermiyor, `srcdoc` onu
  devralıyor), ve bir temayı satır içine almak yüzlerce KB. Cascade'i tarayıcı
  çözüyor; bu paket CSS ayrıştırmıyor. Tarama `catalog_imports.site_json`'da
  duruyor (`0027_catalog_site.sql`).
  - **Tarama `Vocabulary`'ye dokunmuyor**: `Render`'ın izin listesi CSV'nin
    kendi HTML'inden türemeye devam ediyor — paketin ana güvencesi bu.
  - **Tarama `BrandKit.hash`'e girmiyor**: girseydi her yeniden tarama
    katalogdaki her taslağı geçersiz kılardı. İki kırmızı çizgi de testli.
- **Önizleme mağazanın kendi zemininde çiziliyor.** `previewDocument` tarama
  varsa stil bloğunu taranmış değerlerden üretiyor, tarama yoksa okunur koyu
  varsayılan aynen kalıyor. Çerçevenin `sandbox=""` kısıtı değişmedi ve taranan
  hiçbir değer stile doğrudan geçmiyor: her biri dar bir karakter listesinden ve
  `url(` / `@import` / `expression` / `javascript:` reddinden geçiyor.

### Güvenlik

- **Operatörün verdiği adrese daemon'dan istek çıkıyor, yani tarama bir SSRF
  yüzeyi (task-116).** Adres üç kapıdan geçiyor: şema izin listesi, çözülen
  IP'nin sınıflandırması (özel, loopback, link-local, multicast ve stdlib'in
  "global unicast" saydığı RFC 6598 `100.64.0.0/10` dahil), ve **getiriden
  sonra** iniş adresinin yeniden yargılanması — crawler yönlendirme takip
  ediyor, yani yalnızca yazılan adresi kontrol etmek yetmiyordu. Kök sayfa ve
  ürün sayfası aynı kapıdan geçiyor; reddedilen adres 400, okunamayan mağaza
  502. Kalan bilinen risk yazıldı: kapının çözümü ile crawler konteynerinin
  kendi çözümü arasındaki DNS rebinding bu katmanda kapatılamıyor (IP sabitlemek
  her meşru mağazada TLS SNI'yi bozar).

### Eklendi

- **Onay artık dil başına, ve dışa aktarım dosyanın taşıdığı her dili yazıyor.**
  Motor task-105'ten beri Arapça üretiyor ve task-107'den beri dil kapısından
  geçiriyordu, ama üretilen hiçbir zaman dosyaya ulaşamıyordu: `Studio.Export`
  tek bir sürüm — kaynak dilinkini — çözüyordu, çünkü onay tek bir sütunda
  duruyordu ve Türkçe kopyayı onaylamak kimsenin okumadığı bir Arapça kopyayı
  yayına göndermemeliydi. Kaynak dilin kararı olduğu yerde kaldı, hedef dillerin
  kararı kendi tablosuna taşındı (`catalog_product_langs`) — taslak anahtarının
  kendi gerekçesiyle aynı: dil bir son ektir ve yalnızca hedef için. Karar
  verilmemiş dil **bekliyor** okunuyor: satırın yokluğu cevabın kendisi, ve
  `INNER JOIN` "hiç bekleyen yok" derdi. Arapçayı onaylamak Türkçeyi
  kıpırdatmıyor, tersi de.
- **`GET /catalog/outputs` — yazılmış her taslak, bütün dosyaların üstünden.**
  Üretilmiş içerik yalnızca tek ürün tezgâhında, geldiği dosyanın ekranında
  okunabiliyordu; "Arapçada beni ne bekliyor" sorusu dosyaları tek tek açıp
  saymakla cevaplanıyordu. Dile, dosya profiline ve duruma göre süzülüyor —
  profil dosyanın özelliği olduğu için bu süzgeç ancak burada anlam kazanıyor.
  Bir taslak tek bir (dosya, dil) çifti için güncel ve sürüm satırdan
  türetilemiyor, bu yüzden anahtarları stüdyo besteliyor ve depo onları satır
  içi bir tablo olarak join ediyor; kısıt **çift** üzerinde, çünkü aynı sürüm
  bir dosya için güncel, başka bir dosya için bayat olabiliyor. Katalog ne kadar
  büyürse büyüsün tam iki depo okuması.
- **Masaüstünde Kataloglar'ın yanında Çıktılar sekmesi.** Satır çıktının kendi
  dilinde ve kendi yönünde yazıyor — Arapça satır sağdan sola akıyor —, hangi
  dosyadan geldiğini ve o dosyanın profilini söylüyor, neyin değiştiğini
  adlandırıyor, ve tıklandığında o dosyayı o dilde ve o üründe açıyor.

### Düzeltildi

- **Operatörün elle düzenlediği Arapça taslak yönünü sessizce kaybediyordu.**
  `stripDirectionWrapper`'ın üretimde hiçbir çağıranı yoktu: `SaveDraft`
  `Render` çağırıyordu, `RenderLang` değil, yani `<div dir="rtl" lang="ar">`
  zarfa ayrışıyor ve `sanitizeAttrs` `dir`'i düşürüyordu. Hiçbir şey hata
  vermiyordu; metin yalnızca soldan sağa kaydediliyordu.
- **Dil kapısı operatörün yolundan hiç geçmiyordu**, `rewrite.go`'nun kendi
  yorumu "bu yolda ve operatörün yolunda çalışır, yani kapıyı atlamış bir
  taslağı saklamanın yolu yoktur" dediği hâlde. Artık geçiyor, ve bulguları
  **not** olarak ekleniyor: bir insanın Arapçasını oran sezgisi beğenmedi diye
  reddetmek, kaydet düğmesini sessizce çalışmayan bir düğme yapardı.
- **Arapça geçişte başarısız olan ürün, Türkçe ürünü başarısız işaretliyordu.**
  `rewriteOne` durumu dil bilmeden yazıyordu; bir dil kapısı reddi verilmiş bir
  onayı siliyor, `reason`'ını başka bir dil hakkındaki cümleyle eziyor ve ürünü
  operatörün süzgecinden çıkarıyordu.
- **"Bu 'Çevrilecek …' sütunları hangi dile ait?" sorusu sorulamıyordu.** Ekranı
  çizen koşul `pending_target` alanıydı ve daemon o alanı hiç göndermiyordu, yani
  1013 satırlık gerçek IKAS çeviri export'unda hedef dil hiç adlandırılamıyor,
  dolayısıyla hiçbir çeviri okunamıyordu.
- **Eşleme formu, dosyanın zaten çözdüğü dillerden başkasını sunmuyordu** —
  kendi doc yorumu "bu build'in yazabildiği her dil" dediği hâlde. Mağazanın
  kendi açtığı `Html:Detay-EN` sütunu bu yüzden hiçbir zaman eşlenemiyordu.
- **Ürün tablosu ve tezgâh, Arapça sekmesinde Türkçe başlığı yazıyordu**, arama
  da Türkçe başlıkta arıyordu: Arapçayı incelemek için açılan ekranda hiç Arapça
  görünmüyordu.
- Tek dilli bir dosyada dil şeridi hiç çizilmiyordu, yani operatör modülün başka
  bir dil yazabildiğini ve sütunun kendisine ait olduğunu hiç öğrenmiyordu.

- **Katalog artık tek ekran değil: Kataloglar → Ürünler / Kurulum / Marka →
  Ürün tezgâhı.** Modülün yapabildiği her şey tek pencereyi paylaşıyordu —
  önünde dosya listesi, üstünde sütun eşlemesi ve alan anahtarları, başlığına
  katlanmış marka kimliği, solda durum rayı, ortada tablo, sağda 26rem'lik
  önce/sonra sütunu; hepsi altı üst üste şeridin altında. Operatörün deyimiyle
  "boğucu"ydu, ve haklıydı: ekran dört farklı ritimde yapılan dört işi birden
  taşıyordu ve her biri diğerinden yer alıyordu. Bölme veriyi değil ritmi
  izliyor — kurulum dosya başına bir kez, inceleme ürün başına bir kez, tablo
  her gün. Dört üst şerit (masthead, breadcrumb, tam genişlik profil seçici,
  meta + dört düğme) tek başlık oldu; durum rayı tek satır çipe indi ve tablo
  tam genişliğe çıktı; model seçici kalıcı mobilya olmaktan çıkıp seçim
  çubuğuyla birlikte, harcayacağı seçimin yanında beliriyor; detay paneli
  yerine satır tıklaması tezgâhı açıyor — önce ve sonra eşit genişlikte, üstte
  `3 / 51` ve ileri-geri, yani inceleme tabloya dönüp gelmek yerine bir akış.
- **Kataloglar listesi hangi dosyayı açacağını söylüyor.** `GET /catalog/imports`
  artık her satırda o dosyanın durum sayımlarını taşıyor ("50 bekliyor ·
  1 taslak", başarısız varsa önce o) ve eşleşen profili. Sayımlar bütün liste
  için tek bir `GROUP BY` — satır başına okuma, task-80'in panodan kaldırdığı
  1+N yayılımıdır ve liste anket ediliyor. Dosya gövdesi listeye hâlâ
  gelmiyor: bir listeyi açmak her import'u birden açmak kadar pahalı olamaz,
  o yüzden çerçeve (kodlama, ayraç, satır sonu) dosyanın kendi ekranında
  kalıyor. Sayımı okuyamayan bir depo satırı özetsiz bırakıyor, ekranı
  düşürmüyor (SD-6). Son geçiş zamanı ise panodan okunuyor — uygulamanın zaten
  anket ettiği kartlardan, ikinci bir istekten değil.
- **Modeli karta yazan seçici: bir katalog pası artık kendi modelini taşıyor.**
  Operatör modeli değiştiriyor, pas aynı modelle devam ediyordu — kartın
  üzerindeki tek model kontrolü *kodlama* modelini düzenliyordu ve hiçbir
  katalog pası onu harcamıyor. Pas ise her çağrıda ayar dosyasını yeniden
  okuyordu: pas sürerken yapılan bir ayar değişikliği onu **ortasından**
  taşıyabiliyordu (şema kapısı eski değerle geçip ilk taslak çağrısı yenisine
  düşünce kart, "bu sağlayıcı yapılandırılmış çıktı veremiyor" duvarına ürün
  ürün çarpıyor). Artık seçim `POST /catalog/rewrite` ile kartın `params`'ına
  yazılıyor ve pas onu okuyor; boş bırakmak eskisi gibi "ayarlardaki model"
  demek, ama kartın adlandırdığı çift, kart yarın yeniden çalıştırıldığında da
  o çift. Şema kapısı da artık kartın kendi sağlayıcısına soruluyor — yoksa
  operatörün o duvarı aşmak için yaptığı seçim, aynı duvar tarafından
  reddediliyordu. Çift, her per-run seçimin geçtiği izin listesinden geçiyor
  (iki ad da bir alt sürece argv olarak gidiyor) ve dispatch'ten önce bir kez
  daha: kartın params'ı, onu yazan isteği aşan bir satır.
- **Katalog ekranında model seçici ve başarısız pası seçili modelle yeniden
  çalıştırma.** Seçim, harcayan düğmenin yanında: "varsayılan olarak kim
  hizmet veriyor" ile "bu kataloğu kim yazıyor" iki ayrı soru, ve operatör
  ikincisini yanıtlamak için birincisini — daemon'ın yaptığı her iş için —
  değiştirmek zorundaydı. Başarısız satırdaki düğme kartı seçili modele
  yönlendirip yeniden kuyruğa alıyor, yani iki yüz ürünü yeniden işaretlemek
  gerekmiyor. Taslak önbelleği modele göre anahtarlandığı için model değişimi
  önbelleği geçersiz kılar; bu, değişikliğin yapıldığı yerde yazıyor.
- **Pano kartı, kartın gerçekten harcadığı kontrolü sunuyor.** Katalog kartı
  sağlayıcı/model seçicisi alıyor (ve kaydı hem `params`'a hem sütuna yazıyor),
  lead-gen kartı kararın nerede olduğunu söyleyen bir cümle, claude oturumu
  eskisi gibi kodlama modeli listesi. Worker şeridindeki bir kart artık kodlama
  modeli listesine zorlanmıyor: `claude-sonnet-5` sütuna yazılıyordu ve her
  ekran kartın harcamayacağı bir modeli rapor ediyordu. Terminalin ilk satırı
  da (`run started · …`) pasın gerçekten harcayacağı modeli anıyor.
- **Dört gerçek IKAS dışa aktarımı, iki yeni profil ve dili sorulan çeviri
  sütunları (task-111).** Operatörün kendi dosyaları geldi. **Çeviriler**
  dışa aktarımı beklenen uzun format değil, geniş format çıktı — `İsim, Açıklama,
  Meta Başlığı, …` yanında `Çevrilecek …` sütunları, 1013 ürün, 1009'u dolu — ve
  ek koda ihtiyaç duymadı. Ama dosya **hangi dile** çevrildiğini yazmıyor:
  operatör onu ikas panelinde dışa aktarırken seçmiş. İçerikten tahmin etmek bu
  pakette olmayan ve olmaması gereken bir dil algılayıcısı olurdu, ve yanlış
  tahmin Arapçayı Almanca sütununa yazar — bin satır boyunca, sessizce. Bu
  yüzden soruluyor. Varyant düzeyinde özel alanlar da ayrı bir profil oldu;
  imzası ürün düzeyindekini kapsadığı için tablonun **önüne** kondu.
- **Hangi alanların yeniden yazılacağı artık yapılandırılabilir ve kaydediliyor
  (task-111).** Bir ürün export'unun sabit bir alan kümesi yok: bu mağazanın
  dosyasında `SKU` tamamen boş, `Satış Kanalı:meletiorient` mağazaya özel,
  `Html:Detay` ve `Html:Detay-AR` sütunları var ama hiç dolu değil. Bu yüzden
  açılabilecek alanlar **dosyadan** okunuyor — sütunu olmayan alan kapalı olarak
  değil, hiç gösterilmiyor. Yapılandırma bir varsayılan değil **kapı**: kart o
  alanı istese bile kapalıysa yazılmıyor ve pas neyi atladığını söylüyor. Boş bir
  küme "hiçbiri" değil "yapılandırılmadı" demek — yani hepsi — o yüzden boşa
  normalleşen bir seçim kaydedilmiyor, saklansaydı operatörün az önce kapattığı
  her anahtarı geri açardı.
- **Katalog ekranında dil sekmeleri, RTL düzenleme ve preset seçici
  (task-102, task-104).** Platform artık bir rozet değil, seçilebilir bir
  liste — ve liste ekranın kendi kopyası değil, daemon'ın. Ekran profil adlarını
  kendi sabitinde tutuyordu ve o kopya bayatlamıştı. Dil sekmeleri yalnızca
  dosya birden fazla dil taşıyorsa çiziliyor, yani tek dilli bir katalog bugüne
  kadar göründüğü gibi görünüyor. Arapça sekmesinde önizleme ve editör sağdan
  sola çalışıyor — yön yazma yüzeyinde, araç çubuğunda değil — ve "şimdiki"
  değer Türkçe gövde değil, o dilin kendi hücresi: aksi hâlde "zaten çevrilmiş"
  ile "henüz çevrilmemiş" aynı görünürdü. Sütun formu, profilin adlandırmadığı
  bir dil sütununu (mağazanın kendi açtığı `Html:Detay-EN` gibi) seçtirebiliyor.
- **İngilizce ve Arapça ürün içeriği (task-105).** Yeniden yazım artık bir hedef
  dil alıyor. Arapça kayıt Modern Standart Arapça (الفصحى), Körfez e-ticaretinde
  yazıldığı biçimiyle: Arapça noktalama (`، ؛ ؟`), Batı rakamı, kaşide ve hareke
  yok, marka/model/birim Latin harfle. İngilizce Amerikan yazımı. Markanın hitap
  ekseni (`siz`/`sen`/`yok`) Türkçe metinden okunduğu için Türkçe saklanıyor ve
  hedef dile o dilin kendi ekseniyle çevriliyor — ikinci bir enum türetmek
  `BrandKit.hash`'i değiştirir ve kataloğun tamamını çöpe atardı. Kaynak dil
  promptu **byte-identical** kaldı, golden dosyayla çivili: değişseydi
  `content-v1` yükseltilmek zorunda kalır ve operatörün onayladığı her taslak
  bulunamaz hâle gelirdi.
- **Arapça gövde yönüyle birlikte yazılıyor (task-105).** `Render` markanın
  sözlüğünde olmayan her niteliği düşürüyor ve Türkçe bir mağazanın geçmiş
  HTML'inde `dir` yok — yani `dir="rtl"` **sessizce** düşer, hiçbir şey hata
  vermez, dosya dışa aktarılır ve mağazada noktalama yanlış tarafta çıkardı.
  Yönü artık dil belirliyor: `RenderLang` tek bir `<div dir="rtl" lang="ar">`
  yazıyor, sözlük genişletilmiyor, ve sarmalayıcı yeniden kaydetmede iç içe
  geçmiyor (markanın kendi HTML'inde `div` olduğunda her kayıtta bir kat daha
  derine inerdi).
- **Deterministik dil kapısı ve anadil gözden geçirmesi (task-107).** Depodaki
  ilk doğrulayıcı paso, ve neyi koruduğu için burada: Türkçe bir yeniden yazım
  yanlış giderse ekrana bakan operatör yakalar, Arapça yanlış giderse bir
  müşteri okuyana kadar kimse yakalamaz. Önce model çağırmayan bir kapı —
  Türkçe harf sızıntısı, dil dışı harf oranı, çevrilmemiş uzun Latin bölüm,
  yanlış yazı sistemi ve **kaynakta geçmeyen sayı** reddediliyor; kaşide, bidi
  denetim karakteri, Arap-Hint rakamı ve Arapça cümledeki ASCII virgül sessizce
  değil, söylenerek onarılıyor. Sonra ikinci bir model çağrısı: anadili hedef
  dil olan bir editör, yalnızca metni ve makine kapısının bulgularını görüyor.
  Çıktı tekrar kapıdan geçiyor. Gözden geçirene erişilemezse temiz metin notla
  saklanıyor, kapının reddettiği metin saklanmıyor — deterministik kapı taban ve
  taban esnemiyor.
- **Katalog artık çok dilli okuyor ve çok dilli yazıyor (task-103).** Gerçek IKAS
  özel alanlar dışa aktarımının başlığında `Html:Detay`'ın yanında
  `Html:Detay-AR` duruyor — mağaza Arapça gövdeyi zaten orada tutuyor ve motor
  onu ne okuyordu ne yazıyordu. `Lang` ayrı bir boyut olarak eklendi: `Field`
  kapalı kümesi ve tel yazımı değişmedi, `Dialect` ve `File` dil başına sütun
  haritası kazandı, `Product` dosyanın her dilde zaten söylediğini taşıyor.
  Dışa aktarım her dili **tek geçişte** yazıyor ve değişiklik testini o dilin
  kendi eskisine karşı yapıyor — dosya tek dosya, iki ayrı export birbirinin
  işini geri alırdı. Bir dili onaylamak diğerini yayına göndermiyor. Beş gerçek
  fixture üzerindeki byte-identity iddiası bozulmadan duruyor.
- **Bir profile sonradan eklenen sütun, elde olan içe aktarımlara da ulaşıyor
  (task-103).** `stored.go`'nun kendi yorumu "sonradan eklenen bir profil ondan
  önce içe aktarılmış dosyalara da uygulanır" diyordu ama koşulu bunu yalnızca
  *hiçbir profile uymamış* dosyalar için yapıyordu. Zaten uymuş bir içe aktarım
  bağlandığı sütunları sonsuza kadar koruyordu, dolayısıyla `Html:Detay-AR`
  kimsenin kataloğuna ulaşmazdı.
- **Platform preset'i artık seçilebiliyor (task-101).** `Detect` "bu dosyayı
  hangi platform yazdı" sorusunu cevaplıyor, "bu mağaza hangi platformda"
  sorusunu değil. Bir sütununu yeniden adlandırmış mağazanın dosyası, operatör
  onun bir IKAS export'u olduğunu görebilirken tanınmıyor ve tek çıkış yolu
  profil tablosunda zaten duran bir haritayı elle yeniden yazmak oluyordu.
  `PUT /catalog/imports/{id}/dialect` algılamayı eziyor, boş anahtar dosyayı
  algılamaya geri döndürüyor. Profil, `Detect`'in yaptığı gibi **dosyanın kendi
  başlık yazımına** bağlanıyor: tablodaki yazımı yazmak, dosyada hiç olmayan bir
  sütun adını dışa aktarmak ve operatörün panelinin dosyayı reddetmesi demekti.
- **`GET /catalog/profiles` (task-101).** `catalog.Dialects()` task-85'te ihraç
  edilmiş ve bugüne kadar hiç çağrılmamıştı; masaüstü profil adlarını kendi
  sabitine kopyalamıştı ve o kopya bayatladı — task-91'de silinen `ikas-en`
  ekranda kalmıştı. Go'da yaşayan kapalı bir küme artık Go'dan cevaplanıyor.

- **Sağlayıcı kataloğu ve gerçek bir genişleme noktası (task-99).** On üç
  sağlayıcı tanınıyor: dördü çalışıyor, dokuzu **yakında**. Bir katalog satırı
  hiçbir şeyi çalıştırmıyor — ürünün ne olacağını söylüyor. `POST
  /llm/connections` final gövdesiyle var ve hazır olmayanı adıyla reddediyor
  (501); bir adaptör indiğinde rota, gövde ve ekran değişmeden başarılı olmaya
  başlıyor. Sınanamayan hiçbir adaptör yazılmadı: belgeden yazılıp hiç
  çalıştırılmamış bir adaptör ilk gerçek çağrıda düşer ya da — daha kötüsü —
  yarı çalışıp şema istenen yerde düzyazı döndürür.
- **Sır kasası (`internal/secrets`).** OS deposu — macOS Keychain, Windows
  DPAPI, Linux Secret Service — ve o yoksa `0600` bir dosya. Asıl tasarım kararı
  `Backend()`'in arayüzün parçası olması: **yedeğin kendisi güvenlik zaafı
  değil, onu gizlemek zaafiyet.** Anahtarı dosyaya düşen operatör bunu eklerken
  okuyor.

- **Bağlantı yönlendirmenin birimi oldu (task-97, task-100).** Bir sağlayıcının
  kimliği artık bir CLI ikilisinin adı değil. Ölçüldü: Antigravity'nin kendi
  aboneliği yok, bir Google AI planına biniyor; ve Gemini CLI'ın kota havuzunu
  CLI değil **oturum yöntemi** belirliyor (kişisel OAuth → Code Assist, AI Pro
  ile artan; AI Studio anahtarı → ayrı havuz; Vertex ve Workspace ayrı). Yani
  aynı Google hesabıyla girilmiş `agy` ile `gemini` **tek cüzdan** harcıyor.
  Ayarlar ekranı artık bunu satıcıya göre gruplayıp bir cümleyle söylüyor —
  onları bağımsız çizmek, operatöre tek bütçesi varken iki bütçesi olduğunu
  söylemekti.
- **Ayarlar ekranı bölüm rayına ayrıldı**: Bağlantılar · Yönlendirme ·
  Skill'ler · Kurallar. Ray, kaydedilmemiş değişiklik taşıyan bölümü
  işaretliyor.

- **Ayarlarda daemon'ın kendi çağrıları için sağlayıcı seçimi (task-98).**
  Brain'in tarama ve ilişki pasoları, refine ve katalog yeniden yazımı ilk kez
  sabit bir tiere çakılı değil — bir makine taraması binlerce çağrı, ve bundan
  önce onları başka yere yöneltmenin tek yolu bir sabiti düzenleyip yeniden
  derlemekti. Seçici artık bu makineyi biliyor: kurulu olmayan sağlayıcı
  seçilemiyor, oturumu kapalı olan CLI'ın kendi cümlesiyle işaretleniyor, ve
  Ollama'nın modelleri sabit bir listeden değil makinenin kendisinden geliyor.
- **Yerel model çalıştırıcısı bir sağlayıcı (`ollama`).** Ücretsiz, oturumsuz,
  tamamen yerel — operatörün iki kez düşüneceği iş için doğru cevap. Modelleri
  `ollama list` ile keşfediliyor ve `ollama show`'un yetenek bloğuna göre
  süzülüyor: gömme modeli listeden adı öyle göründüğü için değil, öyle olduğunu
  söylediği için çıkıyor.

- **Menü çubuğu paneli bir sohbet (task-96).** Besteci bir formdu ve sorunu
  yerleşimi değil sırasıydı: klasör ve model, iş yazılmadan önce sorulan iki
  soruydu — oysa ikisinin de cevabı çoğu zaman işin kendisinden çıkıyor.
  Sihirbaz önce işi soruyor, klasörü cümleden okuyor, ve yalnız gerçekten
  bilemediğini soruyor. Çıkarım deterministik, model çağrısız, ve **her zaman
  görünür**: hangi klasörü, hangi kelimeden seçtiğini söylüyor. Eşleştirme
  Türkçeye göre: `İçerik` eşleşiyor, `api'de` eşleşiyor, ama `test` adlı bir
  proje "testleri düzelt" cümlesine takılmıyor. Başlatmadan önce tek bir cümle
  ne harcanacağını söylüyor — ajan, klasör, model.

- **Sağlayıcı seçimi brain'e kadar iniyor, ve sağlayıcıların birbirinin yerine
  geçemediği tipte yazılı (task-93).** `Provider` artık ne yapabildiğini
  söylüyor: `agy` şema alabiliyor, `gemini` alamıyor (kurulu ikiliden okundu —
  `--json-schema` diye bir bayrağı yok). Şema taşıyan bir isteği şema veremeyen
  bir sağlayıcıya yönlendirmek, brain'in bir düğümü başlıklı ama
  değerlendirmesiz saklamasıyla biterdi — hiçbir hata vermeden. Router artık
  hiçbir şey harcamadan reddediyor.
- **`gemini` CLI bir sağlayıcı.** Bayrakları hafızadan değil `--help`'ten,
  hata zarfı CLI'ın kendisinden gözlemlendi. Bu makinede kurulu ama oturumu
  kapalı, ve `GET /llm/providers` bunu ayrı ayrı söylüyor: "kurulu mu"
  bedava bir soru, "oturum açık mı" bir model çağrısı — ikincisi istenince
  sorulur.
- **Operatörün sağlayıcı tercihi ayarlara girdi.** Distil ve reason sınıfları
  için birer varsayılan; brain'in kendi pasoları ilk kez sabit bir tiere
  çakılı değil. Sınıfın *hangi iş* olduğu kodda kalıyor (SD-1); *bu makinede
  kim hizmet ediyor* operatörün yazısı.

- **Ürün başına pazar araştırması ve yeniden yazım (task-87).** Araştırma mevcut
  `search → crawl → refine → merge` hattından geçiyor — bu bir tercih değil,
  SD-2'nin şartı: kazınmış sayfa metni `internal/refine`'dan geçmeden hiçbir yere
  düşmüyor. Yeniden yazım `llm.Reason` sınıfı, çünkü marka sesinde metin yazmak
  ve kaynaklar arası sentez yapmak sıkıştırma değil.
- **Modele bu yolda da hiç işaretleme gösterilmiyor.** Şema *metin* taşıyan
  tiplenmiş blokların listesini istiyor; zarfı mağazanın kendi geçmiş HTML'inden
  `envelopeFor` seçiyor. Modelin isteyebileceği bir etiket reddedilmiyor — onu
  isteyecek bir yolu hiç olmadı. Modelin uydurduğu bir URL düz metne iniyor.
- **İki önbellek anahtarı, marka hash'i yalnız birinde.** Taslak anahtarı prompt
  sabitini, modeli, marka hash'ini ve skill sürümünü taşıyor; araştırma anahtarı
  prompt sabitini ve modeli. Bir rakip sayfasının kategori hakkında söylediği şey,
  bu mağaza okuruna "siz" demeye karar verdi diye değişmiyor — marka sesini
  düzeltmek operatörün yapabileceği en pahalı şey olmamalı.
- **`internal/catalogjob` — panodaki kart.** `coderunner.Executor`,
  `Agent() == "catalog"`, `LaneWorker`. `internal/coderunner` altında hiçbir
  dosyaya dokunulmadı; task-79'un dikişi tuttu. Kart açık bir ürün listesi alıyor,
  asla bir filtre değil.
- **`product-content` skill'i.** Markanın kendi sesi ve sözlüğü CSV'den okunuyor;
  bu dosya **yöntemi** anlatıyor: neyin uydurulmayacağını, somut niteliklerin
  makinenin çıkarabileceği düz cümlelerle yazılmasını, anahtar kelime listesinin
  düzyazı olmamasını.
- **İki MCP aracı: `catalog_products` ve `catalog_product`.** İkisi de okuma.
  Üçüncüsü — toplu yeniden yazımı kuyruğa alan araç — **bilinçli olarak
  yazılmadı**: kayıtta bütün etkisi para harcamak olan tek araç olurdu, birinin
  yazdığı bir cümleden bir katalog dolusu arama ve model çağrısı. Onu kuyruğa
  alan rota açık bir ürün listesi istiyor ve operatörün o ürünlere az önce baktığı
  bir ekranda duruyor.
- **Tanınmayan bir başlık için sütun önerisi ve örnek değerler (task-90/91).**
  Otuz yedi sütunlu bir dosyada operatörden sekiz açılır listeyi sıfırdan
  doldurmasını istemek, tanıma hatasının faturasını ona kesmektir. `Suggest`
  deterministik ve model çağrısız bir tahmin üretiyor (bir sütun iki alana
  verilmiyor: `Açıklama` gövdeye, `Metadata Açıklama` SEO'ya), form onunla dolu
  açılıyor, ve her seçimin altında dosyanın kendi ilk satırından bir örnek değer
  duruyor. Eşleme, dosya tanınmış olsa bile "sütunlar" düğmesinden her zaman
  açılabiliyor ve kimlik sütunlarını (`Ürün grup ID`, `SKU`, `Kategori`) da
  sunuyor — bunlar formda olmadığı için tanınmayan bir dosya varyant satırlarını
  hiç gruplayamıyordu.
- **Ürün tezgâhı: editör ve önizleme yan yana (task-90).** Bir ürün tam
  genişlikte açılıyor — solda markanın sözlüğüne kısıtlanmış editör, sağda canlı
  önizleme, ve önizlemenin üstünde `önce · sonra` anahtarı. "sonra",
  kaydedilmemiş düzenleme dahil editördeki hâl. Tabloda satır başına "aç", çift
  tık da açıyor. Yirmi altı remlik panelde editörle önizlemeyi yan yana koymak,
  kimsenin okuyamayacağı iki sütun demekti; "bu daha mı iyi" sorusu ikisini de
  aynı anda istiyor.

### Düzeltildi

- **Marka kimliğindeki metin alanlarında boşluk tuşu çalışmıyordu (task-112).**
  Bir tuş dinleyicisi değil, denetimli bileşenin kendisi: panel *saklanan* sesi
  (iki `string[]`) tutuyor ve her tuş vuruşunda metin alanından yeniden
  kuruyordu, yani `"Kesinliği "` → `split` → `trim` → `join` → `"Kesinliği"`.
  Boşluk, onu yazan tuş vuruşunda siliniyordu; `filter(Boolean)` de aynı şeyi
  Enter'a yapıyordu, o yüzden yeni satır da açılamıyordu. Operatörün "boşluk
  tuşu çalışmıyor" raporu olan biteni birebir anlatıyordu. Form artık metin
  tutuyor (`voiceDraft`) ve bölme yalnızca kaydederken çalışıyor
  (`voiceFromDraft`). Aynı şekle sahip iki latent risk de kapatıldı: çıplak harf
  ve boşluk bağlayan Lead-gen panelleri artık `isTypingTarget`'ı soruyor —
  "bu panelde metin alanı yok" bir kural değil, bağlamanın bugün durduğu yerdi.
- **Katalog'un "seçilenleri yeniden yaz" düğmesi çalıştı, ama ekran bunu hiç
  söylemedi (task-112).** Operatör tek ürün seçti, düğmeye bastı, hiçbir şey
  olmadı sandı. Aslında: `POST /catalog/rewrite` 201 döndü, kart kuyruğa girdi,
  arama + tarama + rafine **altı dakika** sürdü ve ürün `llm: provider cannot
  return structured output: ollama` ile başarısız oldu. Ekran bunların hiçbirini
  çizmiyordu — dönen `run_id` çöpe atılıyordu, `RunsProvider`'a hiç bakılmıyordu
  ve ürünün `reason` alanı API'de olduğu hâlde hiçbir yerde görünmüyordu. Artık
  Ürünler tablosunun üstünde koşu satırı var (uygulamanın tek yoklama döngüsünü
  okuyor, kendi zamanlayıcısını kurmuyor), paso bitince liste kendiliğinden
  tazeleniyor, başarısız ürünün sebebi hem rozetin üstünde hem detay panelinde
  yazıyor, ve `panoda aç` karta bağlıyor.
- **Yapısal çıktı veremeyen bir model altı dakika yaktıktan sonra reddediliyordu
  (task-112).** `internal/llm`'in yönlendiricisi bu eşleşmeyi zaten reddediyor
  ve yorumu "before anything is spent" diyor — o paket için doğru, katalog için
  değil: buradaki her ürün bir arama, bir tarama, bir rafine ve *ondan sonra*
  şemalı çağrı. Kontrol artık `Studio.Rewrite`'ın mevcut `ErrNothingToWrite`
  kapısının yanında, ürün döngüsünden önce; `llm.Router.Capabilities` çağrının
  kendi çözümlemesini kullanıyor ki kontrol ile çağrı ayrışamasın. HTTP tarafı
  422 dönüyor, içe aktarım görünümü sebebi taşıyor (`rewrite_blocked`) ve düğme
  **sebebi yanında yazılı** olarak pasif çiziliyor. Ölçüldü: reddedilen bir
  istekte günlükte tek bir `pipeline.stage` satırı yok.
- **Daemon `languages[].fields`'i hiç göndermiyordu (task-112).**
  `catalogLangView`'da böyle bir alan yoktu ve `File.WriteSet` kendi testleri
  dışında ölü koddu. Eksikliğin belirtisi bir özelliğin yokluğu değil, ekranın
  gerçeğin tersini söylemesiydi: `writeSummary` her içe aktarım için koşulsuz
  "hiçbir alan yazılmayacak" yazıyor, "alanlar" paneli her dil için "bu dilde
  yazılabilecek bir sütun yok" diyor, "kaydet" kalıcı pasif kalıyordu — ve o
  etiketin altında kuyruğa giren paso beş alanın hepsini yazıyordu. Görünüm
  artık her dil için `File.Offered` üzerinde dönüp `File.Writes`'ı soruyor, yani
  yapılandırılmamış bir dosya "hepsi açık" olarak geliyor.
- **Tanınan bir dosyada sütun formu bomboş açılıyordu (task-112).** IKAS ürün
  export'u sorunsuz tanınıyor, otuz yedi sütunun dokuzu doğru bağlanıyor, ürünler
  okunuyordu — ama "sütunlar" formu her açılışta dokuz açılır listeyi de "—"
  gösteriyordu. Sebep: form yalnızca `mapping` alanını okuyordu, o alan ise
  *operatörün kendi* haritasını taşır ve bir profil eşleştiğinde boş kalır.
  `initialMapping` artık dosyanın **gerçekten okunduğu** sütunlardan
  (`languages[].columns`, yani daemon'ın `File.ColumnsFor`'u) açılıyor; hiçbir şey
  eşleşmediğinde eskisi gibi `suggested` tahminine düşüyor.
- **Aynı formdaki düzeltme sessizce yok sayılıyordu (task-112).** Form "bir sütun
  yanlış eşlendiyse buradan düzeltin" diyordu; kaydedilen harita doğrulanıyor,
  saklanıyor, dosya yeniden okunuyor — ve sonra her okuma yine profilden
  geçiyordu, çünkü `ColumnsFor` diyalekti önce sayıyordu. `SetDialect` bunun
  tersini zaten söylüyordu ("operatörün az önce seçtiği daha yeni cevaptır");
  artık iki yön de aynı şeyi söylüyor: operatörün haritası profilin üstündedir ve
  onu **değiştirir**, birleştirmez — formda boşaltılan bir sütun profilden geri
  gelmez. Geri alma yolu `SetDialect`'in aynı anahtarla çağrılmasıdır.
- **Yedi gerçek export biçiminin ikisi bayt bayt aynılık testinin dışındaydı
  (task-112).** `ikas-ceviriler.csv` ve `ikas-fields-variant.csv` fikstürleri her
  satır sonunda `\r\r\n` taşıyordu — hiçbir dışa aktarıcının üretmediği bir dizi
  — ve test yalnızca üç fikstür üzerinde koştuğu için bu fark edilmemişti.
  Fikstürler gerçek dosyaların satır sonlarına çekildi;
  `TestExport_EveryRealExportShapeComesBackByteForByte` artık yedisinin hepsinde
  koşuyor. Yanına `TestExport_NeverWritesIdentity` kondu: her yazılabilir alanı
  değiştiren bir pastan sonra bile `SKU`, `Ürün Grup ID` ve `Slug` sütunları
  satır satır aynı kalıyor.

- **Toplu yeniden yazımın ürettiği taslakların hiçbiri ekranda görünmüyordu
  (task-101).** Bir taslak, altında yazıldığı anahtarla saklanıyor ve o anahtar
  birebir eşleniyor. Kart, kataloğ ajanının beceri sürümünü de içeren anahtarı
  kuruyordu (`…#product-content:<v>`); HTTP tarafı ise `catalogSkillVersion()`
  boş döndüğü için o yarısı olmayan anahtara bakıyordu. Sonuç: para harcanmış
  her taslak, hiçbir okumanın bakmadığı bir yere yazılıyordu — `GET
  /catalog/products` hepsi için `draft: null` diyor, `Export` hiçbir şey
  yazmıyordu. Aynı saplama MCP araçlarında da vardı (`llm.Selection{}`, `""`).
  Kök sebep anahtarın **iki yerde ayrı ayrı kurulmasıydı**, dolayısıyla düzeltme
  saplamayı doldurmak değil: bileşim `Studio.CurrentDraftVersion`'a taşındı ve
  üç kapı da (HTTP rotaları, MCP araçları, pano kartı) onu soruyor. Operatörün
  model seçimi ve beceri sürümü stüdyoya `UseDraftKey` ile enjekte ediliyor —
  `llm.Router`'ın varsayılanlarını aldığı desenin aynısı. İki tarafı birden
  kapsayan bir regresyon testi eklendi; tek başına her iki yarı da özellik
  bozukken geçiyor, ki bu zaten böyle yayına çıkmasının sebebi.
- **Sınıf varsayılanları hiç kaydedilmiyordu (task-100).** Ayarlar ekranı
  "kaydedilmemiş" diyor, düğmeyi açıyor, hiçbir şey yazmıyor ve "Kaydedildi"
  diyordu. Sebep, tek bir sorunun iki yerde cevaplanmasıydı: `anyDirty` testli
  bir dosyadaydı, sınıf karşılaştırması JSX'te dört satır içi ifadeydi, ve
  ikisi ayrıştı. Tek testli fonksiyona indirildi.
- **Sağlayıcı kaydı kendine dair yarım cevap veriyordu (task-97).**
  `Router.Providers()` yalnız sınıf haritasını ve fallback'i geziyordu, yani
  `gemini` ve `ollama` kayıtlı, seçilebilir ve **hiç sorulmamış** durumdaydı —
  ve masaüstü seçicisinin "veri yoksa sorun yok" dalı ikisini de bakmadan
  onaylıyordu. Kendine dair yarım cevap veren bir kayıt, diğer yarısı için
  sessizce kefil olur.

- **IKAS export'u tanınmıyordu (task-91).** task-85'in `ikas` profili hafızadan
  yazılmıştı — `Ürün Adı`, `Stok Kodu`, `Kategori` — ve kimsenin indirmediği bir
  dosyayı tarif ediyordu. Gerçek başlık 37 sütunlu: `Ürün Grup ID / Varyant ID /
  İsim / Açıklama / … / SKU / … / Kategoriler / Etiketler / Metadata Başlık /
  Metadata Açıklama / Slug`. Operatör kendi kataloğunu attı ve ekran
  "TANINMADI" dedi. Profil gerçek dışa aktarımdan yeniden yazıldı, IKAS'ın
  **özel alanlar** export'u (`Html:Detay`) ikinci bir lehçe olarak eklendi, ve
  doğrulanamayan `ikas-en` silindi: hiç eşleşmeyen bir profil, listede duran bir
  iddiadır. `internal/catalog/AGENTS.md`'ye kural yazıldı — bir lehçe profili
  yalnız eldeki gerçek bir dışa aktarımdan eklenir.
- **Eşlemeyi kaydetmek ekranı açmıyordu (task-90).** Elle eşleme yolu vardı,
  kaydediyordu, ve hiçbir işe yaramıyordu: ekranın kapısı `dialect === ""` diye
  soruyordu, `SetMapping` ise bir lehçe *uydurmaz* — eşlemeyi kaydeder. Otuz yedi
  sütunu elle eşleyip kaydete basan operatör formun kendisini geri alıyordu. Kapı
  artık daemon'ın söylediği `readable`: bir platform uydu **ya da** eşleme yeterli.
  Ne başlık ne açıklama adlandıran bir eşleme de artık sessizce kabul edilip boş
  ürünlere dönüşmüyor, bir cümleyle reddediliyor.
- **Grafiğin üstündeki kaydırma sayfayı da kaydırıyordu (task-92).** Tek el
  hareketi iki şeyi birden oynatıyordu. React `wheel`'i kök kapsayıcıya pasif
  bağladığı için JSX işleyicisindeki `preventDefault` yok sayılıyordu; teker
  artık pasif olmayan native bir dinleyici ve hareketi sahipleniyor. Bedeli
  yazıldı: sayfa kipinde imleç grafiğin üstündeyken sayfa kaydırılamıyor —
  haritaların davranışı, ve istenen de buydu.
- **Düğüm panelindeki metin sarmıyordu.** Modelin yazdığı düzyazı düzenli olarak
  hiçbir satır sonu kuralının bölemeyeceği bir şey taşıyor: bir oturum URL'i, bir
  hash, bir import yolu. O en uzun şey panelin, panel de sayfanın genişliğini
  belirliyor ve yatay kaydırma çubuğu beliriyordu. Panel gövdesi artık
  `wrap-anywhere`; taşma kırpılarak değil, sarılarak çözüldü.
- **Sonradan eklenen bir lehçe, ondan önce yüklenmiş dosyalara da uygulanıyor.**
  Algılama başlığın saf bir fonksiyonu ve profil tablosu kod; "yüklendiğinde
  eşleşmedi" kalıcı bir gerçek değil. Okumada lehçe yeniden algılanıyor (ama
  operatörün kendi eşlemesi ezilmiyor — o eşleme bir kez başarısız olduğumuz
  için var), ve `POST /catalog/imports/{id}/reread` ürünleri o sütunlarla
  yeniden kuruyor. Ekran, okunabilir ama bütün ürünleri boş olan bir import'ta
  bunu söylüyor. Yalnız rozeti düzeltmek hiç düzeltmemekten kötü olurdu:
  "IKAS · 1013 ürün" yazıp boş satır göstermek.
- **Yeniden yazım varyant satırlarının yalnız birine yazılıyordu.** Shopify
  `Title` ve `Body (HTML)`'i yalnız ilk satıra yazdığı için "değerin okunduğu
  satıra yaz" kuralı orada doğruydu; IKAS aynı açıklamayı her varyant satırında
  tekrar ediyor, ve tek satıra yazmak dosyayı kendisiyle çelişir hâlde bırakıyordu.
  Alan artık özgün hâlinin dolu olduğu her satıra yazılıyor — Shopify'da bu hâlâ
  tek satır, davranış birebir aynı.

### Değiştirildi

- **Seçili/seçili değil ayrımı artık bir primitif ve yazılı bir kural
  (task-112).** `desktop/AGENTS.md` "seçili bir şey bunu birden fazla kez
  söyler" diyordu ve uygulama bunu tutmuyordu, çünkü koyacak yer yoktu.
  "Tutuluyor" görünümü tüm uygulamada elle iki yerde yazılmıştı; seçili satır
  rayı sekiz yerde dört farklı DOM şekliyle — biri de Katalog ürün tablosunda
  **`border-l-electric`**, yani bu bölümün düzeltildiğini söylediği palet
  hatasının ta kendisi. `ui/button` `active` + `activeAria` aldı (dolgulu yüzey,
  tam Mist etiket, altta 2px Lime çubuk; görünüş tek, duyuru iki: bir paneli
  açan `aria-expanded`, açık kalan ayar `aria-pressed`), `ui/rail` sekiz rayın
  tamamını topladı, ve `ui/card`'ın hiç kullanılmayan `accent` prop'unun anlattığı
  şey artık gerçekten bir bileşen. Katalog'un `sütunlar` · `alanlar` ·
  `marka kimliği` düğmeleri — operatörün işaret ettiği üç düğme — panelleri
  açıkken tutuluyor görünüyor.

- **Katalog'un düzenleme yüzeyi zengin metin editörü değil, açıklamanın kendi
  HTML'i (task-112).** TipTap buraya gerçek bir gerekçeyle gelmişti — şema güdümlü
  bir editör, daemon'ın atacağı bir etiketi üretemeyen tek editör türüdür — ve o
  gerekçe *işaretler* konusunda haklı, *yapı* konusunda sessizce yanlıştı. Şemada
  bir sarmalayıcı düğümü yoktu; bu mağazanın `<div class="flex flex-nowrap
  gap-4"><div class="flex-none w-3/5">` ile başlayan açıklamasını açıp kaydetmek,
  aynı kelimeleri iki div'i düşürerek geri gönderiyordu. Kimseye bir uyarı
  gitmiyordu, çünkü editörün içinden bakınca metin yerindeydi; kayıp yalnızca
  dışa aktarımda görünüyordu. Yerine `HTMLSource`: kaynağı olduğu gibi gösteren
  ve olduğu gibi kaydeden bir alan. `formatHTML` girintiyi **yalnızca iki etiketin
  arasına** koyuyor — metnin içine asla, çünkü daemon'ın ayrıştırıcısı bir metin
  akışındaki satır sonunu `<br>`'ye çeviriyor — ve `flattenHTML` onun test
  edildiği tersi: gösterilen kaynak, saklanan kaynağın kendisi. Dört bağımlılık
  (`@tiptap/*`), `editorSchema`, `starterKitOptions` ve `.rte-surface` stil
  bloğu onunla birlikte gitti.

- **Menü çubuğu artık tek bir panel (task-94).** İki yüzey vardı: altı satırlık
  bir native `NSMenu` — sistemin grisi, sistemin yüzü, sistemin ayırıcıları,
  çünkü `NSMenu` başka bir şey çizemez — ve ⌘⇧G'nin **ekranın ortasında** açtığı
  680×460'lık bir kutu. Ortada beliren bir panel hiçbir şeye ait değildir ve
  arkasındaki her şeyi bağlamdan çıkarır. Yerine 360 puanlık tek bir panel,
  ikonun altına tutturulmuş: durum, besteci, koşu, ve dört eylem. Sağ tıkta iki
  satırlık native cankurtaran kaldı (`Restart daemon`, `Quit Mimir`) — uygulama
  accessory ve panelin WebView'ı takılırsa o WebView'ın çizdiği bir çıkış
  ulaşılamaz bir çıkıştır.
- **Açılış ekranı başarılı el sıkışmada gösterilmiyor.** Daemon loopback'te
  ~200 ms'de cevap veriyordu ve karşılığında her açılışta bir poster, dört
  bağımlılık satırı ve basılacak bir "Devam" düğmesi vardı — arkasında hiç karar
  olmayan bir düğme. Bekleme derecelendi: 600 ms'ye kadar hiçbir şey, sonra tek
  satır, başarısızlıkta stderr'ıyla dürüst yüzey. Markanın kendi yüzeyi, gerçekten
  yapacak bir şeyin olmadığı tek duruma taşındı.
- **Ana ekrandan "Hesap" paneli kalktı.** Aynı panel `Workspace`'te duruyor;
  Mimir'in tek hesabı var ve hiç değişmeyen bir bilgi en değerli yeri tutuyordu.
- **Sağlayıcı çalıştırılamıyorsa koşu ilk üründe duruyor.** Ölçüldü: kimliği
  doğrulanmamış bir `claude` CLI'ıyla kart "tamamlandı" diyor ve üç ürünün üçü de
  aynı sistemik sebeple `failed` oluyordu — panoda yeşil bir kart, altında
  dokunulmamış bir katalog. Bir ürünün hatası o ürünün, bir sağlayıcının hatası
  koşunun: dört yüz ürünlük bir katalogda "CLI oturumu kapalı" bilgisini öğrenmek
  için dört yüz alt süreç başlatmanın anlamı yok. Hiçbir şey yazamayan bir koşu
  artık başarısız — SD-6'nın kendi ifadesiyle, "her kaynak başarısız olmadıkça".
- **Model limiti artık bir duraklama, bir başarısızlık değil (task-89).**
  `coderunner.Outcome` bir `ParkUntil` alanı ilan ediyordu ama `runExecutor` onu
  hiç okumuyordu; alanın kendi yorumu bile "park yolu worker lane'den hiç
  erişilemez" diyordu, ki bir worker işi model bütçesi harcamaya başladığı anda
  bu doğru olmaktan çıkmıştı. Dikiş artık onu okuyor: satır `queued`'a dönüyor,
  sebep satırda kalıyor ve pencerenin kendi uyandırması kuyruğu yeniden
  başlatıyor. Limit tükendiğinde hiçbir alt süreç harcanmıyor — ölçüldü: duraklama
  yürürlükteyken kart üç yoklama boyunca kuyrukta kaldı ve CLI hiç çağrılmadı.
- **Worker lane'in limiti `worker-lane` altında tutuluyor.** Hesap lane'i kimlik
  yuvası başına bir duraklama tutuyor, çünkü tükenen o. Worker lane'deki bir iş
  öyle bir yuva tutmuyor ama bütçesiz de değil: daemon'ın kendi model kimliğini
  harcıyor ve süreçte ondan bir tane var — dolayısıyla bütün worker işleri için
  tek duraklama. Hesap lane'inin davranışı bit bit eskisi.
- **Panoda park rozeti (task-88).** Park edilmiş bir kart ile daha hiç başlamamış
  bir kart ikisi de `queued`; ikisini ayırt edemeyen bir operatör gece boyu süren
  bir duraklamayı takılma sanıyordu. Rozet daemon'ın kendi hold'larından okunuyor,
  kartın hata metninden değil, ve kartların bindiği aynı poll'a biniyor. İki lane
  ayrı: bir katalog kartı asla bir kimlik yuvasının limitiyle tutulmuş görünmüyor.
- **Katalog kartının gövdesi ve sonuca açılan kapı.** Kaç ürün, araştırma açık mı,
  kaç alan — hepsi kartın kendi `params`'ından, ek istek açmadan. Bitmiş bir
  kartta "ürünleri gör" Katalog ekranını o import'ta açıyor.
- **"Seçilenleri yeniden yaz" düğmesi gerçek.** task-86'da rota henüz yokken
  devre dışıydı; artık seçim açık bir id listesi olarak gidiyor ve iş panoda bir
  kart oluyor.

### Eklendi

- **Katalog ekranı (task-86).** İçe aktarma, ürün tablosu, eski/yeni önizleme,
  alan düzenleme ve dışa aktarım. Ekran üç soruyu sırayla cevaplıyor ve düzeni o
  sıra: dosyamı anladı mı (lehçe, çerçeve, ürün sayısı, kendi HTML'imden okunan
  sözlük), içinde ne var (tablo), ve bir üründe ne değişecek (önizleme, alanlar,
  editör).
- **Marka sözlüğüne kısıtlanmış zengin metin editörü.** Editörün şeması
  markanın kendi geçmiş HTML'inden kuruluyor: hiç başlık kullanmamış bir mağazaya
  başlık düğmesi sunulmuyor, `<b>` yazan bir markaya `<strong>` verilmiyor.
  Sözlükte olmayan her şey gizlenmiyor **kapatılıyor** — yalnızca düğmesi olmayan
  bir uzantıya klavye kısayolu ve yapıştırma hâlâ ulaşır. Bu, şemaya dayalı bir
  editör almanın tek gerekçesi: daemon'ın süzeceği bir etiketi üretemeyecek olan
  tek editör türü odur.
- **Önizleme `sandbox`'lı bir `srcdoc` iframe.** İki kilit birden: `sandbox=""`
  hiçbir izin vermiyor (`allow-scripts` yok) ve `srcdoc` bu belgenin CSP'sini
  devralıyor — `script-src 'self'` bu task'ta değişmedi. Yalıtım kadar kapsama da
  isteniyordu: `<style>` bloğu taşıyan bir açıklama çevresindeki uygulamayı
  yeniden düzenleyemiyor.
- **Katalog tembel yükleniyor, ve bunu yapan tek ekran o.** Editörün ölçülen
  maliyeti **+130 kB gzip** — paketin yarısından fazlası kadar (227 kB → 357 kB).
  `React.lazy` arkasında ayrı bir parça olarak duruyor, açılışların çoğu onu hiç
  yüklemiyor ve başlangıç paketi özellikten önceki hâlinin bir kilobayt içinde.

### Değiştirildi

- **`img-src`'ye `https:` eklendi, başka hiçbir şeye dokunulmadı.** Ürün
  açıklaması fotoğraflarını mağazanın kendi CDN'inde tutuyor ve bir yeniden
  yazımı değerlendiren operatörün ürünü görmesi gerekiyor. Bedeli açıkça
  söyleniyor: bir önizleme açmak o CDN'e istek yapıyor.
- **İçe aktarma artık modelin arkasında dakikalarca beklemiyor (task-85 kusuru).**
  Ses distil'i `internal/llm`'in `RefineTimeout`'unu (180 sn) devralıyordu; o
  bütçe arka planda bir sayfa damıtmak için ölçülmüştü, operatörün dosya bırakıp
  beklediği bir istek için değil. Gerçek CLI ile ölçüldü: istek 4 dakika açık
  kaldı. Artık kendi sınırı var (`CatalogVoiceTimeout`, 45 sn) ve süre dolduğunda
  sözlük ve import ayakta kalıyor, sebep marka panelinde yazıyor, yeniden
  çıkarmak bir düğme.
- **Operatörün kendi yazdığı bağlantı artık bir uydurma sayılmıyor (task-85
  kusuru).** "Kaynakta olmayan URL bir URL değildir" kuralı bir modelin
  uydurmasını durdurmak için yazılmıştı; elle düzenlemeye de uygulanınca
  editörün bağlantı düğmesi sessizce hiçbir şey yapmayan bir düğmeye dönüşüyordu
  — bu paketin önlemek için var olduğunu söylediği hatanın ta kendisi. İki yol
  artık ayrı: bir insanın kararı kabul ediliyor, bir modelin çıktısı hâlâ
  kaynağın URL'leriyle sınırlı.
- **`<script>` ve `<style>` içeriği artık metin sayılmıyor (task-85 kusuru).**
  Bilinmeyen bir sarmalayıcıyı açıp metnini korumak `<section>` için doğru,
  `<script>` için yanlıştı: etiket düşüyordu ama gövdesi ürün sayfasına
  görünür bir cümle olarak yazılıyordu.
- **Operatör kendi taslağını ikinci kez kaydedebiliyor (task-85 kusuru).**
  `edited_by_operator` koruması makineyi durdurmalıydı, insanı değil; ikinci
  kayıt sunucudan açıklanamayan bir 500 alıyordu. Koruma artık gelen yazımın da
  operatöre ait olup olmadığına bakıyor, ve reddedilen bir yazım sebebiyle
  birlikte 409 dönüyor.

### Eklendi

- **`internal/catalog` — ürün içeriği stüdyosunun çekirdeği (task-85).** Bir CSV
  export'unu okur, varyant satırlarını ürünlere toplar, açıklamalardaki RTE
  HTML'inden markanın sözlüğünü çıkarır ve dosyayı geri yazar. Bu sürümde hiçbir
  ürün içeriği yeniden yazılmıyor; yeniden yazımın **güvencesi** kuruluyor.
- **Modele hiç işaretleme gösterilmiyor.** Bir açıklama `Block`'lara ayrılıyor:
  yeniden yazımın dokunabildiği metin, ve o metnin içinde durduğu opak `Envelope`.
  Yeniden basım zarfları tekrar oynatıyor. Modele HTML verip HTML istemek bu
  tasarımın önlemek için var olduğu hata — uydurulan işaretleme, operatörün canlı
  bir mağazaya yapıştırdığı şey oluyor. Testi bir alt küme iddiası: çıktının etiket
  kümesi girdinin etiket kümesinden büyük olamaz.
- **Girdide geçmeyen bir URL bir URL değil.** Yeniden basım kaynak belgenin kendi
  href/src kümesini izin listesi olarak alıyor; hedefi orada olmayan bir bağlantı
  düz metne iniyor, kaynağı olmayan bir görsel düşüyor. Bir ürün sayfasındaki
  uydurulmuş bağlantı, tüccarın müşterisine gönderdiği kırık bir bağlantıdır ve
  bir yeniden yazımın en olası hatasıdır.
- **Kayıpsızlık bir sözleşme.** Kimsenin değişiklik onaylamadığı bir hücre yeniden
  türetilmiyor, kopyalanıyor: BOM, kodlama (UTF-8 / Windows-1254), ayraç (`,` /
  `;`), satır sonu, kapanış satır sonu ve **her bir hücrenin tırnaklaması**.
  Bu yüzden CSV okuyucusu ve yazıcısı bu paketin kendisinin: `encoding/csv` hangi
  hücrelerin tırnaklandığını söyleyemiyor, yazıcısı da bu kararların üçünü kendi
  sahipleniyor. Shopify HTML sütununu gerekmese de tırnaklıyor ve dosya başına tek
  bir kural o dosya hakkında hem "minimal" hem "hepsi" derken yanılıyor.
- **Marka kimliği iki yarım, biri bedava.** Sözlük (etiket, sınıf, stil özelliği
  frekansları ve yapı sayımları) her açıklamayı okuyor ve **hiç model çağrısı
  harcamıyor** — testi nil bir `Completer` ile koşuyor, yani kırılmış bir iddia
  sessizce bir alt süreç başlatmak yerine panikliyor. Ses profili tek bir
  `llm.Distill` çağrısı ve reddi yalnız sesi kaybettiriyor, import'u değil (SD-6).
- **`/catalog/*` rotaları.** İçe aktarma, sütun eşleme, marka kiti okuma ve
  düzenleme, ürün listesi, taslak kaydetme, durum ve dışa aktarım. İçe aktarma bu
  yüzeydeki gövdesi bir belge olan tek rota ve `bodyLimits` tablosunda kendi satırı
  var — bir rotanın capini yükseltmek orada verilmiş bir karar olmalı.
- **Sunucu yetkili taraf, editör bir kolaylık.** `PUT /catalog/products/{id}/draft`
  istemcinin gönderdiği ne ise aynı sadeleştirme kapısından geçiriyor ve neyin
  sadeleştirildiğini cevapta söylüyor. Satır elle düzenlenmiş işaretleniyor ve
  bundan sonra hiçbir toplu geçiş onu ezemiyor — koruma SQL'de
  (`WHERE edited_by_operator = 0`), `outreach_emails`'in `WHERE status = 'draft'`
  koruması ile aynı biçimde.
- **İki ayrı önbellek anahtarı (migration `0025_catalog.sql`).** Marka hash'i
  taslak anahtarında var, araştırma anahtarında yok. Marka sesini düzeltip yeniden
  koşan bir operatör her açıklamayı yeniden yazdırıyor ve hiçbir rakip
  araştırmasını atmıyor. Araştırmayı yazan taraf task-87, tablosu burada kuruldu
  çünkü migration'lar append-only.
- **`handle` ve `sku` okunuyor, asla yazılmıyor.** Handle ürünün URL'i; yeniden
  yazmak ona işaret eden her bağlantıyı ve her sıralamayı 404'e çeviriyor. Bu bir
  yeniden yazım değil bir taşıma, ve bu paket taşıma yapmıyor.

**Anthropic hesabı artık kalıcı: bir kez bağlanıyorsunuz, uygulamayı kapatmak
oturumu kapatmıyor.**

### Değiştirildi

- **Uygulamayı kapatmak artık hesabı düşürmüyor.** Kimlik yuvası
  (`~/Library/Application Support/mimir/claude-session`) ve onun adlandırdığı
  Keychain girdisi kapanışa dayanıyor, yani her açılışta yeniden
  `claude auth login` yapmak gerekmiyor. Mimir yine hiçbir kimlik bilgisini
  okumuyor, taşımıyor ya da saklamıyor — kalıcılık için yazılan tek şey silme
  işleminin kaldırılması oldu. Yuva hâlâ Mimir'in kendi yuvası, terminaldeki
  `claude` oturumunuz ayrı bir Keychain girdisi ve ona dokunulmuyor.
- **Açılışta `Reset` yerine `account.Restore`.** Bir giriş iki açılış arasında
  düşebilir ya da iptal edilebilir, ve Keychain'in arkasında durmadığı bir kayıt
  olmayan kapasiteyi ilan eder — o yüzden `claude auth status` karar veriyor:
  giriş yaşıyorsa kayıt korunuyor (veritabanı kaybolmuşsa yeniden yazılıyor),
  CLI net olarak "boş" diyorsa tam `Reset`, probe hiç cevap veremediyse
  (CLI yok, Keychain kilitli, timeout) **yalnızca kayıt** siliniyor — dizini
  silmek geçerli bir Keychain girdisini kalıcı olarak öksüz bırakırdı.
- **`POST /accounts/reset` artık yalnızca "çıkış yap" düğmesi.** Bağlantıyı
  kesen tek yol o, ve ikinci bir Anthropic hesabına geçmenin de yolu bu: çıkış
  yapın, diğer hesapla bağlanın.
- **Hesap panelinde "yeniden bağlan".** Kayıt duruyor ama Keychain girdisi
  ölmüşse ortaya çıkan tek yeni durum bu; bağlan düğmesi kayıt yüzünden gizli
  kaldığı için tek tıkla çıkış + giriş yapıyor.

### Kaldırıldı

- **`daemon::sign_out`** (`desktop/src-tauri/src/daemon.rs`) ve daemon'un
  kapanıştaki `accounts.Reset` çağrısı. Kapanmak çıkış yapmak değil.

**Taslak yazmak artık "bulunan herkese" değil, işaretlediklerinize — iki kanala,
kendi yazdığınız kurallarla. Model seçimi ve kural dosyaları arama çubuğundan
çıkıp kendi ayar ekranına taşındı.**

### Eklendi

- **Şirket tablosunda onay kutusu sütunu.** Seçim `place_id` kümesidir, satırın
  üzerindeki bir bayrak değil: tablo daemon tarafında süzülüp sayfalandığı için
  süzgeçten çıkan bir satır hâlâ operatörün seçtiği şirkettir, ve bayrak olsaydı
  süzgeç değiştiği anda sessizce düşerdi. Bunun bedeli seçimin ekranı aşabilmesi,
  o yüzden alttaki çubuk "3 tanesi bu süzgeçte görünmüyor" diye açıkça söylüyor.
  Başlıktaki kutu üç durumlu (hiçbiri / bir kısmı / hepsi) ve **yalnızca tablodaki
  satırlara** dokunuyor — sessizce dört bin satır demek olan bir "hepsi" bu
  ekrandaki en pahalı yanlış anlama olurdu. Shift-tıklama aralık seçiyor, boşluk
  tuşu satırı işaretliyor, Enter satırı açıyor.
- **`POST /maps/outreach`** — süzgeç değil, kimlik listesi alır. Aradaki fark
  ekranın tamamını belirliyor: arama "bana şirket bul", bu "şunlara yaz"
  demektir. Bir süzgeç, kısa bir dizeyle bir bölgenin tamamını harcatabilirdi ve
  operatör bunun kaç şirket olduğunu önceden göremezdi. Alttaki çubuk şirket
  değil **mesaj** sayısı yazıyor (şirket × kanal), çünkü harcanan o.
- **İki kanal: e-posta ve WhatsApp.** Taslaklar kanal başına saklanıyor ve karar
  da kanal başına: e-postayı gönderip WhatsApp mesajını atlamak olağan bir
  karardır. Taslak ekranı kanal sekmeleriyle okunuyor; "WhatsApp'ta aç" ve
  "E-postada aç" metni operatörün zaten kullandığı uygulamaya veriyor —
  uygulamanın kendi posta hesabı ya da WhatsApp oturumu yok, ve "gönderildi"
  bir tıklamanın yan etkisi değil operatörün kararı olarak kalıyor.
- **Kanal başına bir kural dosyası** (`internal/settings/rules/*.md`), taslak
  istemine olduğu gibi ekleniyor. Dosya diskte duruyor, yolu ekranda yazıyor —
  konumu sır olan bir kural dosyasına kimse güvenmez — ve kendi düzenleyicinizde
  de açabilirsiniz. Kural dosyası istemin parçası olduğu için **önbellek
  anahtarının da parçası**: düzenlediğinizde eski kurallarla yazılmış taslaklar
  geçersiz olur, "gönderildi" işaretlenmiş olanlar dahil. Dürüst takas bu —
  alternatifi, güncel kuralların hiç üretmediği bir mektubu göstermek olurdu — ve
  ekran bunu düzenlemenin yapıldığı yerde söylüyor.
- **Ayarlar ekranı.** Model seçimi ve iki kural dosyası tek bir ekranda, tek bir
  kaydetme çubuğuyla: neyin kaydedilmediğini adıyla söylüyor, ⌘S metin
  kutusunun içinden erişiyor. Üç ayrı kaydet düğmesi olan bir ayar sayfası,
  hangisini unuttuğunuzu sonradan öğrendiğiniz sayfadır.

### Değişti

- **Model seçimi arama çubuğundan kalktı.** Bir çalıştırmanın hangi modeli
  harcadığı kampanyaya ait bir karardır, tek bir aramaya değil — ve arama
  çubuğundaki seçici, durumunu sonradan kimsenin göremediği bir seçimdi. Artık
  ayar ekranında duruyor, `POST /maps/leadgen` hiç `provider`/`model`
  göndermiyor ve daemon kaydedilmiş varsayılanı kendisi okuyor
  (`api.leadgenSelection`); hiçbir şey kaydedilmemişse sınıf yönlendirmesi, yani
  bu ekran yokken her çalıştırmanın yaptığı şey. Brain taramasının kendi
  seçicisi duruyor: o tek bir tarama turunu yönlendiriyor, ayrı bir karar.
- Arama çubuğundaki kutu artık **"bulunan herkese e-posta taslağı yaz"** diyor.
  Onay kutulu bir sütunun yanında eski etiket "seçtiklerime yaz" gibi okunuyordu.

### Düzeltildi

- **`daemon_request` PUT ve PATCH'i reddediyordu.** Rust tarafındaki verb
  izin listesi GET/POST/DELETE'te kalmıştı, oysa TypeScript tarafı ikisini de
  çoktan tanımlamıştı: `editCodingTask` (kart düzenleme) ve
  `saveBrainScanPolicy` (tarama politikası) çağrıldıklarında proxy'den
  "unsupported method" alıyordu. Beş verb de artık listede.

### Kaldırıldı

- `POST /maps/emails/status` (yerine `POST /maps/outreach/status`);
  `LeadCompany.email_status`/`email_method` alanları — taslak durumu artık
  `drafts[]` içinde, kanal başına.

**Brain'in neyi okuyabileceği artık bir ayar, bir sabit değil — ve hangi
dosyaları hiç okuyamayacağı bir ayar bile değil.**

### Eklendi

- **Taranan klasörler operatörün.** `~/development` ve `~/Documents` bir Go
  sabitiydi; "bu daemon hangi klasörlerimi okuyabilir?" sorusunun cevabı dosya
  düzenleyip yeniden derlemekti. Artık Brain sekmesinde yerel klasör seçiciyle
  ekleniyor ve çıkarılıyor (`GET`/`PUT /brain/scan/policy`,
  `POST /brain/scan/policy/reset`). Sabit yerinde duruyor ama artık *tohum*:
  hiç kaydetmemiş bir makine tam olarak eskisi gibi tarıyor.
- **Hariç tutma listesi.** Bir dosya ya da bir klasör — klasör tüm alt ağacını
  kapsıyor. Eşleşme yol *bileşeni* üzerinden, dize öneki üzerinden değil:
  `/a/b` `/a/b/c`'yi kapsıyor, `/a/bravo`'yu kapsamıyor. Hariç tutulan yol
  `os.Stat`'tan önce eleniyor — verilen güvence baytların hiç okunmaması.
- **Politika her turda yeniden okunuyor.** Bir klasör eklendiğinde ya da hariç
  tutulduğunda bir sonraki tur ona uyuyor; daemon yeniden başlatılmıyor. Kök
  listesini boşaltmak da artık tek yönlü bir kapı değil: döngü bitmiyor,
  bekliyor.

### Güvenlik

- **Kimlik dosyaları artık hiç okunmuyor, hiçbir ayara bakılmadan.** Bir tarama
  dosya içeriğini harici bir model CLI'ının stdin'ine yazıyor, yani gözden kaçan
  bir `.env` fark edildiğinde makineyi çoktan terk etmiş oluyor. `.env*`,
  `*.pem`, `*.key`, `id_rsa*`, `.npmrc`, `.netrc`, `.pgpass`, `kubeconfig`,
  `*.tfstate`, `service-account*.json`, `.ssh/`, `.aws/`, `.gnupg/` ve
  benzerleri `scannable`'ın **ilk** kuralında eleniyor.

  Önceki tek engel `.gitignore`'du (`git ls-files --exclude-standard`) ve o
  engel repo olmayan dizinlerde — yani `~/Documents` taramasında — hiç devrede
  değildi. Bu denylist operatörün listesinden ayrı ve kasten
  yapılandırılamaz: hariç tutma listesi boşaltılabilir, bu boşaltılamaz.

  Denylist'in sıradan işi düşürmediği de test altında: `docs/secrets.md`,
  `internal/keyboard.go`, `src/environment.ts` taranmaya devam ediyor.

**Token limiti bittiğinde iş kaybolmuyor: koşan görev kuyruğa geri konuyor,
kuyruk duruyor, olan biten kalıcı bir loga yazılıyor ve limit yenilendiği anda
geliştirme pipeline'ı kendi kendine yeniden başlıyor.**

### Eklendi

- **Harcanmış token bütçesi artık bir duraklama, hata değil.** Bir çalıştırma
  görev ortasında limite takıldığında `failed` olmuyor: oturum kimliğiyle
  birlikte `queued`'a geri konuyor (`store.ParkRun`), böylece limit dönünce
  `--resume` ile kaldığı yerden devam ediyor — baştan başlamıyor. Kartın
  üzerinde neden beklediği ve saat kaçta devam edeceği yazıyor.
- **Kuyruk, bütçesi bitmiş yuvaya iş vermiyor.** Dispatcher o yuvayı boş
  saymıyor; sırada bekleyen kart, aynı cevabı bir kez daha almak için bir CLI
  çağrısı harcamıyor.
- **Kalıcı rate-limit logu** (`rate_limit_log` tablosu, migration 0021). Üç
  faz: `run` (bir çalıştırma limite takıldı ve kuyruğa döndü), `dispatch`
  (kuyruktaki bir görev başlatılamadı) ve `resumed` (pencere yenilendi, kuyruk
  yeniden başladı). Bellekteki bir halka tampon değil tablo, çünkü iki uç
  arasında saatler ve bir daemon yeniden başlatması olabiliyor: sabah "gece ne
  oldu?" diye soran operatör satır okuyor.
- **Limit yenilenince pipeline kendi kendine başlıyor.** Duraklama, CLI'ın
  kendi verdiği `resetsAt` değerine (ya da `…|<unix>` ekiyle gelen kullanım
  limiti cümlesine) kurulmuş tek bir uyandırma; o an gelince kuyruk her zamanki
  `pump` ile yeniden pompalanıyor. Hiçbir şeye basılmıyor, hiçbir döngü
  beklemiyor. CLI hiç saat vermediyse `CodingLimitRecheck` (15 dk) kadar
  bekleniyor — bu bir tahmin ve öyle davranılıyor.
- **Duraklama yeniden başlatmayı aşıyor.** Daemon açılışta logdan hâlâ süren
  duraklamaları geri kuruyor, yoksa her yeniden başlatma logun zaten bildiği
  şeyi bir CLI çağrısı harcayarak yeniden keşfederdi.
- **`GET /coding-tasks/queue/limits`** — şu an neyin beklendiği (`holds`) ve
  logun kendisi. `POST /coding-tasks/queue/kick` artık bütçe bittiğinde 409 ile
  kuyruğun kaçta kendi kendine devam edeceğini söylüyor.

### Değişti

- **Başarısız bir `result` satırı artık CLI'ın kendi cümlesini kartın üzerine
  yazıyor**, `error_during_execution` gibi bir kategori adını değil. Kullanım
  limiti mesajı da zaten orada duyuruluyor.

## [2.12.0] — 2026-09-04

**Mimir'in tek bir Claude hesabı var, o hesap Mimir'in kendi kimlik yuvasında
duruyor, ve uygulama kapanınca oturum kapanıyor.**

### Değişti

- **Çok hesaplı yuva kaydı kalktı.** `~/.claude-accounts` taraması, hesap
  ekleme/unutma, hesap seçici, "otomatik — ilk boşalan hesap" ve iki yuvanın
  aynı kimliğe düştüğünü söyleyen çakışma uyarısı gitti. Kapasite artık aynı
  anda tek çalıştırma: bir kimlik zaten tek rate limit'ti, ikinci yuva onu
  ikiye bölmüyordu.
- **Hesap artık Mimir'in kendi yuvası.** `config.ClaudeSessionDir` store'un
  yanında türetiliyor (`MIMIR_*` override'ı yok) ve CLI'a
  `CLAUDE_SECURESTORAGE_CONFIG_DIR` olarak bu veriliyor. Operatörün
  terminaldeki `claude` oturumu ayrı bir Keychain girdisi olarak duruyor ve
  Mimir'in çıkışından etkilenmiyor — bu ayrım olmasa "kapanınca sıfırla"
  operatörü kendi terminalinden atardı.
- **Daemon'un kendi model çağrıları da aynı hesabı harcıyor.** refine, distill
  ve recap artık CLI'ın varsayılan oturumunu değil bağlı hesabı kullanıyor;
  "bunu hangi hesap ödedi?" sorusunun tek yanıtı var. Hiçbir hesap bağlı
  değilken bu çağrılar CLI'ın kendi hatasıyla başarısız oluyor — sessizce başka
  bir kimliğe düşmüyor.
- **Kabuk profilleri tek profile indi.** `salihdevran` / `eziode` yerine tek bir
  `claude`, ve o kabuk Mimir'in yuvasına yönlendirilmiş halde açılıyor.

### Eklendi

- **Hesap bağlamak artık bir giriş akışı.** `POST /accounts/login` daemon'da
  `claude auth login`'i bir pty üzerinde başlatıyor, yetkilendirme adresini
  çıktısından okuyup **Chrome'da gizli pencerede** açıyor; `GET /accounts/login`
  akışı takip ediyor. Normal pencere tarayıcıda zaten açık olan Claude oturumunu
  taşıyor ve hangi hesapla bağlanıldığını hiç sormuyor — gizli pencerenin sebebi
  bu.
- **CLI'ın kendi tarayıcı açışı bir PATH shim'i ile susturuluyor** (yalnız o alt
  süreç için, `exit 0`). Shim'in *başarılı* olması önemli: CLI tarayıcının
  açıldığına inandığında `http://localhost:<port>/callback` yönlendirmesini
  seçiyor, yani giriş tarayıcıda bitince hiçbir şey yazmak gerekmiyor.
  Açamadığına inandığında kod yapıştırma yoluna düşüyor — o yol da destekleniyor
  (`POST /accounts/login/code`), ama olağan yol değil.
- **`POST /accounts/reset`** — çıkış yap, yuva dizinini sil, kaydı unut. Hem
  arayüzdeki "çıkış yap" düğmesi, hem masaüstü kabuğunun kapanırken çağırdığı
  yol. Daemon aynı sıfırlamayı kendi kapanışında **ve** açılışında da yapıyor:
  öldürülen bir daemon kapanış yarısını hiç çalıştıramıyor, launchd'nin
  daemon'u ise uygulama kapanınca durmuyor.
- **Yarıda kalan bir çalışma kaldığı yerden sürdürülebiliyor.** İnternet
  koptuğunda ya da bir conflict'e çarpıldığında çalışma `failed` olarak
  bitiyordu ve tek çare görevi baştan vermekti — yapılmış işin tamamı çöpe
  gidiyordu. `POST /coding-tasks/{id}/retry` kartı kuyruğa geri koyuyor:
  gövdesiz çağrı (**devam et**) satırın `session_id`'sini koruyor, dispatcher
  kartı yeniden aldığında CLI `--resume` ile açılıyor ve oturum kaldığı yerden
  sürüyor. `{"fresh":true}` (**baştan dene**) oturumu atıyor — CLI'ın artık
  sürdüremediği bir oturumdan çıkış yolu, tercih değil.
  - Sürdürülen oturuma kesintinin **nedeni** de veriliyor, görevle birlikte:
    `--resume` uzun ve yarım bir tool çağrısında bitmiş olabilen bir transcript
    oynatıyor, hedefin tekrar söylenmesi oturumun yanlış ipin ucuna
    yapışmasını engelliyor.
  - `init`'ten sonra çöken bir çalışma da artık oturumunu saklıyor. Sürdürecek
    tek şey oydu; kaybedilseydi her çökme görevi sıfırdan başlatırdı.
  - Board'da **failed** ve **durduruldu** kartlarında iki düğme, terminalin üst
    çubuğunda da aynı ikisi. Sürdürülecek bir oturum yoksa "devam et" hiç
    çıkmıyor: kaldığı yerden devam etmeyi vaat edip sessizce baştan başlamaktansa
    sürdürülemez demek daha dürüst.
  - Kapasite tek çalıştırma olduğu için, o an bir task koşuyorsa iki düğme de
    kartı **kuyruğa** alıyor — ve arayüz bunu tıklamadan önce söylüyor.

### Kaldırıldı

- `POST /accounts`, `DELETE /accounts/{id}`, `POST /accounts/scan`;
  `account.Discover` / `Sync` / `Register` / `Delete`; `config.ClaudeAccountsDir`
  ve `MIMIR_CLAUDE_ACCOUNTS_DIR`; arayüzdeki `AccountSelect` ve
  `identityClashes`.

## [2.11.1] — 2026-09-03

**İki hesap aynı anda açık kalıyor, ve `claude` artık her seferinde yeniden
erişim izni sormuyor.**

### Düzeltildi

- **Kabuğun ömrü artık soketin ömrü değil.** pty'yi websocket sahipleniyordu:
  her bağlantıda yeni bir `Start`, ve socket kapanınca `Close` kabuğa SIGHUP.
  Arayüz de aynı anda tek terminal mount ettiği için ikinci hesabı açmak
  birincinin viewer'ını unmount ediyor, o da soketi kapatıyor, o da **kabuğu
  öldürüyordu**. Artık oturumları `ptyterm.Registry` sahipleniyor: viewer
  ayrılır, kabuk çalışmaya devam eder. İki hesap aynı anda açık kalabiliyor.
- **Tekrar tekrar sorulan workspace-trust sorusu.** Aynı hatanın ikinci yüzüydü:
  öldürülen her oturumla birlikte, operatörün az önce verdiği "Yes, I trust this
  folder" cevabını taşıyan `claude` süreci de gidiyordu, ve profil her
  değiştiğinde sıfırdan bir `claude` başlıyordu. Oturum yaşadığı için cevap da
  yaşıyor.
- **`Registry.Kill` haritadan senkron siliyor.** Eskiden silme işini, kabuk
  gerçekten ölünce uyanan gözcü goroutine yapıyordu; bir oturumu öldürüp hemen
  "ne çalışıyor" diye soran çağıran, hâlâ çalıştığı cevabını alıyordu.

### Eklendi

- **Oturum geçmişi geri oynatma.** Bir profile yeniden bağlanan viewer, kabuğun
  o ana kadar söylediklerini (son 256 KiB) alıyor — profil değiştirip geri
  dönünce boş ekran değil, bıraktığın ekran geliyor.
- **`DELETE /terminals/{profile}`** — viewer kapatmak artık kabuğu bitirmediği
  için, bir oturumu kasten sonlandırmanın tek yolu. Kabuğu kapanmış bir panelde
  "yeniden başlat" düğmesi.

## [2.11.0] — 2026-09-03

**Bir lead artık aranabilir bir şey: telefonlar defterde, ve bir bölge tek bir
bölge.**

### Eklendi

- **İletişim zenginleştirme artık koşunun bir aşaması** (stage 1b). Daha önce
  yalnızca Export'ta çalışıyor ve cevabı hiçbir yere yazılmıyordu — yetmiş
  şirketlik bir defterde sıfır telefon olmasının sebebi buydu. Artık her koşuda
  çalışıyor ve `leads`'e yazılıyor, yani getirme şirket başına bir kez ödeniyor.
  Ölçülen: 0 → 40 telefon.
- **Web sitesi olmayanlar için DuckDuckGo araması.** Listede site yoksa enricher
  şirketin sitesini arayıp buluyor; dizin siteleri (Google, Facebook, firma
  rehberleri) eleniyor, çünkü küçük bir işletmeyi kendi adında geçen bu siteler
  onu geçiyor ve birini "web sitesi" diye kaydetmek hiç bulmamaktan kötü.
- **Bölge toplaması** (`GET /maps/leads/regions`). Seçici artık koşuları değil
  yerleri listeliyor: yirmi bir kez aranmış bir bölge tek bir "Denizli", yirmi
  bir satır değil. `GET /maps/leads?region=` ile filtreleniyor, büyük/küçük harf
  duyarsız — "denizli" ile "Denizli" iki ayrı yer değil.

### Değiştirildi

- **Ulaşılamayan şirketler `unknown` kategorisinde.** Ne telefonu ne sitesi olan
  bir şirket aranamaz, ve bir outreach listesi arayabildiklerinin listesidir;
  ticaretinin adı altında dosyalamak operatörü çıkmaz bir satıra götürürdü.
  `category_method` bunu `unreachable` olarak kaydediyor — sınıflandırma
  başarısızlığı değil, ulaşılamazlık.

## [2.10.0] — 2026-09-03

**Uygulamanın içindeki terminal artık gerçek bir terminal, ve bir modelin kotası
dolduğunda bölge sınıflandırması boş dönmüyor.**

### Eklendi

- **İnteraktif kabuk** (`internal/ptyterm`). Terminals ekranındaki `KABUK`
  bölümü, operatörün kendi giriş kabuğunu bir pty üzerinde çalıştırıyor —
  `$SHELL -l -i`, Terminal.app'in başlattığı sürecin aynısı. oh-my-zsh,
  eklentileri, prompt ve `.zshrc`'de tanımlı `claude-acct` fonksiyonu birebir
  çalışıyor, çünkü aynı rc dosyalarını okuyan aynı kabuk. İki profil:
  `salihdevran` prompt'a `claude`, `eziode` ise `claude-acct eziode` yazıyor.
  Komut exec edilmiyor, *yazılıyor* — `claude-acct` bir kabuk fonksiyonu, exec
  edilecek bir ikilisi yok, ve scrollback'te operatörün kendi yazacağı satır
  görünüyor. `GET /terminals/profiles`, `GET /ws/terminals/pty`.
- **İsim tabanlı kategori kuralı** (`CategoryForName`). Kazınan satırlarda
  Google'ın `types[]` etiketi yok, bu yüzden ücretsiz kural katmanı her zaman
  ıskalıyor ve her şirket modele düşüyordu; modelin kotası dolunca bütün bölge
  `unknown` dönüyordu. Artık isim ticareti açıkça söylüyorsa ("yazılım",
  "eczane") kural cevaplıyor. Ölçülen: 20 şirketin 18'i ücretsiz çözüldü.

### Düzeltildi

- **`claude` CLI'ın gerçek hatası artık görünüyor.** CLI kotası dolduğunda 1 ile
  çıkıyor, stderr'i *boş* bırakıp sebebi stdout'a JSON olarak yazıyor. Kod
  stdout'u atıp boş stderr'i raporladığı için "session limit" hatası "run
  `claude login`" tavsiyesine dönüşüyordu — düzeltmesi imkânsız bir tavsiye.
  Çıkış kodu sıfır olmasa da stdout okunuyor, ve 429 `ErrRateLimited` olarak
  auth hatasından ayrılıyor.

## [2.9.0] — 2026-09-03

**Hiçbir şey oturumla birlikte kaybolmuyor: bulunan işletmeler bir defterde,
konuşmalar olduğu gibi veritabanında, taranan dosyaların geçmişi kayıtlı.**

### Eklendi

- **Lead defteri** (task-63). Bir arama artık cevap verip unutmuyor. `leads`
  tablosu işletme başına tek satır tutuyor — kategorisiyle, TTL'siz, kimsenin
  okurken sildiği bir satır değil — ve `lead_runs` / `lead_run_members` hangi
  aramanın ne zaman neyi bulduğunu saklıyor. `companies` ve `region_searches`
  olduğu gibi kaldı: onlar önbellek, bu bir kayıt, ve ikisini aynı tabloya
  koymak süresi dolan bir satırın kaydı sessizce silmesi demekti. Aynı arama
  ikinci kez çalışınca `lead_runs` bir satır artıyor, `leads` artmıyor. Ücretsiz
  kazıma telefonu boş döndürdüğünde daha önce Places'ten gelmiş numara
  silinmiyor — `ON CONFLICT` dolu bir alanın üzerine boş yazmıyor.
  `GET /maps/leads`, `/maps/leads/categories`, `/maps/leads/runs`.
- **Kayıtlı işletmeler ekranı** (task-64). Lead-gen sekmesi artık boş bir panelle
  değil, defterle açılıyor — "elimde hangi işletmeler var" sorusunun dürüst
  cevabı bu. Süzme ve kategori sayımı daemon'da yapılıyor; ekranda duran sayfayı
  süzmek "baktığınız iki yüz satırda ara" demek olurdu. Koşu geçmişi bir süzgeç,
  taslak kararı satırın yanında.
- **Sohbet arşivi** (task-65). Claude Code oturumları, Terminals'taki kodlama
  koşuları ve agy konuşmaları artık **kelimesi kelimesine** veritabanında:
  `chat_turns` her turu, `chat_fts` de aranabilir hâlini tutuyor. Bugüne kadar
  yalnızca damıtılmış özet saklanıyordu ve ham metin `~/.claude/projects`
  altındaki JSONL dosyasında duruyordu — o dosya silinince konuşma da gidiyordu.
  Arşiv aynı ayrıştırmadan besleniyor (ikinci bir okuma yok, ikinci bir imleç
  yok) ve **hiç model çağırmıyor**, ki aylardır biriken bir geçmişi almak fatura
  değil taşıma olsun. `GET /chat/sessions`, `/chat/sessions/{id}`,
  `/chat/search`.
- **Dosya sürüm geçmişi** (task-67). Değişiklik tespiti zaten çalışıyordu — Brain
  her taramada dosyanın ham baytlarının SHA-256'sını karşılaştırıyor ve
  `~/development` ile `~/Documents` üzerinde on beş dakikada bir geçiyor — ama
  yeni değerlendirme eskisinin üzerine yazılıyordu. Artık her farklı içerik
  hash'i için bir satır: ne zaman, ne kadar büyüktü, ve o hâliyle ne anlama
  geliyordu. Dosya içeriği saklanmıyor; git zaten baytları tutuyor, tutmadığı
  şey okuma. Sürüm satırını `UpsertBrainNode` yazıyor, çağıran değil — eski hash
  yalnızca orada görünür, ve böylece her ingest yolu geçmişi bedavaya kazanıyor.
- **"Değişti, yeniden okundu"** (task-67, task-68). Tarama artık yeni bir dosyayı
  değişmiş bir dosyadan ayırıyor; konsolda ayrı bir satır olarak görünüyor, ve
  düğüm panelinde bir sürüm zaman çizelgesi var — bir satıra tıklayınca o
  sürümün değerlendirmesi açılıyor.

- **Lead-gen çalıştırmasında model seçimi.** Arama formuna iki açılır liste
  geldi: sağlayıcı (`agy` — ücretsiz, ya da `claude` — kotanızdan) ve o
  sağlayıcının modeli. Seçim, bir çalıştırmanın üç model aşamasının üçünü de
  birden bağlıyor (`RunRequest.Selection` → `Run`'ın başında `.With(sel)`), ki
  bir arama yarısı bir modelde yarısı başkasında bitmesin. Seçim yapılmadığında
  hiçbir şey değişmiyor: sınıfa göre yönlendirme hâlâ varsayılan.
  `GET /llm/providers` izin listesini yayımlıyor; `POST /maps/leadgen` bir
  `provider`/`model` çifti alıyor ve listede olmayanı 400 ile reddediyor —
  ikisi de bir alt sürecin argv'sine dönüştüğü için sessizce varsayılana
  düşmek yanlış cevap. Bir seçim erişilebilirlik yedeğini de kapatıyor:
  "bunu agy'de çalıştır" dedikten sonra sessizce claude'a geçmek, seçilmeyen
  bir bütçeyi harcamak olurdu. Aşamaların önbellekleri seçimle ad alanına
  ayrılıyor, yoksa farklı bir modele geçen çalıştırma öncekinin cevaplarını
  okur ve seçici hiçbir şey yapmamış gibi görünürdü.

### Düzeltildi

- **`could not reach the daemon: timeout: global`.** Tauri kabuğu her daemon
  çağrısına sabit 30 saniye veriyordu. Bir lead-gen çalıştırması ise bir bölgeyi
  kazıyıp her şirketi sınıflandırıp kategori başına bir sentez üretiyor —
  tasarımı gereği dakikalar sürüyor — ve bağlantı tüm bu süre boyunca açık
  duruyor. Sonuç hataların en kötüsüydü: iş bitiyordu, cevap çöpe gidiyordu,
  ekranda "daemon'a ulaşılamadı" yazıyordu. Artık bütçe rotaya göre veriliyor:
  pipeline rotaları 45 dakika, geri kalanı 2 dakika, bağlanma ise ayrı ve kısa
  (5 sn) — daemon loopback'te, bağlanamıyorsa ölüdür. Zaman aşımı mesajı da
  hangi çağrının ne kadar beklediğini söylüyor.
- **Ağ bütçeleri paket kaybeden bir hat için yeniden ölçüldü.** Yeniden iletim
  saniyeler yiyor ve eski değerler çalışan bir isteği başarısız bir aşamaya
  çevirecek kadar dardı: `SearchTimeout` 10→30 sn, `CrawlTimeout` 45→150 sn,
  `RefineTimeout` 60→180 sn, `ResearchTimeout` 120→360 sn, `GMapsPageTimeout`
  30→90 sn, `AgyPrintTimeout` 90→240 sn, `MapScrapeTimeout` 120→300 sn. Sabit,
  ayar düğmesi değil (SD-1). Yoklama bunun dışında tutuldu — yeni
  `LLMHealthTimeout` (20 sn): "bu CLI kurulu ve giriş yapılmış mı" sorusunun
  cevabı ya hemen gelir ya hiç, ve kullanılamayan tek bir sağlayıcının
  `/diagnostics`'i dakikalarca açık tutması gerekmiyor.

### Değişti

- **İki metin tavanı ayrıştırıcıdan tüketiciye taşındı.** `MaxPromptChars` (600)
  ve `MaxAssistantChars` (1200) artık `internal/memory.toRow` içinde
  uygulanıyor. Aynı ayrıştırmanın iki tüketicisi var ve talepleri zıt: özet satırı
  küçük kalmalı, arşiv ise tam olarak o kırpılan metni saklamalı. Ayrıştırıcı
  kırpsaydı arşiv her transkripti ikinci kez okumak zorunda kalırdı.

## [2.8.0] — 2026-09-03

**Lead-gen bir ekran değil, bir çalışma alanı oldu — ve kategoriler artık
gerçekten dolu.**

### Düzeltildi

- **Her şirket `unknown` kategorisine düşüyordu.** Kazınan satırlarda Google
  `types[]` yok, dolayısıyla kural katmanı boş dönüyor ve karar modele kalıyor;
  o model de task-51'den beri agy-only ve agy bu makinede girişsiz. Artık
  **yalnızca sınıflandırma** profili, sağlayıcı kullanılamıyorsa claude haiku'ya
  düşüyor. task-51'in kapattığı makine çapındaki distil yedeği kapalı kalıyor:
  bu yedek bir batch (yirmi şirket, birkaç yüz token) ve yalnızca operatörün
  başlattığı bir çalışmada devreye giriyor. Kötü bir cevap tekrar denenmiyor —
  o ikinci görüş olurdu, kullanılabilirlik değil.

### Değişti

- **Distil katmanı `gemini-3.8-flash-high`'a taşındı** (task-61). Sabitlenmiş bir
  model etiketini bumplamak kendi başına bir iştir (SD-5); `-high` soneki modelle
  birlikte taşındı, çünkü gerekçesi değişmedi. Bu aynı zamanda bir arızayı da
  kapatıyor: bu makinede distil çağrıları boş stderr ile `exit status 1` veriyordu,
  daemon'un birebir aynı çağrısı (şema ve scratch dizini dahil) 3.8'de `SUCCESS`
  dönüyor. Önbellekler geçersizleştirilmedi — 3.7 ile üretilmiş özetler hâlâ
  doğru özetler, ve `brain_nodes` her satırın hangi model tarafından yazıldığını
  zaten kaydediyor.
- **Şirket listesi.** Sol tarafta kategori rayı: her kategori, kaç şirket,
  kaçının web sitesi yok ve kütlenin nerede olduğunu gösteren iki piksellik bir
  metre. Sağda sıralanabilir, süzülebilir bir tablo — "web" sütunundaki *yok*
  ekrandaki tek sıcak renk, çünkü operatörün aradığı şey o. Seçilen satırın
  detayı yanda, seçili kategorinin boşluk analizi altta.
- **Taslak ekranı.** Karar listesi olarak bir kuyruk (önce kararsızlar), sağda
  tek seferde bir mektup — okunabilir ölçüde, ortalanmış, monospace değil.
  `↑↓` gez, `g` gönderildi, `a` atla; işaretlemek bir sonrakine geçirir.
- **Dürüst hata durumu.** Hiçbir şey sınıflandırılamadıysa başlık bunu söylüyor
  ve çalışmanın kendi gerekçesini yazıyor — tek bir `unknown` kovasını sonuç
  gibi göstermiyor.
- Ekranın metinleri Türkçeleşti; Brain ve Dashboard ile aynı dil.
- Sayma işleri `lib/leadgen.ts`'e taşındı ve test edildi: JSX içinde alınan
  karar, kimsenin denetleyemediği karardır.

## [2.7.0] — 2026-09-02

**Kazınan şirketler artık bir Excel dosyası, ve scraper'ın kırıldığı yerde bir
model duruyor.**

### Eklendi

- **Feed için model yedeği (claude haiku).** `internal/mapscrape`'in seçicileri
  hiçbir şey okuyamadığında — o markup Google'ın ve haber vermeden değişir —
  render edilmiş sayfa `internal/refine`'ın yeni `ExtractFeed` profiliyle
  yeniden okunuyor. Bir şey ayrıştırabilen akış modele hiç gitmiyor: seçici yolu
  id'yi ve koordinatı URL gramerinden okuyor, model bunu yapamaz. Kurtarılan her
  satır yine `mapscrape:` önekini taşıyor.
- **Üçüncü bir sağlayıcı.** `internal/mapsllm`: sidecar hiç ayağa kalkamayan
  makinede aynı Maps sayfası Crawl4AI ile çekilip aynı profille okunuyor. Sıra
  artık sidecar → model → Places, ve `regionsearch.Standard` bu sırayı tek yerde
  kuruyor.
- **İletişim zenginleştirme.** `internal/contacts` her şirketin kendi sitesini
  bir kez açıp telefon/e-posta çıkarıyor: önce `tel:`/`mailto:`/alt bilgi
  kalıpları, yalnızca onların bulamadığı sayfalarda claude haiku — ve modelin
  cevabı da aynı kalıplardan geçiriliyor, yani "beş altı yedi" diye bir telefon
  dosyaya girmiyor. Hiçbir alan tahmin edilmiyor; her satır hangi katmanın
  baktığını kaydediyor, böylece boş hücre "arandı, bulunamadı" demek oluyor.
- **Excel çıktısı.** `POST /maps/leadgen/export` bir `.xlsx` yazıyor: özet
  sayfası + **her kategori için bir sayfa**, her satırda ulaşım bilgileri ve
  "web sitesi var mı" sütunu. Masaüstünde "Excel'e aktar" düğmesi, iletişim
  tamamlama anahtarı ve "Finder'da göster" — Finder komutu yalnızca exports
  dizinini açabiliyor, WebView'a genel bir dosya açıcı verilmedi.

### Değişti

- `internal/regionsearch` sıralı bir sağlayıcı listesi tutuyor (ikili değil).
- `internal/refine` altı profile çıktı; iki yeni çıkarım profili distil yerine
  Reason (claude) sınıfında: ikisi de ancak daha ucuz bir şey başarısız olduktan
  sonra çalışıyor, ve en çok düşen katmana bağlı bir yedek yedek değildir.
- Yeni bağımlılık: `github.com/xuri/excelize/v2 v2.9.1` (sabit sürüm).

## [2.6.0] — 2026-09-02

**Bölgesel şirket araması artık hiçbir Google API anahtarı istemiyor.** Ücretsiz
scraper (`internal/mapscrape` + `deploy/playwright-maps`) task-28'den beri
duruyordu ama ulaşılamıyordu: `cmd/mimir-daemon` lead-gen pipeline'ını yalnızca
`PlacesAPIKey` varsa kuruyordu, yani anahtarsız makinede ne `/maps/*` rotaları
ne `maps_search` ne de lead-gen vardı.

### Düzeltildi

- **Anahtarsız makinede bölge araması diye bir şey yoktu.** Artık kaynak sırası
  tek bir yerde (`internal/regionsearch`) ve **önce bedava olan** deneniyor:
  yerel scrape birincil, faturalı Places yedek. Bunun iki sonucu var — anahtarsız
  makinenin tam bölge araması oluyor, ve anahtarsız yol artık her makinenin
  geçtiği yol, yalnızca anahtarsızların düştüğü test edilmemiş bir dal değil.
- **Sıra iki yerde yazılıydı ve ikisi aynı şeyi söylemiyordu.** Pipeline "Places,
  sonra scrape" diyordu, `maps_search`'ün kaydı ise "Places ya da hiç". İkisi de
  artık aynı router'ı kullanıyor.

### Eklendi

- `internal/regionsearch` — sağlayıcı sırası, sağlanabilirlik ve hangi kaynağın
  cevapladığı. Faturalı bir cevap bunu not olarak söylüyor.
- **Sidecar'ı daemon kendi başlatıyor.** Bağlantı reddedilirse
  `mapscrape.EnsureRunning` `docker compose up -d` çalıştırıp `/health`'i bekliyor
  ve istek bir kez tekrarlanıyor. Kimlik bilgisi istemeyen bir yetenek terminal de
  istememeli. Compose dosyası `scripts/install-agent.sh` ile binary'nin yanına
  kuruluyor — launchd ile başlayan daemon'un bulabileceği bir repo yok.
- `maps_search` her makinede kayıtlı; açıklaması hangi kaynağın cevaplayacağını
  söylüyor ("hiçbir şey harcamaz" / "faturalanır"), yanıt `source` ve notları
  taşıyor. `/diagnostics` `region_sources` + `region_search_free` bildiriyor.
- Masaüstü: sonuç başlığında `scrape · ücretsiz` / `places · faturalı` rozeti —
  boş telefon sütunu artık eksik veri değil, kaynak farkı olarak okunuyor.

## [2.5.0] — 2026-09-02

**Hesap ayrımı artık Mimir'de de çalışıyor.** Mekanizma task-37'den beri
doğruydu; eksik olan kayıttı, ve kayıt hiç yapılmamıştı: `accounts` tablosu boş
olduğu için hesap seçicide yalnızca "Otomatik" vardı, dispatcher tek bir
sentetik varsayılan yuva görüyordu ve ikinci kimlik hiç harcanmıyordu.

### Düzeltildi

- **İkinci hesap hiç kullanılmıyordu.** Bir yuva, ancak biri uygulamada dizinini
  seçtiğinde vardı. Artık `~/.claude-accounts` otorite: daemon açılışta tarıyor,
  `POST /accounts/scan` istek üzerine tarıyor, ve kayıt dizine göre idempotent.
  Kural kabuktakiyle birebir aynı (`claude-acct` / `claude-who`): varsayılan yuva
  = değişkenin *hiç* set edilmemesi, `default`/`a`/`salihdevran` adlı bir dizin de
  o varsayılana çöker. İki kimlik = aynı anda iki task.
- **Daemon'un kendi model çağrıları rastgele bir hesabı harcıyordu.**
  `internal/llm/claude.go` hiç `cmd.Env` kurmuyordu, yani refine/distill/recap
  daemon'u kim başlattıysa onun kimliğini — bir dev kabuğunda operatörün kendi
  oturumunu — harcıyordu. Artık `account.Environ` uygulanıyor ve harcanacak yuva
  işaretlenebiliyor (`POST /accounts/background`, boş id = CLI'ın varsayılanı).
  Aynı düzeltme oturum değişkenlerinin (`CLAUDECODE`, oturum id'si, mesajlaşma
  soketi) alt süreçlere sızmasını da bitiriyor.

### Eklendi

- `0015_account_slots.sql` — `accounts` üzerinde `discovered` ve `is_background`,
  ve en fazla bir arka plan yuvası olsun diye kısmi tekil indeks.
- `internal/account/discover.go` — `Discover` + `Registry.Sync`. Yalnızca ekler:
  dizini silinmiş bir yuvanın satırı kalır, çünkü ona iğnelenmiş bir run olabilir
  ve doğru rapor kırmızı bir probe'dur, kuyruğun altından kaybolan bir satır değil.
- `internal/config` — `ClaudeAccountsDir` (test override'ı
  `MIMIR_CLAUDE_ACCOUNTS_DIR`). Kabuğun kendi `CLAUDE_ACCOUNTS_DIR`'ı bilerek
  okunmuyor: daemon launchd altında onu görmüyor, okumak da hangi hesapların var
  olduğunu "daemon'u kim başlattı"ya bağlardı.
- Masaüstü: taramadan gelen satırda "unut" yerine "taramadan", her satırda "arka
  plan" işareti, ve yenile artık listelemeden önce tarıyor.

### Değişti

- **Taramadan gelen bir yuva uygulamadan unutulamıyor** (`ErrAccountDiscovered`,
  409). Dosya sistemi otorite: unutmak, bir sonraki taramanın geri aldığı bir söz
  olurdu. Kaldırmak dizini silmekle olur.

## [2.4.0] — 2026-09-02

**Brain yeniden kuruldu, ve ucuz iş ucuz modele taşındı.** Bir önceki sürümde
eklenen düğüm katmanı hiç çalışmıyordu; bu sürüm onu store'un üstüne yeniden
kuruyor ve aynı anda model çağrılarını tek bir çıkışın arkasına alıyor.

### Düzeltildi

- **Brain'in üç aracı da her çağrıda hata veriyordu.** Handler'lar düz `string`
  döndürüyordu; `internal/mcp/finalize.go` fail-closed olduğu için her çağrı
  `mcp: response not refined` ile düşüyordu. Testler yeşildi çünkü kanonik liste
  testi araçların yalnızca *adını* sayıyor, hiçbirini çağırmıyor — ve üç yanıt
  tipi `chokepoint_test.go`'ya, yani tam bu hatayı yakalamak için var olan teste,
  eklenmemişti. Dördü de artık orada.
- **Aynı kaynağı iki kez ingest etmek iki düğüm üretiyordu.** Kimlik `UnixNano`
  ile tuzlanmıştı, dolayısıyla bir repo iki kez okununca birbirine bağlanan iki
  ayrı düğüm çıkıyordu. Kimlik artık `(project_path, kind, source_key)`.
- **Özet başarısız olunca hiçbir şey kaydedilmiyordu.** Commit mesajı "bağlama
  hatası ingest'i düşürmüyor" diyordu ama özetleyici düşünce `IngestData` erken
  dönüyordu. Artık düğüm önce yazılıyor, sonra bağlanıyor: sağlayıcı yoksa düğüm
  başlığıyla ve etiketsiz duruyor, aranabilir kalıyor.
- **Testler ağa çıkıyordu.** Sahte bir `claude` yazan testler, distil'in birincil
  sağlayıcısı `agy` olunca PATH'teki gerçek binary'e gidiyordu. `e2e` bu yüzden
  29 saniye sürüyordu; şimdi 7. Boş bir `AgyCLIPath` artık `"agy"`ye
  varsayılmıyor, sağlayıcı kendini kullanılamaz ilan ediyor.

### Değişti

- **Model çağrıları sınıfa göre yönlendiriliyor** (`internal/llm`). Damıtma —
  sayfa özeti, episode recap'i, düğüm etiketi, sınıflandırma — `agy` ile
  `gemini-3.7-flash-low` üzerinde; sentez ve gap analizi `claude-haiku-4-5` ile;
  coding runner'a dokunulmadı. `agy` yoksa ya da kotası dolduysa router claude'a
  düşüyor. `docs/ROADMAP.md` §B.1'in ilk maddesi sahip kararıyla bu doğrultuda
  yeniden yazıldı.
- **İki kopya exec kodu teke indi.** `internal/refine` ve eski Brain aynı
  subprocess dansının iki elle yazılmış kopyasını taşıyordu ve ikisi çoktan
  ayrışmıştı. `internal/refine` artık `internal/llm`'i çağırıyor;
  `ErrClaudeUnavailable` sentinel'i yerinde duruyor.
- **Etiket çıkarımı metin ayrıştırmayı bıraktı.** `agy --json-schema` ile
  yapılandırılmış çıktı alınıyor. Eski kod `SUMMARY:` öneki bulamayınca ham
  yanıtı değerlendirme sanıp sıfır etiketle kaydediyordu — ve sıfır etiketli bir
  düğüm ne aramada ne kümelemede görünüyordu. Sıfır etiket artık reddediliyor.
- **Kümeleme ingest başına O(N)'den O(1) yazmaya indi.** Eski `LinkNode` diskteki
  her düğümü okuyor, tek ortak etiketi olan herkese kenar atıyor ve her komşunun
  dosyasını yeniden yazıyordu. Şimdi adaylar tek bir FTS sorgusundan geliyor,
  ucuz kenarlar yerel Jaccard ile hesaplanıyor, ve gerisine tek bir ilişki
  geçişi karar veriyor — kenarlar tek yönde, tek transaction'da yazılıyor.

### Eklendi

- **`brain_related`** — bir düğümden komşularına yürüyor, ikinci bir arama
  gerekmeden.
- **Alias'lar.** Damıtma, düğümün metninde geçmeyen eşanlamlı ve komşu terimleri
  de üretiyor ve bunlar FTS indeksine giriyor. Vektör indeksi olmadan semantik
  erişimin karşılığı bu: "corruption prevention" araması, o kelimelerin hiçbiri
  geçmeyen bir düğümü buluyor.
- **`MIMIR_GITHUB_TOKEN`.** Eskiden `os.Getenv("GITHUB_TOKEN")` istek yolunun
  ortasında okunuyordu (SD-1 ihlali). Artık `config.Load`'da, bir kez, ve
  `docs/SECURITY.md`'de ikinci operatör kimlik bilgisi olarak yazılı.

- **`make install-mcp`.** Bu repoda hiçbir şey mimir-mcp'yi bir istemciye
  kaydetmiyordu; `claude mcp add` yalnızca INSTALL.md'de elle yazılacak bir
  komut olarak duruyordu ve bu makinedeki sonucu, başka bir dizine geçince
  sessizce yok olan proje kapsamlı bir kayıttı. Artık tek komut: `claude` (user
  scope), `agy` + Antigravity IDE (ikisi aynı `mcp_config.json`'ı okuyor),
  `gemini` CLI ve VS Code. Dördünün de kendi `mcp add`'i var, o yüzden JSON'ları
  elle birleştirilmiyor. İkili checkout'ta bırakılmıyor, support dizinine
  kuruluyor: repo taşınınca dört yapılandırma birden kırılmasın.
- **Oturum ön-kontrolü.** Claude Code'da `SessionStart`, agy'de `PreInvocation`
  (agy'nin `SessionStart` olayı yok; `invocationNum` koruması olayı konuşma
  başına bire indiriyor). İkiliyi kontrol ediyor, istemcinin kaydı düşmüşse
  yeniden kaydediyor, daemon cevap vermiyorsa `launchctl kickstart` ile
  kaldırıyor — on yarım saniyelik deneme, sonra dürüst bir notla vazgeçiyor — ve
  modele bir hafıza olduğunu, repoyu yeniden okumadan önce `project_context`
  çağırmasını söylüyor. Oturumu asla bloklamıyor, her yolda exit 0.

- **Brain artık kendini kaydediyor.** Bir şeyin hatırlanması için modelin
  `brain_ingest_data` çağırmayı hatırlaması gerekiyordu — yani tam olarak
  güvenilmeyecek şey. Üç kaynak diskte zaten duruyordu ve üçü de **sıfır model
  çağrısıyla** okunuyor: M8'in çoktan damıttığı Claude Code episode'ları oturum
  düğümüne ve dokundukları her dosya için bir dosya düğümüne dönüşüyor; agy
  konuşmaları `Stop` hook'unun bıraktığı spool'dan çekiliyor; commit'ler
  `git log`'dan okunuyor. Operatörün gerçek store'unda tek tikte 147 oturum,
  371 dosya, 16 commit ve 1015 kenar.
- **Terminal işleri commit üzerinden yakalanıyor.** Her komutu kaydeden bir
  kabuk hook'u bilerek yapılmadı: gürültülü, komut satırına yazılan sırları
  toplar, ve bir hafta sonra hiçbirinin değeri kalmaz. Commit, birinin saklamaya
  değer bulduğu kısım ve zaten nedenini anlatan bir mesaj taşıyor.
- **`brain_scan_repo`.** Bir repoyu bir kez okuyup düğümlere çeviriyor, böylece
  bir oturumun tanımadığı bir dosya hakkındaki ilk sorusu dosyayı açmadan
  yanıtlanıyor. Bu paketteki tek bilerek pahalı iş, o yüzden `dry_run` faturayı
  harcamadan gösteriyor ve `content_hash` değişmemiş dosyayı bedavaya atlıyor:
  ikinci tarama hiç model çağrısı yapmıyor, yarıda kesilen tarama kaldığı yerden
  devam ediyor. Bu repoda ölçüldü: 365 dosya, elemelerden sonra 320; 6 dosyalık
  batch 60 saniye, yani ilk tam geçiş ~50 dakika.
- **Elemeler boyutla ilgili değil.** Kilit dosyaları, `testdata`/golden
  fixture'ları, fontlar, ikonlar ve minified bundle'lar gayet okunabilir ve
  sonraki bir oturuma hiçbir şey vermiyor — ama her biri, önemli bir dosyayla
  aynı maliyeti çıkarıyor.
- **Dosya düğümleri birikiyor.** Bir dosyaya elli oturum dokunduysa
  `brain_related` o dosyada "bu dosyaya ne oldu" sorusunu yanıtlıyor — başka
  hiçbir yüzeyin yanıtlamadığı bir soru.

### Şema

- `0014_brain_capture.sql` — `brain_nodes.content_hash` (bir yeniden taramanın
  değişmemiş dosyayı atlayabilmesi için) ve `brain_capture_state` (imleçler).
- `0013_brain.sql` — `brain_nodes`, `brain_edges`, `brain_fts`. Markdown dosya
  deposu tamamen kaldırıldı; `data/brain/` `.gitignore`'dan çıktı.

### Notlar

- `agy`'nin araç kısıtlama bayrağı yok. Yerine `--sandbox`, repo olmayan boş bir
  scratch çalışma dizini ve `MIMIR_NESTED=1` var — sonuncusu `mimir-mcp`'nin
  sıfır araçla açılmasını sağlıyor, yani global kayıtlı Mimir'a özyineleme
  kapalı. Bu, claude yolunun garantisinden zayıf ve `docs/SECURITY.md` bunu
  olduğu gibi yazıyor.
- `docs/CAPABILITIES.md` coding runner'ın `--mcp-config` ile Mimir'ı iç içe
  yüklediğini iddia ediyordu; kod hiçbir zaman öyle yapmadı. İddia kaldırıldı.
- Oturum etiketlerinde iki hata, ikisi de teste yakalandı. Etiketler
  `facts.commands`'tan türetiliyordu; orası komut satırı değil düzyazı açıklama
  tutuyor ("Run full make check"), yani ilk kelime bir fiil — neredeyse her
  oturum `check`, `find`, `read` etiketi alıyordu. Ve mutlak yollardan
  türetiliyordu, o yüzden ilk iki parça paket adı değil makinenin dizin düzeni
  oluyordu (`internal-store` yerine `repo-internal`). Artık yalnızca göreli
  yollardan.
- `/brain/*` route'ları ve `make brain-scan` planlanmıştı, yapılmadı: birincinin
  tüketicisi yok, ikincisi ise batch'li tarama sayesinde gereksiz kaldı — bir
  iş kuyruğuna, iş kimliğine ve ilerleme akışına ihtiyaç kalmadı.
- `-race`, okumakla görülmeyecek bir şeyi yakaladı: `Scan`, `Ingest`'i eşzamanlı
  çağırıyor ama ne `brain.Store` ne `brain.Completer` bunu şart koşuyordu. İkisi
  de artık koşuyor ve sahteleri korumalı.
  `desktop/src/lib/modules.ts` bu kuralı zaten yazıyor — gerçek bir route'a
  bağlı olmayan yüzey, kabuğun daemon hakkında yalan söylemesinin en hızlı yolu.

## [2.3.0] — 2026-09-02

**Model seçimi ve gerçek bir dashboard.** Bir task'ın hangi modeli harcayacağı
artık seçilebiliyor, ve uygulamanın açıldığı ekran ilk kez canlı veri gösteriyor.

### Eklendi

- **Task başına model.** `coding_runs.model` ilk günden beri vardı ama bir karar
  değil bir yankıydı: `Create` sabiti damgalıyor, `args()` aynı sabiti CLI'a
  geçiriyordu. Artık task yazılırken seçiliyor. Liste `internal/config`'te bir
  sabit (SD-1) ve `GET /coding-models` ile yayınlanıyor — masaüstündeki seçici
  o listenin görünümü, ikinci bir kopyası değil. Bilinmeyen model, üç saniye
  sonra ölen bir run değil, formdayken gelen bir 400.
- **Tam ad, takma ad değil.** `opus` "o an en yenisi" demek; backlog'da bir
  hafta bekleyen kart için yanlış sözleşme. Kart, o gün seçilen modelle çalışır.
- **Dashboard.** Açılış ekranı bir broşürdü — bir başlık, bir cümle, iki bağlantı
  — yani uygulamanın açıldığı ekran canlı verisi olmayan tek ekrandı. Artık
  sırayla: çalışan işler ve **canlı terminal çıktıları**, kuyruk, son bitenler,
  bağımlılık sağlığı, hesap doluluğu, günlük maliyet.
- **Çalışan her job kendi terminalini kendisi açıyor.** Eskiden bu yalnız
  operatörün tıkladığı işler için geçerliydi; kuyruktan çıkan ya da menü
  çubuğundan başlatılan bir iş kimse aramadan konsol açmıyordu. Artık poll
  döngüsü onları sahipleniyor. Elle kapatılan sekme kapalı kalıyor.
- **Bağımlılık sağlığı ilk ekranda.** Crawl4AI bir oturum boyunca kapalıydı;
  `/diagnostics` bunu — düzeltme komutunu adıyla vererek — söylüyordu ve kabuk
  hiçbir yerde göstermiyordu. Artık daemon'un kendi cümlesi olduğu gibi
  gösteriliyor. Uygulama hiçbir şeyi kendisi başlatmıyor: konteyner kaldırmak
  operatörün kendi makinesindeki kararı.

### Değişti

- **Tek poll döngüsü.** Board ve dashboard aynı diziyi okuyor (`RunsProvider`).
  Daemon'da projeler arası run rotası yok, yani bir board proje başına bir
  istek demek — bu fan-out bir kez yapılabilir, iki kez israf.
- `Dashboard.tsx`'ten `css`/`HoverDiv`/`HoverButton`, modül kaydı, yeni task
  formu ve diagnostics kancası ayrı dosyalara çıktı; iki ekran aynı formu
  paylaşıyor, ikinci bir kopya üretmiyor.
- `internal/api/AGENTS.md`: model listesi yayınlanır, aynalanmaz.
- `internal/coderunner/AGENTS.md`: model satırdan gelir, config'ten değil.

- **Aynı hesaba bağlı iki yuva uyarısı.** Bir yuva, CLI'ın hash'leyip Keychain
  girdisi adına çevirdiği bir dizin — zaten kullandığınız hesapla ikinci bir
  yuvaya giriş yaparsanız iki girdi, iki yeşil satır ve **tek** rate limit
  olur. Satırlardan anlaşılmıyor, artık hesap yöneticisi açıkça söylüyor.

### Düzeltildi

- **Bir hesap aynı anda iki task çalıştırabiliyordu.** `launch` run'ı sahipleniyor
  ama yuvayı meşgul olarak *başlattığı goroutine'in içinde* işaretliyordu; `pump`
  boş yuva kalmayana dek döndüğü için bir sonraki tur, çocuk henüz
  zamanlanmadan aynı yuvayı boş görüp ikinci bir run'ı aynı kimliğe
  bağlıyordu. Kuyrukta beş iş varken beşini birden alabiliyordu. Dispatch
  kilidi bunu kapatmıyor — pencere `launch`'ın dönüşü ile çocuğun
  zamanlanması arasında. Artık işaretleme goroutine'den önce, senkron yapılıyor.
  Regresyon testi düzeltme olmadan kesin olarak kırmızı.

### Notlar

- Crawl4AI bu makinede kapalıydı; sebep koddaki bir hata değil, Docker
  Desktop'ın çalışmıyor oluşuydu. `make crawl-up` sonrası `/diagnostics` yeşil.
- Model listesi bir sabit: nesil değiştiğinde elle güncellenir, tıpkı
  `CodingModel` ve `ResearchModel`'in bugün olduğu gibi.

## [2.2.0] — 2026-09-01

**İki Claude Code hesabı, iki paralel iş.** Bir task'ın hangi kimliği
harcayacağı artık seçilebiliyor ve kapasite bir sayı değil: her hesap aynı anda
tek task.

### Eklendi

- **Hesaplar.** Bir hesap = bir dizin. Claude Code kimlik bilgilerini macOS
  Keychain'de tutuyor ve hangi girdiyi kullanacağını
  `CLAUDE_SECURESTORAGE_CONFIG_DIR` yolundan türetiyor — yani dizin sadece bir
  hash girdisi, Mimir hiçbir kimlik bilgisi görmüyor. Projeler gibi: yol bir kez
  alınır, sonrası id ile taşınır. Yeni rotalar `GET|POST /accounts`,
  `DELETE /accounts/{id}`, `GET /accounts/{id}/status`.
- **Canlı kimlik sorgusu.** `claude auth status` her yuva için `email`,
  `orgName` ve `subscriptionType` döndürüyor ve hiçbir şey harcamıyor, bu yüzden
  hesap satırındaki bilgi kayıttan değil o andan geliyor. Login'i düşmüş bir
  hesap sağlıklı görünmüyor.
- **Hesap başına tek run.** `CodingMaxConcurrentRuns` sabiti kaldırıldı: aynı
  kimliği paylaşan iki run aynı rate limit'i ve aynı oturum durumunu paylaşır,
  yani ikincisi verim değil çekişme. Daha fazla kapasite = bir hesap daha
  kaydetmek.
- **Otomatik atama, isteğe bağlı sabitleme.** Task varsayılan olarak ilk boşalan
  hesaba düşer; istenirse bir hesaba sabitlenir. Dispatcher kuyruğun başını
  almak yerine kuyruğu yürüyor, böylece meşgul bir hesaba sabitlenmiş bir kart
  arkasındaki başka hesaba ait işi bekletmiyor.
- **Recents.** Terminals kenar çubuğunda varsayılan olarak kapalı bir bölüm:
  geçmiş oturumlar. Açıldığında transcript aynı soketten baştan oynatılıyor —
  ikinci bir depo değil, board'ın kendi listesi eksi zaten açık olanlar.

### Değişti

- Çocuk süreç ortamı artık miras alınmıyor, kuruluyor. Kimlik yuvası seçiliyor
  ve daemon'ı başlatan Claude Code oturumunun değişkenleri (`CLAUDECODE`,
  `CLAUDE_CODE_*`, `AI_AGENT`) temizleniyor — bunları okuyan iç içe bir CLI
  başkasının oturumunu sürdürdüğünü sanıyordu.
- `internal/api/AGENTS.md`'deki "dosya sistemi yolu tam olarak tek rotada kabul
  edilir" kuralı "tam olarak iki rotada" oldu (`POST /projects`,
  `POST /accounts`). Kuralın koruduğu şey değişmedi: yol bir kez, kaydı sırasında,
  o kararın sahibi olan paket tarafından doğrulanır.

### Şema

- `0012_accounts.sql` — `accounts` tablosu (`config_dir` üzerinde tekil indeks)
  ve `coding_runs`'a `requested_account_id` + `account_id`. İkisi ayrı: biri
  operatörün sabitlemesi, diğeri run'ın gerçekten çalıştığı yuva.

## [2.1.0] — 2026-09-01

**Coding task artık bir yaşam döngüsü.** Bir görev yazılıp bekletilebiliyor,
kuyruğa alınabiliyor, durdurulabiliyor; görsel eklenebiliyor; ve çalışan her
job'ın kendi terminali var. Bu sürümün asıl konusu, "çalışıyor yazan ama
çalışmayan" kartların kökünü kurutmak.

### Eklendi

- **Backlog ve kuyruk.** `POST /coding-tasks` artık `start: false` kabul ediyor:
  kart önce yazılıyor, token ancak biri çalıştırdığında harcanıyor. Yeni
  statüler `backlog`, `queued`, `stopped`; yeni rotalar
  `POST /coding-tasks/{id}/enqueue`, `POST /coding-tasks/{id}/stop`,
  `DELETE /coding-tasks/{id}`. Kuyruk SQLite'ta durduğu için daemon yeniden
  başladığında da yerinde kalıyor ve kaldığı yerden dağıtılıyor.
- **Eşzamanlılık sınırı.** Aynı anda en çok `CodingMaxConcurrentRuns` (2) run;
  üçüncüsü kuyrukta bekliyor. Dispatcher `internal/coderunner`'ın bir metodu —
  ikinci bir orkestratör değil, `docs/ROADMAP.md`'nin terk ettiği DAG/kanban
  ayrımına dönüş yok.
- **Run durdurma.** Önce SIGINT (CLI kendi `result` satırını yazabilsin diye),
  `CodingStopGrace` sonra SIGKILL, ikisi de sürece değil süreç grubuna — yoksa
  `claude`'un çocukları stdout borusunu açık tutuyor.
- **Görsel eki.** `POST /coding-tasks/attachments` + `GET .../{id}`. Tür,
  istemcinin dosya adına değil baytlara bakılarak (`http.DetectContentType`)
  belirleniyor; yalnız PNG/JPEG/GIF/WebP. Dosyalar store'un yanındaki
  `attachments/` dizinine yazılıyor, prompt'a yol olarak enjekte ediliyor ve
  o dizin `--add-dir` ile okunabilir kılınıyor.
- **stderr artık akıyor.** CLI'ın stderr'ı `stderr` olayı olarak yayınlanıyor ve
  transcript'e yazılıyor. "run `claude login`" gibi bir run'ın neden hiç
  çalışamayacağını söyleyen tek yer burasıydı ve hiçbir yere ulaşmıyordu.
- **Terminals ekranı.** Çalışan her job kendi sekmesini alıyor: canlı satırlar,
  scrollback, kopyala, STOP. Oturumlar `Dashboard`'ın üstünde tutuluyor, yani
  ekran değiştirmek soketi öldürmüyor. PTY değil — daemon `claude`'u borularla
  çalıştırıyor, gösterilebilecek dürüst şey o akış ve stderr.
- **Board artık çalışıyor.** Beş kolon (Backlog · Queued · Running · Done ·
  Failed), her zaman görünür "+ Yeni task", kart üzerinde run/stop/sil/terminal,
  ve Backlog↔Queued arası sürükle-bırak. Yoklama aralığı panoya göre değişiyor.

### Düzeltildi

- **Yeniden başlatmadan sağ çıkan "running" satırları.** Daemon açılışta
  `Resume` ile bunları `failed` + "the daemon restarted while this run was in
  flight" yapıyor. Bir run'ın sonucu artık `context.WithoutCancel` ile
  yazılıyor: kapanış sırasında biten her run satırını kaybediyordu ve sonsuza
  kadar `running` kalıyordu.
- **Görünmez WebSocket hataları.** `socket.onerror`'ın yazdığı sebebi
  `socket.onclose` siliyordu; bağlanamayan bir soket tamamen sessizdi. Ayrıca
  `openRunStream`'in reddi yutuluyordu (`void … .then(…)`), rozet sonsuza kadar
  "running" kalıyordu. Soket terminal olay görmeden kapanırsa artık
  `GET /coding-tasks/{id}` ile yoklamaya düşülüyor.
- **`/coding-tasks` rotaları `Runner` yokken de kayıtlıydı** ve nil-interface
  çağrısı 500 üretiyordu; `/ws/runs/{id}` koruması `Transcripts`'i kontrol
  etmiyor, geçmişsiz soket sunuyordu.
- **Yalan söyleyen bağlantı göstergeleri.** Başlıktaki yeşil nokta, overlay'deki
  "ready" ve kart etiketleri sabit yazılmıştı; artık gerçek daemon durumundan
  geliyor.
- **Klasör seçicinin sessiz hatası** (try/catch dışındaydı), başarısız bir
  `start`'ın önceki run'ı boş panelle yeniden çizmesi, ve run akarken "Start
  run"ın yeniden etkinleşip ilk akışı terk etmesi.
- `consume`'un yuttuğu `scanner.Err()` artık başarısızlık nedeni olarak
  raporlanıyor; zaman aşımı da kendi mesajını alıyor.

### Şema

- `0011_coding_task_lifecycle.sql` — `coding_runs`'a `title`, `created_at`,
  `queued_at`, `attachments` kolonları ve `(status, queued_at)` indeksi.
  `started_at = 0` artık "hiç başlamadı" demek.

## [2.0.0] — 2026-09-01

**GOAT artık Mimir.** Marka adı, tüm görsel sistem ve kodun içindeki her
tanımlayıcı Mimir Studio Brand System Guide'a göre yeniden yazıldı. Sürüm
numarası major: ikili adları, ortam değişkenleri, store yolu ve launchd
label'ı değişti — eski kurulum bu sürümle konuşmaz.

### Değişti — isimler

| Eski | Yeni |
|---|---|
| `bin/goat-mcp` · `bin/goat-daemon` | `bin/mimir-mcp` · `bin/mimir-daemon` |
| `github.com/logrenant/goat-mcp` | `github.com/logrenant/mimir` |
| `GOAT_DAEMON_PORT` · `GOAT_DAEMON_TOKEN` · `GOAT_*` | `MIMIR_*` |
| `com.goat.daemon` · `com.goat.desktop` | `studio.mimir.daemon` · `studio.mimir.app` |
| `~/Library/Application Support/goat-mcp/goat.db` | `~/Library/Application Support/mimir/mimir.db` |
| `~/Library/Logs/goat-daemon.log` | `~/Library/Logs/mimir-daemon.log` |
| `goat.bearer.<token>` (WebSocket alt protokolü) | `mimir.bearer.<token>` |
| `sessionlog` wire string `goat_run` | `mimir_run` (migration `0009`) |

`goat v1` adı yalnızca emekli Node öncülünü anlatan tarihsel pasajlarda kaldı;
o bir kayıt, marka kullanımı değil.

### Değişti — görsel sistem

- **Palet**: Carbon `#101114` · Mist `#eef0f2` · Electric `#2547e8` · Lime
  `#c6f04a`. Dört renk, beşincisi yok; arayüz yapısı yalnızca Carbon/Mist
  tonlarından kuruldu. Eski altın aksan Electric'e, yeşil "tamamlandı" Lime'a
  döndü. Hata kırmızısı palet dışı tek renk ve bilerek öyle: dekorasyon gibi
  okunan bir hata, beşinci renkten daha kötü.
- **Tipografi**: Aldrich (display, yalnızca büyük harf başlıklar ve etiketler)
  + Open Sans (gövde, 300/400/600). İkisi de OFL, `desktop/src/assets/fonts/`
  altında woff2 olarak gömülü — uygulama dışarı font istemiyor, offline
  çalışıyor, latin-ext ile Türkçe karakterler tam.
- **Marka**: ürün arayüzünde wordmark, ikonlarda altı kollu asterisk. İkisi de
  marka SVG'lerinin kendi path verisinden geliyor (`desktop/src/components/brand.tsx`),
  tek düz renkte çiziliyor.
- **İkonlar**: `scripts/make-icons.py` menü çubuğu template ikonunu, 1024px
  uygulama ikonunu, `.icns` setini ve favicon'u tek geometriden üretiyor.

### Eklendi

- `scripts/install-agent.sh` kurulumda eski store'u yeni yola **taşıyor**
  (`sqlite3 .backup` ile tutarlı anlık görüntü; eskisi yedek olarak yerinde
  kalır). Kayıtlı projeler, run geçmişi ve proje hafızası korunur.
- Store migration `0009`: `memory_episodes.source_kind` satırlarında
  `goat_run` → `mimir_run`.
- Store migration `0010`: run transkript yolları (`coding_runs.transcript_path`,
  `memory_episodes.source_path`, `memory_ingest_state.source_path`) yeni
  dizine yazıldı; kurulum betiği transkript dosyalarını da kopyalıyor. Böylece
  eski `goat-mcp` klasörü gerçekten silinebilir hale geliyor.

### Düzeltildi

- `test/e2e` sürüm dizesini sabit `0.1.0` olarak bekliyordu ve 1.1.0'daki
  sürüm bump'ından beri kırıktı — Go test cache'i maskelemişti. Artık
  `mcp.Version` sabitini okuyor, bir daha eskiyemez.

## [1.1.1] — 2026-09-01

### Eklendi

- Uygulama ilk çalıştırmasında kendini **login item** olarak kaydediyor
  (`~/Library/Application Support/mimir/.autostart-initialized` işaretiyle
  bir kez). Daemon zaten login'de geliyordu; menü çubuğu gelmeyince operatörün
  elinde çalışan bir sistem ve ona giden bir kapı kalmıyordu. Sonrasında karar
  tray'deki anahtarın.

## [1.1.0] — 2026-09-01

GOAT artık "açınca çalışan bir uygulama" değil, sistemde sürekli çalışan bir
servis ve menü çubuğundan tek kısayolla erişilen bir giriş noktası.

### Eklendi

- **launchd agent** (`scripts/install-agent.sh`, `make install-agent`) —
  `mimir-daemon` login'de başlar, ölürse `KeepAlive` ile geri gelir, uygulamadan
  bağımsız yaşar. Port + token kurulumda üretilir; `endpoint.json` ve plist
  ikisi de `0600`. `make agent-status` / `agent-logs` / `agent-restart` /
  `uninstall-agent`.
  - plist `PATH`'i genişletir: launchd'nin verdiği `/usr/bin:/bin:/usr/sbin:/sbin`
    ile `internal/refine` ve `internal/coderunner`'ın `claude` CLI'yi bulması
    mümkün değil.
- **Menü çubuğu uygulaması** — Dock ikonu yok (accessory), tray'de canlı daemon
  durumu, `New task…`, `Open GOAT`, `Restart daemon`, login'de başlatma anahtarı
  ve `Quit GOAT`. Uygulamadan çıkmak daemon'ı durdurmaz; pencereyi kapatmak
  gizler.
- **Hızlı task penceresi (⌘⇧G)** — son kullanılan projeye varsayılan, prompt
  yaz `⏎` ile başlat; canlı akış aynı pencerede. Pencereyi kapatmak run'ı iptal
  etmez, bitince sistem bildirimi gelir. Saf karar mantığı
  `desktop/src/lib/quickTask.ts` içinde, testli.

### Değişti

- **Masaüstü kabuğu artık attach-first.** `endpoint.json` varsa launchd'nin
  daemon'ına bağlanır (sağlıksızsa `launchctl kickstart -k`), asla ikinci bir
  daemon doğurmaz — tek SQLite store'a iki yazar olmasın diye. Dosya yoksa
  eskisi gibi kendi çocuğunu başlatır (`make desktop-dev` yolu).
- `endpoint.json` bir girdi olarak doğrulanır: `0600` değilse veya `base_url`
  loopback değilse **reddedilir**, okunmaz.
- Sürüm dizesi tek kaynaktan (`internal/mcp.Version`) geliyor ve git etiketiyle
  aynı: `/healthz`, `diagnostics` ve tray durum satırı aynı numarayı gösterir.
- Bundle hedefi yalnızca `app`; `.dmg` adımı Finder otomasyon izni istiyor ve
  GOAT dağıtılmıyor, kopyalanarak kuruluyor.

## [1.0.0] — 2026-09-01

İlk sürüm etiketi: bugüne kadar inşa edilmiş ve çalışan sistemin tamamı.
`make check` ve `make desktop-check` yeşil.

### Eklendi

- **`bin/mimir-mcp`** — Claude Code oturumu için yerel, sıfır maliyetli MCP
  sunucusu. Araçlar: `web_search`, `fetch_page`, `research`, `diagnostics`,
  `ecommerce_product_lookup`, `tiktok_profile_lookup`, `gmaps_business_lookup`,
  `instagram_profile_lookup`, `maps_search` (yalnızca Places anahtarıyla) ve
  proje hafızası araçları `project_context`, `context_recall`, `context_remember`.
- **`bin/mimir-daemon`** — yalnızca loopback dinleyen, uzun ömürlü HTTP servisi:
  klasör kapsamlı coding-task koşucusu (canlı akış), Google Maps lead-gen
  hattı ve aynı MCP kayıt defterinin `/mcp` üzerinden sunumu.
- **`desktop/`** — Tauri + React kabuğu: bağlantı el sıkışması, Workspace
  (klasör seç → görev ver → akışı izle), Leadgen (bölge araması → kategorilendirme
  → boşluk analizi → e-posta taslakları).
- **Proje hafızası (M8)** — Claude Code oturum transkriptlerini damıtıp
  proje başına aranabilir bağlam olarak geri veren `internal/{sessionlog,memory}`.
- Bağımlılıkların tamamı tam sürümle sabitlendi (SD-5); Crawl4AI ve Playwright
  Maps yardımcı konteynerleri `deploy/` altında sabit imajlarla tanımlı.

### Notlar

- Bu sürümde `mimir-daemon`'ın ömrü masaüstü penceresinin ömrüne bağlıdır:
  kabuk her açılışta port + token üretip daemon'ı çocuk süreç olarak başlatır.
  Sürekli çalışan servis ve menü çubuğu 1.1.0'da gelir.
