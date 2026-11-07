#!/usr/bin/env python3
"""Проверка формата выпуска по правилам кейса.

Запуск: python check_digest.py <каталог с digest.md, post.txt и journal.csv>
Код выхода 0, если формат соблюден.
"""

import csv
import re
import sys
from pathlib import Path

DIGEST_LIMIT = 4000
POST_LIMIT = 2000
STATUSES = {"включён", "объединён", "исключён"}
HEADER_RE = re.compile(
    r"(?m)^# Дайджест по кибербезопасности: .+, контрольное время .+$"
)
COUNT_RE = re.compile(r"(?m)^Событий в выпуске: (\d+)\b")
ITEM_RE = re.compile(r"(?m)^## (\d+)\. \S")
URL_RE = re.compile(r"https?://\S+")
TRAIL = ".,;:!?)]}>\"'"
LABELS = ("Суть: ", "Последствия: ", "Источники: ", "Обоснование: ")
EMPTY_EVENTS = "Подходящих событий за период нет"


def main() -> int:
    if len(sys.argv) != 2 or sys.argv[1] in ("-h", "--help"):
        print("Использование: python check_digest.py <каталог>", file=sys.stderr)
        return 2
    out = Path(sys.argv[1])
    errors = []
    digest_text, digest_ok = read_text(out / "digest.md", errors, "digest.md")
    post_text, post_ok = read_text(out / "post.txt", errors, "post.txt")
    if digest_ok:
        errors.extend(check_digest(digest_text))
    if post_ok:
        errors.extend(check_post(post_text))
    errors.extend(check_journal(out / "journal.csv"))
    if not (out / "email_preview.html").is_file() and not (out / "email.txt").is_file():
        errors.append("нет email_preview.html или email.txt")
    if errors:
        for item in errors:
            print(item, file=sys.stderr)
        return 1
    print("формат в порядке")
    return 0


def read_text(path: Path, errors: list, label: str) -> tuple:
    if not path.is_file():
        errors.append(f"нет файла {label}")
        return "", False
    raw = path.read_bytes()
    if raw.startswith(b"\xef\xbb\xbf"):
        errors.append(f"{label}: файл содержит BOM")
        return "", False
    try:
        return raw.decode("utf-8"), True
    except UnicodeDecodeError:
        errors.append(f"{label}: файл не в UTF-8")
        return "", False


def check_digest(text: str) -> list:
    if not text.strip():
        return ["digest.md: пустой дайджест"]
    errors = []
    if "\u2014" in text or "\u2013" in text:
        errors.append("digest.md: длинное тире")
    if "Не вошли" in text:
        errors.append("digest.md: лишний блок")
    count_chars = char_count(text)
    if count_chars > DIGEST_LIMIT:
        errors.append(f"digest.md: лимит знаков: {count_chars}")
    if HEADER_RE.search(text) is None:
        errors.append("digest.md: заголовок выпуска не совпадает с форматом")
    found = COUNT_RE.search(text)
    if found is None:
        errors.append("digest.md: нет строки «Событий в выпуске»")
        return errors
    declared = int(found.group(1))
    matches = list(ITEM_RE.finditer(text))
    if declared == 0:
        if matches:
            errors.append("digest.md: при нуле событий не должно быть блоков")
        if EMPTY_EVENTS not in text:
            errors.append("digest.md: нет сообщения о пустом выпуске")
        return errors
    if declared > 5:
        errors.append(f"digest.md: событий больше пяти: {declared}")
    if len(matches) != declared:
        errors.append(f"digest.md: блоков {len(matches)}, в строке счёта {declared}")
    for index, match in enumerate(matches):
        number = int(match.group(1))
        if number != index + 1:
            errors.append(f"digest.md: номер {number}, ожидался {index + 1}")
        end = matches[index + 1].start() if index + 1 < len(matches) else len(text)
        errors.extend(check_block(text[match.start() : end]))
    return errors


def check_block(block: str) -> list:
    errors = []
    prev = -1
    for label in LABELS:
        at = block.find("\n" + label)
        if at < 0 or at <= prev:
            return ["digest.md: в блоке нет четырёх полей в нужном порядке"]
        rest = block[at + 1 + len(label) :]
        line = rest.split("\n", 1)[0]
        if not line.strip():
            errors.append(f"digest.md: пустое поле {label.strip()}")
        prev = at
    return errors


def check_post(text: str) -> list:
    if not text.strip():
        return ["post.txt: пустой пост"]
    errors = []
    if "\u2014" in text or "\u2013" in text:
        errors.append("post.txt: длинное тире")
    count = char_count(text)
    if count > POST_LIMIT:
        errors.append(f"post.txt: лимит знаков: {count}")
    return errors


def check_journal(path: Path) -> list:
    if not path.is_file():
        return ["нет файла journal.csv"]
    raw = path.read_bytes()
    if raw.startswith(b"\xef\xbb\xbf"):
        return ["journal.csv: файл содержит BOM"]
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError:
        return ["journal.csv: файл не в UTF-8"]
    if not text.strip():
        return ["journal.csv: пустой файл"]
    rows = list(csv.reader(text.splitlines()))
    if not rows:
        return ["journal.csv: пустой файл"]
    header = [cell.strip() for cell in rows[0]]
    if header != ["id", "status", "group", "reason", "error"]:
        return ["journal.csv: заголовок должен быть id,status,group,reason,error"]
    errors = []
    seen = set()
    for line, row in enumerate(rows[1:], start=2):
        if len(row) != 5:
            errors.append(f"journal.csv: строка {line}: ожидалось 5 столбцов")
            continue
        item_id, status, group, reason, _error = (cell.strip() for cell in row)
        if not item_id:
            errors.append(f"journal.csv: строка {line}: пустой id")
            continue
        if item_id in seen:
            errors.append(f"journal.csv: строка {line}: повтор id {item_id}")
        seen.add(item_id)
        if status not in STATUSES:
            errors.append(f"journal.csv: строка {line}: статус {status!r} недопустим")
        if status == "объединён" and not group:
            errors.append(f"journal.csv: строка {line}: статус «объединён» без группы")
        if not reason:
            errors.append(f"journal.csv: строка {line}: пустая причина")
    return errors


def char_count(text: str) -> int:
    stripped = URL_RE.sub(lambda match: keep_trail(match.group(0)), text)
    return len(stripped)


def keep_trail(url: str) -> str:
    core = url.rstrip(TRAIL)
    return url[len(core) :]


if __name__ == "__main__":
    sys.exit(main())
