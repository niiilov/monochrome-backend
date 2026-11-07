# cyber-digest

ИИ-сервис подготовки новостного дайджеста по кибербезопасности (бэкенд на Go, хакатон ИТ-Фест).

## Запуск

```bash
cp .env.example .env   # заполните LLM_API_KEY, LLM_BASE_URL, LLM_MODEL
# Для стабильного прогона оставьте LLM_PROXY и LLM_PROXY_FILE пустыми

make build
./bin/digest run \
  --dataset testdata/train/publications.json \
  --control-time "2026-11-07T12:00:00+03:00" \
  --out ./out

python tools/check_digest.py ./out
./bin/digest check --out ./out
```

Без ключа: `./bin/digest run ... --mock`.

HTTP/UI: `./bin/digestd` (см. `cmd/digestd`).

Обязательные сценарии:

```bash
./bin/digest test-mandatory          # mock
./bin/digest test-mandatory --live   # живая модель, пишет out/tests_report.*
```

## Контрольный прогон одной строкой

```bash
LLM_PROXY= LLM_PROXY_FILE= ./scripts/control_run.sh testdata/train/publications.json "2026-11-07T12:00:00+03:00"
```

Скрипт запускает `digest run`, `digest check`, `tools/check_digest.py` и собирает файлы в `submission/`.

## Переменные окружения

| Переменная | Назначение |
|---|---|
| `LLM_API_KEY` | ключ API |
| `LLM_BASE_URL` | OpenAI-совместимый endpoint |
| `LLM_MODEL` | имя модели |
| `LLM_PROXY` / `LLM_PROXY_FILE` | опциональный HTTP-прокси |
| `SMTP_*`, `ALLOWED_TEST_RECIPIENTS` | рассылка (allowlist) |

Секреты только в env / локальном `.env`. В git не коммитить.

## Инструменты

- Go 1.22+ (в среде разработки может быть новее)
- `make` (build, test, vet, status, check)
- Python 3 для `tools/check_digest.py`
- Опционально: Docker, gitleaks (`scripts/check_secrets.sh`)

## Структура

```
cmd/digest/          # CLI: run, check, test-mandatory, llm-ping
cmd/digestd/         # HTTP API + веб
internal/            # domain, ingest, topic, extract, cluster, rank,
                     # summarize, render, verify, llm, guard, runner, api, ...
prompts/             # шаблоны промптов (*.tmpl)
testdata/train/      # учебный набор организаторов
testdata/mandatory/  # T1-T8
tools/check_digest.py
scripts/control_run.sh
docs/TZ.md docs/PRD.md docs/AI_DESCRIPTION.md docs/EVAL_texts.md
```

## Документы сдачи

- `docs/AI_DESCRIPTION.md` - модель, шаги ИИ, проверки, ограничения
- `docs/EVAL_texts.md` - разбор текстов на учебном наборе
- `docs/EVAL.md` - разбор отбора (участник A)
- `submission/` - артефакты контрольного прогона после `control_run.sh`
