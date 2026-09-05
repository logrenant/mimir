# task-69 — Tarama izinleri: kökler operatörün, sırlar hiç okunmuyor

- **Status:** done
- **Owner agent:** Claude Opus (reviewer, B-1 takibi)
- **Prerequisites:** task-49, task-51 (kökler ve daimî tarama)
- **Primary paths:** `internal/settings/scan.go`, `internal/brain/exclude.go`,
  `internal/brain/scan.go`, `internal/brain/supervisor.go`,
  `internal/api/scanpolicy.go`, `cmd/mimir-daemon/main.go`,
  `desktop/src/lib/scanPolicy.ts`, `desktop/src/screens/Brain.tsx`
- **Roadmap bucket:** B.9 — Brain

## Context

`ASSESSMENT-2026-09-05.md` §B-1: daimî taramanın sır dosyası filtresi yoktu.
Paket içinde koşturulan doğrulamada `.env`, `id_rsa`, `server.key`, `.npmrc`,
`kubeconfig`, `terraform.tfstate` ve `~/Documents` PDF'lerinin hepsi
`scannable() = true` dönüyor, içerik `internal/llm/agy.go`'da `cmd.Stdin`'e
yazılıp harici bir model CLI'ına gidiyordu. Aradaki tek engel `.gitignore`'du ve
o engel repo olmayan dizinlerde — yani `~/Documents`'ta — hiç devrede değildi.

Aynı anda operatörün kendi isteği: taranan klasörler `internal/config`'te sabitti
(`~/development`, `~/Documents`), yani "bu daemon neyi okuyabilir?" sorusunun
cevabı Go dosyası düzenleyip yeniden derlemekti.

İkisi tek yüzey. Biri güvenlik varsayılanı, öteki rıza.

## Scope

1. `internal/settings/scan.go` — `ScanPolicy{Roots, Excludes}`, `scan.json`'da,
   `settings.json`'dan ayrı dosyada. `EffectiveScanPolicy(defaultRoots)` iki
   okuyucunun (döngü ve ekran) tek cevabı.
2. `internal/brain/exclude.go` — iki katman: `Excluder` (operatörün mutlak yol
   listesi, bileşen bazlı eşleşme) ve `isSecret` (yapılandırılamaz kimlik
   denylist'i).
3. `internal/brain/scan.go` — `isSecret` `scannable`'ın ilk kuralı;
   `ScanOptions.Exclude` `os.Stat`'tan önce; `ScanResult.Excluded` sayacı.
4. `internal/brain/supervisor.go` — `SupervisorDeps.Policy` her turda okunuyor;
   boş kök listesi döngüyü bitirmiyor, bekletiyor.
5. `internal/api/scanpolicy.go` — `GET`/`PUT /brain/scan/policy`,
   `POST /brain/scan/policy/reset`. Kökler `project.Canonicalize`'dan geçiyor.
6. Masaüstü: `lib/scanPolicy.ts` (saf mantık + testler) ve Brain sekmesinde
   `ScanPolicyCard` — yerel klasör/dosya seçici, taslak + kaydet.

## Out of scope

- Hariç tutulan bir yoldan gelmiş **mevcut düğümlerin silinmesi.** Tarama
  duruyor, grafik yerinde kalıyor. Silme ayrı ve açık bir onay olmalı — ayrı
  task.
- Glob desenleri. Liste görülüp satır siliniyor; desen semantiği ayrı bir iş.

## Definition of Done

- [x] `.env`, `id_rsa`, `*.pem`, `.npmrc`, `kubeconfig`, `*.tfstate`, `.ssh/`
      hiçbir politika yapılandırılmamışken bile eligible sete girmiyor
      (`TestScan_CredentialsAreRefusedWithoutAnyPolicy`)
- [x] Denylist sıradan dosyaları düşürmüyor: `docs/secrets.md`,
      `internal/keyboard.go`, `src/environment.ts` taranıyor
      (`TestIsSecret_LeavesOrdinaryFilesAlone`)
- [x] Hariç tutulan yol `os.Stat`'tan önce eleniyor, bileşen bazlı — `/a/b`
      `/a/bravo`'yu kapsamıyor (`TestExcluder_MatchesSubtreesNotStringPrefixes`)
- [x] Politika her turda yeniden okunuyor; daemon yeniden başlatılmıyor
      (`TestSupervisor_PolicyIsReReadEverySweep`)
- [x] Kök listesi boşaltılıp geri doldurulabiliyor, döngü ölmüyor
      (`TestSupervisor_EmptyRootsPauseTheLoopRatherThanEndIt`)
- [x] `/`, home dizini, `/etc` kök olarak reddediliyor; sembolik bağ önce
      çözülüyor (`TestScanPolicy_RefusesRootsTheDirectoryGuardRejects`)
- [x] Hariç tutma var olmayan bir yolu ve bir dosyayı adlandırabiliyor
- [x] `make check` green
- [x] `make desktop-check` green (206 vitest, 13 cargo)

## Notes for the reviewer

- **SD-1:** `BrainScanRoots` sabit olarak kalıyor ama artık *tohum*. Gerekçe
  `internal/config/AGENTS.md`'ye yazıldı: SD-1'in kendi testi bu değeri dışarı
  atıyor — makinenin özelliği değil, kişiden kişiye değişen bir rıza.
  `internal/settings` bu kategori için zaten var olan precedent.
- **SD-2 komşuluğu:** `isSecret` bir MCP yanıtını değil bir *okumayı* engelliyor,
  ama aynı mantığın uzantısı — veri, çıkması gereken yerden çıkmasın.
- İki katmanın ayrı tutulması kasıtlı: `Excluder` boşaltılabilir, `isSecret`
  boşaltılamaz.
