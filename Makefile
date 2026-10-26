.PHONY: test integration verify verify-a1 verify-a2 verify-a3 verify-a4 test-api-fault
test:
	docker compose --profile tools run --rm --build tools go test -v -count=1 ./...
	$(MAKE) test-api-fault
integration:
	docker compose --profile tools run --rm --build tools go test -v -count=1 ./tests/integration/...
verify:
	docker compose --profile verify run --rm verify
verify-a1:
	docker compose --profile verify run --rm verify /app/verify --a1

verify-a2:
	docker compose --profile verify run --rm verify /app/verify --a2

verify-a3:
	docker compose --profile verify run --rm verify /app/verify --a3

test-api-fault:
	docker compose --profile tools run --rm --no-deps -e TEST_CORRECTION_API_URL=http://api:8080 tools go test -v -count=1 -run TestA3Correction ./tests/integration

verify-a4:
	docker compose --profile verify run --rm verify /app/verify --a4

.PHONY: reset-demo load explain chaos verify-demo
A6_RUN = artifacts/a6-runs/$(shell python -c "import time;print(time.time_ns())")
reset-demo:
	python scripts/a6.py reset --project eczanet-a6 --out $(A6_RUN)
verify-demo:
	python scripts/a6.py verify --project eczanet-a6 --out $(A6_RUN)
load:
	python scripts/a6.py load --project eczanet-a6 --out $(A6_RUN)
explain:
	python scripts/a6.py explain --project eczanet-a6 --out $(A6_RUN)
chaos:
	python scripts/a6.py chaos --project eczanet-a6 --out $(A6_RUN)
