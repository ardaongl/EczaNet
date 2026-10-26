# EczaNet mimari kararları

Bu dosya teslim edilen ürünün temel mimari kararlarını ve nedenlerini özetler.

## ADR-001 — Modüler monolit

Backend tek Go uygulaması içinde `platform`, `auth` ve `stock` modüllerine ayrılır. Case kapsamındaki transaction'ların tek veritabanında atomik kalması, dağıtık sistem karmaşıklığından daha değerlidir.

## ADR-002 — PostgreSQL iş bütünlüğünün parçasıdır

Yabancı anahtarlar, `UNIQUE`/`CHECK` kısıtları, transaction'lar ve satır kilitleri yalnız veri saklama için değil, eşzamanlı iş kurallarını korumak için kullanılır. Uygulama kontrolleri tek başına yarış durumlarını engellemez.

## ADR-003 — Para ve stokta kayan nokta kullanılmaz

Para kuruş cinsinden tam sayı, stok temel birim cinsinden tam miktar olarak tutulur. Kutu/blister/birim dönüşümü satıştan önce yapılır; satış anındaki fiyat ve paylar satır snapshot'ına yazılır.

## ADR-004 — FEFO ve deterministik kilit sırası

Lotlar önce SKT, sonra lot kimliğiyle tüketilir. Satış kilit sırası idempotans, reçete/sayaç, fiyat, ürün guard ve lotlar şeklindedir. Bu sıra deadlock riskini ve erken SKT'li lotun atlanmasını azaltır.

## ADR-005 — Kritik POST işlemlerinde idempotans

Satış, iade, transfer ve benzeri komutlar `Idempotency-Key` kullanır. Aynı anahtar ve gövde tekrarlandığında ilk sonuç döner; farklı gövde aynı anahtarla kabul edilmez. İş sonucu aynı transaction içinde kaydedilir.

## ADR-006 — Append-only hareket ve denetim kayıtları

Stok düzeltmeleri geçmiş satırları değiştirmez; yeni ve dengeli hareket kayıtları üretir. Audit kayıtları da eklemeli-yalnızdır. Böylece fiziksel bakiye ile hareket defteri yeniden uzlaştırılabilir.

## ADR-007 — Sunucu taraflı oturum ve sunucu yetkisi

Oturum cookie'si `HttpOnly` olarak tutulur; CSRF ve Origin kontrolü uygulanır. Rol ve lokasyon yetkisi istemciden gelen bilgiye güvenmeden sunucuda çözülür. Veritabanı runtime hesabı minimum yetki taşır.

## ADR-008 — Ayrı ve sınırlı simülatör

Simülatör doğrudan tablo yazmaz; sınırlı servis hesabıyla gerçek API'yi kullanır. Olay kimliği ve idempotans anahtarı seed/tarih/sıra üzerinden üretilir. Böylece restart sonrasında aynı olay çift etki oluşturmaz.

## ADR-009 — İzole ve güvenli kabul ortamı

Reset, yük ve kaos komutları yalnız sabit `eczanet-a6` Compose projesini ve doğrulanmış volume etiketlerini kabul eder. Ana geliştirme verisine veya global Docker kaynaklarına yönelik genel silme/prune komutu bulunmaz.
