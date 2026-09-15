# task-99 — Sır kasası ve API taşımaları

- **Status:** done
- **Owner agent:** Coder (Gemini)
- **Prerequisites:** task-97
- **Primary paths:** `internal/secrets/**`, `internal/llm/**`, `internal/connections/**`, `internal/store/**`, `internal/api/**`, `docs/SECURITY.md`
- **Roadmap bucket:** §B — model yönlendirme

## Context

task-97 bağlantıyı yönlendirmenin birimi yaptı ama yalnız CLI adaptörleriyle.
Operatörün istediği, CLI aboneliği ile API anahtarının **ayrı ayrı**
eklenebilmesi: "Claude Code CLI" aboneliği harcar, "Anthropic API" ayrı
faturalanır, ve ikisi aynı anda var olabilmeli.

Bu, deponun bugüne kadar tutmadığı bir şeyi getiriyor: **operatörün yazdığı bir
kimlik bilgisi.** `docs/SECURITY.md` bunu açıkça reddediyordu ve doğruydu —
CLI'lar operatörün kendi oturumlarını sürüyor, `internal/account` bir sır
görmüyor: `claude auth login`'i bir dizine yöneltip anahtarlığın gerisini
saklamasına bırakıyor.

### Cross-platform sır saklama — araştırıldı

Sektörün cevabı Electron'un `safeStorage`'ı (VS Code, Slack, Discord): OS
deposu varsa o — macOS Keychain, Windows DPAPI, Linux Secret Service — yoksa
belgelenmiş bir yedek. **Kopyalanmaya değer olan şey şifreleme değil,
`getSelectedStorageBackend()`'in hangi arka uca düştüğünü *bildirmesi*.**

Bu makinede ölçüldü: `security` ile yazılan bir öğe aynı ikili tarafından
**istem çıkmadan** okunuyor (ACL `/usr/bin/security`'ye güveniyor ve iki işi de
o yapıyor), silme dahil tam tur çalışıyor.

## Scope

1. **`internal/secrets`** — `Vault` arayüzü, OS deposu ve `0600` dosya yedeği,
   ve `Backend()`. ✅
2. **Store + kayıt CRUD** — operatörün kendi bağlantıları için migration ve
   `internal/connections` yazma yolu.
3. **API adaptörleri** — `anthropic-api`, `gemini-api`, `openai-compatible`.
4. **Katalog** — base URL'ler, `Discovered` model listeleri.
5. **Rotalar** — `POST/PATCH/DELETE /llm/connections`, `/probe`.
6. **`docs/SECURITY.md`** yeniden yazımı.

## Definition of Done

- [x] Kasa OS deposunu deneyip düşerse dosyaya iniyor ve **hangisi olduğunu
      söylüyor**.
- [x] Dosya kasası `0600`, dizini `0700`, ve create-sonra-chmod değil
      **doğrudan 0600 yaratılıyor**.
- [x] Bozuk bir kimlik dosyası sessizce atılmıyor.
- [x] Katalog yayınlanıyor: çalışan ve **yakında** olanlar bir arada.
- [x] Genişleme noktası gerçek: `POST /llm/connections` var, final gövdeyi
      alıyor, ve hazır olmayanı **adıyla** reddediyor (501).
- [x] Arayüz final duruma göre: eklenebilecekler listesi, "yakında" rozetleri,
      ve her satırın kendi gerekçesi.
- [ ] API adaptörlerinin kendisi — bir hesaba karşı sınanabildiğinde.
- [ ] `docs/SECURITY.md` yeniden yazımı — ilk API bağlantısıyla birlikte.
- [x] `make check` yeşil.

## Changelog — bu turda inen

- **`internal/secrets`**, ve tasarımın can alıcı yeri: **`Backend()` arayüzün
  parçası.** Yedeğin kendisi güvenlik zaafı değil; **onu gizlemek** zaafiyet.
  Anahtarı dosya kasasına düşen operatör bunu *eklerken* okuyor. Electron'un
  `basic_text` bildirimi tam olarak bu.
- **OS deposu varsayılmıyor, sınanıyor.** "Anahtarlık var mı" sorusu platformdan
  cevaplanamaz: başsız bir Linux oturumunda D-Bus Secret Service yok, kilitli
  bir anahtarlık reddeder, bir konteynerde ikisi de yok. Sonda yazar, geri okur,
  siler — ve arkasında satır bırakmaz.
