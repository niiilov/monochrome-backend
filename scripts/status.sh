#!/bin/sh
set -eu
if ! command -v dirname >/dev/null 2>&1; then
  if [ -d /c/PROGRA~1/Git/usr/bin ]; then
    PATH="/c/PROGRA~1/Git/usr/bin:$PATH"
  fi
fi
cd "$(dirname "$0")/.."
printf '%s\n' "этап	владелец	статус	открытые вопросы"
found=0
for f in docs/progress/S*.md; do
  [ -f "$f" ] || continue
  found=1
  stage=$(basename "$f" .md)
  owner=$(sed -n 's/^Владелец:[[:space:]]*//p' "$f" | head -n 1)
  status=$(sed -n 's/^Статус:[[:space:]]*//p' "$f" | head -n 1)
  questions=$(awk '
    /^## Открытые вопросы[[:space:]]*$/ { grab=1; next }
    /^## / { if (grab) exit }
    grab { print }
  ' "$f" | tr '\n' ' ' | sed 's/[[:space:]]*$//; s/^[[:space:]]*//')
  if [ -z "$questions" ]; then
    questions="-"
  fi
  printf '%s\t%s\t%s\t%s\n' "$stage" "$owner" "$status" "$questions"
  echo "--- передачи $stage ---"
  awk '
    /^## Передачи второму участнику[[:space:]]*$/ { grab=1; next }
    /^## / { if (grab) exit }
    grab { print }
  ' "$f"
  echo
done
if [ "$found" -eq 0 ]; then
  echo "файлы docs/progress/S*.md не найдены" >&2
  exit 1
fi
