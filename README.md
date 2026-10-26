# EczaNet — Semih Arda Öngül

EczaNet, 20 şubeli kurgusal bir eczane zinciri için geliştirilmiş uçtan uca stok ve satış yönetimi uygulamasıdır. Go, PostgreSQL 16 ve React kullanır. Lot/SKT takibi, FEFO satış, reçete ve eşdeğer ürün, fiyat ve katılım payı, iade, transfer, sayım, tedarik, sipariş önerisi ve raporlama akışları gerçek API üzerinden çalışır.

Gerçek SGK, MEDULA ve ödeme sağlayıcısı entegrasyonu kapsam dışındadır.

## Hızlı kurulum

Gereksinimler: Docker Engine/Desktop ve Docker Compose 2.24.4 veya üzeri.

```powershell
Copy-Item .env.example .env
docker compose up --build -d --wait
```

- Web: [http://localhost:3000](http://localhost:3000)
- API sağlık kontrolü: [http://localhost:8080/health](http://localhost:8080/health)
- Veritabanı host makineye port açmaz.

`docker compose down` veriyi korur. `docker compose down --volumes` veriyi siler ve normal sıfırlama yöntemi değildir.

İlk kurulum 20 şube, 1 depo, 5.000 ürün ve 90 günlük satış geçmişi üretir. Seed aynı `DEMO_SEED` ve `DEMO_DATE` ile tekrarlandığında aynı mantıksal veriyi oluşturur; güvenlik salt ve tokenları deterministik değildir.

## Tek komutla doğrulama

Çalışan ve seed edilmiş sistem üzerinde bütün F-1…F-7 ve T-1…T-5 senaryolarını doğrulamak için:

```powershell
make verify
```

Make bulunmayan ortamda:

```powershell
docker compose --profile verify run --rm verify
```

İzole ve tekrar sıfırlanabilir kabul ortamı:

```powershell
docker compose -p eczanet-a6 -f compose.yaml -f tests/compose.a6.yaml up --build -d --wait
python scripts/a6.py verify --project eczanet-a6 --out artifacts/verify-001
python scripts/a6.py reset --project eczanet-a6 --out artifacts/reset-001
python scripts/a6.py verify --project eczanet-a6 --out artifacts/verify-002
```

`artifacts/` Git tarafından yok sayılır. Reset komutu yalnızca sabit adı ve Docker etiketleri doğrulanmış `eczanet-a6` test ortamını kabul eder; ana geliştirme verisini hedeflemez.

## Demo hesapları

Varsayılan yerel demo parolası: `Demo-EczaNet-2026!`. Gerçek ortamda kullanılmamalıdır.

| Rol | E-posta | Kapsam |
|---|---|---|
| Eczacı | seed.yildiz@ddo-case.invalid | Şube 1 satış ve reçete |
| Teknisyen | teknisyen@ddo-case.invalid | Reçete hazırlığı |
| Depo | depo@ddo-case.invalid | Kabul, sevk ve tedarik |
| Müdür | mudur@ddo-case.invalid | Onay ve sayım |
| Muhasebe | muhasebe@ddo-case.invalid | Finans ve fiyat |
| İkinci eczacı | eczaci.b@ddo-case.invalid | Şube 2 |
| Simülatör | simulator@ddo-case.invalid | Sınırlı servis hesabı |

## Mimari

- Go modüler monolit: HTTP ve yapılandırma `internal/platform`, kimlik `internal/auth`, iş kuralları `internal/stock`.
- PostgreSQL transaction'ları belge, lot, bakiye, defter, audit ve idempotans sonucunu birlikte commit eder.
- Para kuruş cinsinden tam sayı; stok miktarları temel birim cinsinden tutulur.
- Satış kilit sırası: idempotans → reçete/sayaç → fiyat → ürün guard → DB saati → FEFO lotları.
- React/TypeScript istemcisi nginx üzerinden API'ye erişir; dashboard 5 saniyede bir gerçek veriyi yeniler.
- Migration, runtime ve test kullanıcıları farklı yetkilere sahiptir.

Ayrıntılı gerekçeler [DECISIONS.md](DECISIONS.md), API sözleşmesi [OpenAPI](api/openapi.yaml), elle istek koleksiyonu [requests.http](requests.http) ve test rehberi [TESTING.md](TESTING.md) içindedir.

## Simülatör

Simülatör ayrı bir Compose servisidir ve yalnızca gerçek HTTP API'sini kullanır. Normal hızda yaklaşık her 28,8 saniyede bir satış üretir; hatalı barkod, geçmiş SKT, günlük reçete limiti ve aynı idempotans anahtarının tekrarı gibi kontrollü ret senaryoları da içerir. Beklenen iş retleri ile beklenmeyen sistem hataları ayrı sayılır.

```powershell
$env:A6_SIMULATOR_ENABLED='true'
$env:SIMULATOR_SPEED='1'
docker compose -p eczanet-a6 -f compose.yaml -f tests/compose.a6.yaml up -d --no-deps simulator
```

## Yerel performans ölçümleri

Sonuçlar aynı yerel Docker ortamında alınmıştır; farklı donanım için garanti değildir.

| Senaryo | Yük | Sonuç |
|---|---:|---:|
| Karma okuma, quote ve satış | 5 VU / 7.007 istek | p50 16 ms, p95 46 ms, p99 47 ms, beklenmeyen hata %0 |
| Eşzamanlı stok çekişmesi | 200 istek / 100 stok | 100 satış, 100 stok reddi, 0 beklenmeyen 5xx |
| Aynı idempotans anahtarı | 200 tekrar | Tek satış etkisi |
| 5.000 ürün × 20 şube öneri | 100.000 sonuç | Hesap ve commit en fazla 3.034 ms |

İndeks karşılaştırmasında aynı sorgu ve fixture üçer kez çalıştırılmıştır:

| Sorgu | Dizinsiz medyan | Dizinli medyan |
|---|---:|---:|
| Şube satış keyset sorgusu | 3,744 ms | 0,778 ms |
| Lot hareket defteri sorgusu | 27,081 ms | 0,105 ms |

## Doğrulama soruları

1. **En zor bölüm:** FEFO, reçete sahipliği, günlük limit ve fiyat geçerliliğini aynı transaction ve kilit protokolünde korumaktı. Yalnız uygulama belleği kilidi çok süreçte yeterli olmadığı için PostgreSQL satır kilitleri kullanıldı.
2. **Bilinen sınırlamalar:** Gerçek SGK/ödeme entegrasyonu yoktur; rate limiter tek API sürecinin belleğindedir; performans ölçümleri yerel makineye bağlıdır.
3. **İndeks yaklaşımı:** Satış ve defter sorguları aynı fixture ve parametrelerle, indeks transaction içinde geçici olarak kaldırılıp geri alınarak karşılaştırıldı. UNIQUE ve FK kısıtları kaldırılmadı.
4. **Düzeltilen çıktı:** Goose tablo adındaki tire önce tırnaksız üretildiği için PostgreSQL sözdizimi hatası oluştu; identifier açıkça çift tırnaklanarak boş veritabanı migration testi geçirildi.
5. **FEFO kanıtı:** Verify senaryosu erken SKT'li lottan 4, sonraki lottan 2 birim tüketir; eşzamanlı 200 istekte fiziksel stok, lot tahsisi ve hareket defteri yeniden uzlaştırılır.

## Geliştirme ve CI

GitHub Actions; container build, tam doğrulama, PostgreSQL entegrasyon testleri, Go format/vet/race, domain coverage, frontend build ve Playwright senaryolarını çalıştırır. Üretilen raporlar GitHub Actions artifact'i olarak saklanır, repoya commit edilmez.

Daha fazla komut ve beklenen sonuç için [TESTING.md](TESTING.md) dosyasına bakın.

DDO Case Kodu: YILDIZ-a33906
