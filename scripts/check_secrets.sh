#!/bin/sh
set -eu
if ! command -v dirname >/dev/null 2>&1; then
  if [ -d /c/PROGRA~1/Git/usr/bin ]; then
    PATH="/c/PROGRA~1/Git/usr/bin:$PATH"
  fi
fi
cd "$(dirname "$0")/.."
if command -v gitleaks >/dev/null 2>&1; then
  gitleaks detect --source . --no-git --redact
  exit $?
fi
pattern='AKIA[0-9A-Z]{16}|sk-[A-Za-z0-9]{20,}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|LLM_API_KEY=[^[:space:]]{8,}|SMTP_PASSWORD=[^[:space:]]{8,}'
hits=$(grep -RInE --exclude-dir=.git --exclude-dir=bin --exclude-dir=runs --exclude-dir=out --exclude-dir=data --exclude=check_secrets.sh --exclude=.env -e "$pattern" . || true)
if [ -n "$hits" ]; then
  printf '%s\n' "$hits" >&2
  echo "найдены возможные секреты" >&2
  exit 1
fi
echo "секреты не найдены"