- **Dosya kasası `settings.writeFile`'ı yeniden kullanmıyor.** O yazıcı `0644`
  chmod'luyor; bir ayar dosyası için doğru, bunun için tam olarak fark eden
  kadar yanlış. Dosya `0600` **yaratılıyor**, sonradan chmod'lanmıyor: ikisi
  arasında dosyanın var olup okunabildiği bir pencere var.
- **Bozuk bir kimlik dosyası onarılmıyor ve atılmıyor.** Ayrıştırılamayan bir
  dosya, birinin elle düzenlediği bir dosya olabilir; sessizce sıfırdan
  başlamak içindeki her anahtarı kaybetmek olurdu.
- **`List` yok.** Bu sistemde sırları saymak için bir sebep yok, ve bir sayıcı
  sızıntının aldığı şekildir.
- **Bağımlılık: `github.com/zalando/go-keyring v0.2.8`**, tam sürüme pinli
  (SD-5). Gerekçesi: cross-platform şart ve üç arka ucun ikisini
  doğrulayamıyorum. macOS'u ölçebilirim; Windows DPAPI ve Linux Secret Service
  syscall'larını **elle yazmak, bu oturum boyunca kaçındığım "hafızadan yazma"
  hatasının ta kendisi olurdu.** Bakımı yapılan, gerçek kullanıcıları olan bir
  kütüphane bu ikisini benim yazabileceğimden iyi taşıyor. Ayak izi: iki
  geçişli bağımlılık, ikisi de platforma kilitli (`wincred` Windows, `godbus`
  Linux), artı zaten var olan `x/sys`. cgo yok.

## Katalog kararı — sınayamadığım hiçbir şeyi uygulamadım

Operatörün kararı, ve doğrusu buydu: **sınanamayan sağlayıcılar "yakında"
olarak işaretlenir, ama API genişletilmeye hazır bırakılır.**

- `internal/llm/catalogue.go` on üç sağlayıcıyı tanıyor: dördü çalışıyor (bu
  makinedeki CLI'lar), dokuzu `coming-soon`. Bir satır eklemek hiçbir şeyi
  çalıştırmıyor — ürünün ne olacağını söylüyor, ki bu farklı ve daha küçük bir
  vaat.
- `Adapter.Implemented()` adı ilan edilenle yazılanı ayırıyor. API adaptörleri
  **isim olarak var**: bu, tel şeklini ve seçiciyi şimdiden nihai yapıyor, yani
  birini eklemek bir tabloyu ve bir adaptörü değiştiriyor, başka hiçbir şeyi
  değil.
- **`POST /llm/connections` bugün her şeyi reddediyor, ve bu şeklin çalışması.**
  Katalog girdisini çözüyor, adını ve sebebini döndürüyor (501 — sunucunun
  yapamadığı, isteğin yanlış olduğu değil). Bir adaptör indiğinde bu işleyici
  rotası, gövdesi ve onu çağıran ekran değişmeden başarılı olmaya başlıyor.
- **`coming-soon` bir girdi bilinçli olarak base URL ve docs URL taşımıyor.**
  Onlar başkasının servisi hakkında olgular, ve bir tabloda doğrulanmamış bir
  olgu tam olarak bu yaklaşımın kaçındığı şey. Adaptörle, ona karşı sınanmış
  olarak geliyorlar. Testi var: yanıt gövdesinde `https://api.` geçemez.

## Bir sapma ve gerekçesi

Adaptörler yazıldığında **SDK mi ham HTTP mi** sorusu cevaplanmalı.
`claude-api` referansı varsayılan olarak resmî Anthropic SDK'sını istiyor; bu
depoda ise üç kardeş adaptör olacak, ve birine vendor SDK'sı çekip diğer ikisini
elle yazmak tutarsız, üçüne de çekmek bilinçli olarak dar bir `go.mod`'a üç ağır
ağaç eklemek. Önerim ham HTTP: üçünde tek şekil, yalnız `net/http`. Karar
adaptör task'ında verilecek ve orada yazılı olacak.
