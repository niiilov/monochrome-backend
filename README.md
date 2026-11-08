# mono

ИИ-сервис подготовки новостного дайджеста по кибербезопасности (бэкенд на Go, хакатон ИТ-Фест).

## Запуск

```bash
cp .env.example .env   # заполните LLM_API_KEY, LLM_BASE_URL, LLM_MODEL
# Для стабильного прогона оставьте LLM_PROXY и LLM_PROXY_FILE пустыми

make build
./bin/mono run \
  --dataset testdata/train/publications.json \
  --control-time "2026-11-07T12:00:00+03:00" \
  --out ./out

python tools/check_mono.py ./out
./bin/mono check --out ./out
```

Без ключа: `./bin/mono run ... --mock`.

HTTP API: `./bin/monod` (см. `cmd/monod`).

То же API через Docker Compose (ключ из `.env`, прогоны в `./runs`):

```bash
make up          # http://localhost:8080
make up-mock     # без сети и без ключа
make down
```

Обязательные сценарии:

```bash
./bin/mono test-mandatory          # mock
./bin/mono test-mandatory --live   # живая модель, пишет out/tests_report.*
```

## Контрольный прогон одной строкой

```bash
LLM_PROXY= LLM_PROXY_FILE= ./scripts/control_run.sh testdata/train/publications.json "2026-11-07T12:00:00+03:00"
```

Скрипт запускает `mono run`, `mono check`, `tools/check_mono.py` и собирает файлы в `submission/`.

## Переменные окружения

| Переменная | Назначение |
|---|---|
| `LLM_API_KEY` | ключ API |
| `LLM_BASE_URL` | OpenAI-совместимый endpoint |
| `LLM_MODEL` | имя модели |
| `LLM_PROXY` / `LLM_PROXY_FILE` | опциональный HTTP-прокси |
| `SMTP_*`, `ALLOWED_TEST_RECIPIENTS` | рассылка (allowlist) |
| `DATABASE_URL` | PostgreSQL для аккаунтов, сессий и списка выпусков. Без неё `monod` не запускается |
| `APP_PUBLIC_URL` | публичный адрес для ссылки подтверждения почты |
| `AUTH_COOKIE_SECURE` | `true`, если сайт открывается по HTTPS |

Секреты только в env / локальном `.env`. В git не коммитить.

## Инструменты

- Go 1.22+ (в среде разработки может быть новее)
- `make` (build, test, vet, check, ci)
- Python 3 для `tools/check_mono.py`
- Опционально: Docker, gitleaks (`scripts/check_secrets.sh`)

## CI/CD

На push и pull request GitHub Actions (`.github/workflows/ci.yml`) выполняет `go vet`, `go test ./...`, сборку `mono` и `monod`, `scripts/check_secrets.sh` и сборку Docker-образа. Локально то же самое без образа: `make ci`.

Push в `main` после успешных проверок публикует образ в GHCR: `ghcr.io/<owner>/cyber-digest` с тегами `latest` и полного SHA. В образ не попадают `.env` и `data/`. Локальный аналог — `make up`.

Готовый образ:

```bash
docker run --rm -p 8080:8080 ghcr.io/niiilov/cyber-digest
```

Процесс — `monod`, адрес `:8080`, проверка живости: `GET /healthz`. CLI внутри образа: `docker run --rm --entrypoint mono ghcr.io/niiilov/cyber-digest --help`.

## Структура

```
cmd/mono/          # CLI: run, check, test-mandatory, llm-ping
cmd/monod/         # HTTP API
internal/            # domain, ingest, topic, extract, cluster, rank,
                     # summarize, render, verify, llm, guard, runner, api, ...
prompts/             # шаблоны промптов (*.tmpl)
testdata/train/      # учебный набор организаторов
testdata/mandatory/  # T1-T8
tools/check_mono.py
scripts/control_run.sh
docs/TZ.md docs/PRD.md docs/AI_DESCRIPTION.md docs/EVAL_texts.md
```

## Документы сдачи

- `docs/AI_DESCRIPTION.md` - модель, шаги ИИ, проверки, ограничения
- `docs/EVAL_texts.md` - разбор текстов на учебном наборе
- `docs/EVAL.md` - разбор отбора (участник A)
- `submission/` - артефакты контрольного прогона после `control_run.sh`
