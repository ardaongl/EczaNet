# EczaNet test ve doğrulama rehberi

Demo hesapları ve kurulum adımları [README.md](README.md) dosyasındadır. Test çıktıları `artifacts/` altında oluşturulur ve Git'e eklenmez.

## Hızlı doğrulama

```powershell
make verify
```

Make bulunmayan ortamda:

```powershell
docker compose --profile verify run --rm verify
```

Komut HEALTH, AUTH, F-1…F-7, T-1…T-5, IDEMPOTENCY ve INVARIANTS sonuçlarını ayrı `PASS`/`FAIL` satırlarıyla basar. Başarısız veya eksik kontrol sıfırdan farklı çıkış kodu üretir.

## Tekrarlanabilir izole ortam

```powershell
docker compose -p eczanet-a6 -f compose.yaml -f tests/compose.a6.yaml up --build -d --wait
python scripts/a6.py verify --project eczanet-a6 --out artifacts/verify-001
python scripts/a6.py reset --project eczanet-a6 --out artifacts/reset-001
python scripts/a6.py verify --project eczanet-a6 --out artifacts/verify-002
```

Her `--out` yolu yeni veya boş olmalıdır. Reset yalnızca `eczanet-a6` isimli izole proje ve etiketleri doğrulanmış iki test volume'ünü hedefler.

## Özellik ve zor görev matrisi

| Kimlik | Nasıl denenir | Beklenen |
|---|---|---|
| F-1 | Ürün oluştur, barkod ve etken maddeyle ara | Baştaki sıfırlar, doz, form ve kutu bilgisi korunur |
| F-2 | İrsaliye kabul et, depodan şubeye sevk et | Belge tek etki üretir; lot ve SKT korunur |
| F-3 | E-reçete yükle, eşdeğer ve kısmi karşılama yap | Hak doğru azalır; başka şube reddedilir |
| F-4 | Quote al, satış yap, iade ve karantina uygula | Sunucu tutarı ve satış snapshot'ı değişmez |
| F-5 | Ara/nihai transfer ve sayım yap | Yoldaki, eksik ve hasarlı stok ayrı izlenir |
| F-6 | İhtiyaç, müdür onayı ve sipariş akışını çalıştır | Öneri bir kez tüketilir; ETA tek sayılır |
| F-7 | Kritik stok, yakın SKT, finans ve kontrollü defteri aç | API sonuçları hareket ve satış toplamlarıyla eşleşir |
| T-1 | Erken ve geç SKT'li lotlarla satış; 200 eşzamanlı istek | FEFO korunur; negatif stok ve beklenmeyen 5xx oluşmaz |
| T-2 | İki şube ve günlük reçete limiti yarışları | Yalnız izinli sahip tüketir; limit aşımı reddedilir |
| T-3 | Fiyat geçerliliği, iskonto, katılım payı ve iade | Kuruş hesabı ve snapshot tamdır |
| T-4 | Tahsisli stok, transfer, sayım ve defter uzlaştırması | Çift teslim ve kayıp hareket oluşmaz |
| T-5 | 5.000 ürün × 20 şube öneri hesabı | 100.000 sonuç hesap+commit dahil 5 saniyenin altında |

## Ek kontroller

| Amaç | Komut |
|---|---|
| Go ve PostgreSQL testleri | `make test` |
| Entegrasyon testleri | `make integration` |
| Coverage kapısı | `python scripts/check-coverage.py artifacts/coverage.out` |
| Frontend build | `npm ci --prefix web` ardından `npm run build --prefix web` |
| Tarayıcı testleri | `cd web`; ardından `npm run test:e2e` |
| Mantıksal seed checksum | `python scripts/a6.py checksum --project eczanet-a6 --out artifacts/checksum-001` |
| Karma yük | `python scripts/a6.py load --project eczanet-a6 --out artifacts/load-001` |
| İndeks karşılaştırması | `python scripts/a6.py explain --project eczanet-a6 --out artifacts/index-001` |
| DB kesintisi/iyileşme | `python scripts/a6.py chaos --project eczanet-a6 --out artifacts/chaos-001` |
| Soğuk kurulum | `python scripts/a6-cold.py cold-001` |
| Simülatör | `python scripts/a6-simulator-check.py --out artifacts/simulator-001` |
| Canlı dashboard | `node scripts/a6-live-ui.mjs --out artifacts/live-ui-001` |
| OpenAPI ve belge kontrolü | `python scripts/check-docs.py` |
| HTTP koleksiyonu | `python scripts/check-http-collection.py --out artifacts/http-collection.json` |

Ana geliştirme verisi üzerinde reset, kaos, soğuk kurulum, volume silme veya global Docker prune çalıştırmayın.

## Elle arayüz senaryosu

1. Eczacıyla barkod/etken madde arayın ve lot/SKT stoklarını görüntüleyin.
2. Quote alın, satış yapın ve satış detayından iade/karantina sonucunu kontrol edin.
3. Bir reçeteyi kısmi karşılayın; farklı şubeden erişimin reddedildiğini görün.
4. Depodan sevk oluşturup eksik/hasarlı nihai teslim gerçekleştirin.
5. Müdür hesabıyla sayım ve ihtiyaç onayı yapın.
6. Depo hesabıyla sipariş/tahsis/ETA akışını tamamlayın.
7. Muhasebe hesabıyla fiyat ve finans raporlarını açın.
8. Teknisyen hesabıyla kontrollü ilacı kesinleştirmenin reddedildiğini görün.

## API ve CI

- [OpenAPI](api/openapi.yaml) bütün endpointlerin sözleşmesini içerir.
- [requests.http](requests.http) olumlu ve kasıtlı ret örneklerini içerir.
- `.github/workflows/ci.yml` fresh seed üzerinde verify, Go/PG, format, vet, race, coverage, frontend build ve Playwright testlerini çalıştırır.
- CI çıktıları `artifacts/` olarak yüklenir; test kanıtları repoya commit edilmez.
