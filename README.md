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

Список рассылки принадлежит вошедшему пользователю. Статус `send` значит «отправлять», `skip` значит «не отправлять».

| Метод | Путь | Тело |
|---|---|---|
| GET | `/api/v1/recipients` | таблица адресов |
| POST | `/api/v1/recipients` | `{"email":"...","status":"send"}` или `{"emails":["..."],"status":"skip"}` |
| PATCH | `/api/v1/recipients/{id}` | `{"status":"send"}` или `{"status":"skip"}` |
| DELETE | `/api/v1/recipients/{id}` | удалить адрес |
| POST | `/api/v1/runs/{id}/send` | `{"mode":"all"}` или `{"mode":"selected","recipient_ids":["..."]}` |

`all` берёт только адреса со статусом `send`. `selected` отправляет выбранные, и каждый из них тоже должен быть `send`. Письма уходят по одному через тот же SMTP. Если `dry_run` не передан, письмо отправляется по-настоящему. Старый вызов с полем `recipients` по-прежнему сверяет адрес с allowlist.

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
| `SMTP_*` | SMTP и для одноразового кода регистрации, и для рассылки дайджеста |
| `ALLOWED_TEST_RECIPIENTS` | allowlist только для старого вызова с полем `recipients`. Код регистрации и рассылка по сохранённому списку идут через тот же SMTP без этого списка |
| `DATABASE_URL` | PostgreSQL для аккаунтов, сессий и списка выпусков. Без неё `monod` не запускается |
| `APP_PUBLIC_URL` | публичный адрес сайта для проверки Origin |
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
