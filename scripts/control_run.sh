#!/usr/bin/env bash
# Контрольный прогон: digest run + check + сбор submission/.
# Использование: scripts/control_run.sh <набор> <контрольное время>
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

DATASET="${1:-}"
CONTROL="${2:-}"
if [[ -z "$DATASET" || -z "$CONTROL" ]]; then
  echo "Использование: $0 <набор> <контрольное время>" >&2
  echo "Пример: $0 testdata/train/publications.json \"2026-11-07T12:00:00+03:00\"" >&2
  exit 2
fi

OUT="${CONTROL_OUT:-out/control}"
SUB="${SUBMISSION_DIR:-submission}"

mkdir -p "$OUT" "$SUB"

echo ">> digest run"
# Пустые LLM_PROXY* в окружении не дадут подтянуть прокси из .env (см. loadDotEnv).
LLM_PROXY="${LLM_PROXY-}" LLM_PROXY_FILE="${LLM_PROXY_FILE-}" \
  go run ./cmd/digest run \
    --dataset "$DATASET" \
    --control-time "$CONTROL" \
    --out "$OUT"

echo ">> digest check"
go run ./cmd/digest check --out "$OUT"

echo ">> check_digest.py"
python tools/check_digest.py "$OUT"

echo ">> сбор $SUB/"
cp -f "$OUT/digest.md" "$SUB/digest.md"
cp -f "$OUT/post.txt" "$SUB/post.txt"
cp -f "$OUT/email_preview.html" "$SUB/email_preview.html"
cp -f "$OUT/journal.csv" "$SUB/journal.csv"
cp -f "$OUT/journal.json" "$SUB/journal.json"
cp -f "$OUT/run_report.json" "$SUB/run_report.json"
cp -f docs/AI_DESCRIPTION.md "$SUB/AI_DESCRIPTION.md"
cp -f docs/EVAL_texts.md "$SUB/EVAL_texts.md"
cp -f README.md "$SUB/README.md"

if [[ -f out/tests_report.md ]]; then
  cp -f out/tests_report.md "$SUB/tests_report.md"
fi
if [[ -f out/tests_report.json ]]; then
  cp -f out/tests_report.json "$SUB/tests_report.json"
fi

echo "готово: $SUB/"
ls -la "$SUB"
