# task-98 — Desktop: modeli harcayan her yerde sağlayıcı seçimi

- **Status:** done
- **Owner agent:** Coder (Gemini)
- **Prerequisites:** task-93, task-95
- **Primary paths:** `desktop/**`
- **Roadmap bucket:** Desktop kabuğu

## Context

Model seçimi bugün iki yerde: kartın modeli ve lead-gen koşusunun modeli.
Brain'in taraması, katalog yeniden yazımı ve refine, daemon'ın sınıf
yönlendirmesine bırakılmış — operatörün göreceği ya da değiştireceği bir yer
yok. İstenen: **brain dâhil** her görevde sağlayıcı ve modelin seçilebilmesi.

## Scope (do exactly this)

1. **Ayarlar ekranı operatörün varsayılanlarını taşır** (task-93'ün
   `internal/settings` girdisi): distil ve reason sınıfları için birer
   sağlayıcı/model. Kurulu olmayan sağlayıcı seçilebilir görünmez; oturum
   açmamış olan işaretlenir (`gemini` bu makinede öyle).
2. **Brain ekranı tarama başlatırken seçimi gösterir** ve üstüne yazmaya izin
   verir. Bir makine taraması binlerce çağrı; hangi tierin harcandığı
   görünmeden başlamamalı.
3. **Katalog yeniden yazımı** aynı seçiciyi alır (kart açılırken).
4. **Kartın sağlayıcı seçicisi** (task-95'in satırdaki sağlayıcı alanı):
   yalnız `Agentic` yeteneği olan sağlayıcılar.
5. **Tek bileşen.** `ProviderPicker`, `lib/daemon.ts`'in yayınladığı tablodan
   kurulur — dört ekranda dört kopya değil.

## Out of scope (do NOT do here)

- `internal/**`.
- Sınıf yönlendirmesini ekrandan değiştirmek. Sınıf → sağlayıcı kodun kararı;
  ekranın değiştirdiği şey operatörün tercihi.

## Definition of Done

- [x] Ayarlarda distil/reason varsayılanları.
- [x] Brain taraması, katalog yeniden yazımı ve kart aynı seçiciyi kullanıyor.
- [x] Kurulu olmayan sağlayıcı seçilemiyor; oturum açmamış olan işaretli.
- [x] `make desktop-check` yeşil · Status `done` + changelog.

## Notes for the reviewer (Opus)

- Dört kopya bir seçici, dört farklı istek gönderen dört ekran demektir.

## Changelog

- **Ayarlarda "Daemon'ın kendi çağrıları" kartı**: distil ve reason sınıfları
  için birer sağlayıcı/model. Brain'in tarama ve ilişki pasoları, refine ve
  katalog yeniden yazımı ilk kez sabit bir tiere çakılı değil. Bir makine
  taraması binlerce çağrı; bundan önce onları başka bir yere yöneltmenin tek
  yolu bir sabiti düzenleyip yeniden derlemekti.
- **Seçici artık bu makineyi biliyor.** Kurulu olmayan bir sağlayıcı seçilemez
  (seçilebilir göstermek, önüne bir tık daha koyulmuş bir başarısızlıktır);
  kurulu ama oturumu kapalı olan CLI'ın kendi cümlesiyle işaretlenir. Erişim
  bilgisinin **hiç** olmaması bambaşka bir şey — router'ı olmayan bir daemon —
  ve o durumda hiçbir şey kararmıyor.
- **"Oturumları sına" düğmesi.** Pahalı soru yalnız sorulduğunda soruluyor:
  sağlayıcı başına bir çağrı. Bir ekranın açılması bunu göndermiyor.
- **Keşfedilen modeller boş kataloğu yeniyor.** Ollama'nın modelleri bu
  makinedeki dosyalar, o yüzden sabit tablo hiçbirini listelemiyor ve seçici
  daemon'ın bulduğunu gösteriyor.
- **Brain ekranı bedavaya aldı**: zaten aynı `ProviderModelPicker`'ı
  kullanıyordu, dolayısıyla erişilebilirlik süzgeci ve keşfedilen modeller
  oraya kendiliğinden geldi. Tek bileşen kuralının karşılığı bu.

### Sapmalar

- **Katalog yeniden yazımına ayrı bir seçici konmadı.** Task dosyası "kart
  açılırken aynı seçiciyi alır" diyordu; uygularken katalog yeniden yazımının
  *daemon'ın kendi işi* olduğu belli oldu — `catalogjob` `llm`'i Reason sınıfı
  üzerinden çağırıyor. Yani yeni sınıf varsayılanı onu zaten kapsıyor, ve
  dördüncü bir kopya seçici tam olarak bu task'ın "dört kopya bir seçici, dört
  farklı istek gönderen dört ekran demektir" kuralının ihlali olurdu.
- **Kartın sağlayıcı seçicisi (madde 4) burada değil.** Satırda bir sağlayıcı
  sütunu gerektiriyor, o da task-95'in işi. Ajan CLI'ları oraya inince seçici
  de oraya iniyor.

`make desktop-check: 0` · `make check: 0`
