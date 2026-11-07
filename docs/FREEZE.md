# Заморозка перед защитой

Тег: `release-defense`. После этого тега код и промпты не менять. Сайт с UI в тег не входит: он собирается отдельно.

Коммит тега записывается в `docs/progress/S14.md` тем же тегом. Пока тег не поставлен, ориентир ветки: `s14-freeze`.

## Что заморожено

- Код и промпты на теге `release-defense`.
- Хэш промптов живых прогонов репетиции: `f044835189648e78e70822bb6211429bb413cbe93d2c314da1157f3bf5313609`.
- Хэш конфига: `d804f1951891067828ca77881858beea81dfb93e31fbf8414764da8a39596ebf`.
- Модель живых прогонов: `google/gemini-3.5-flash-lite`, провайдер `openai`, температура 0.
- Версия приложения в отчете: `0.1.0-s0`. Go в прогоне: `go1.25.0`.
- Стоимость: `cost: null`, `cost_unavailable: true`.

Ключи в эти файлы не записывались. Они остаются в локальном `.env`.

## Контрольная репетиция

Команда: `scripts/control_run.sh testdata/rehearsal/articles.json "2026-11-07T12:00:00+03:00"`.

Каталог: `out/s14-rehearsal` (копия сдачи: `out/s14-submission`). Прогон `20261107T233349-01bf83c0`, 07.11.2026 23:33:49-23:34:06 МСК, 17123 мс.

Набор: `testdata/rehearsal/articles.json`. Хэш набора: `58721b5362cee8090233f1085ce530dd006ee880623df41a6de0d6218cdde9db`.

Счетчики: всего 4, включено 3, объединено 1, исключено 0, событий 3. Токены: 22438 / 3060. `verify`: 1 повтор, замечаний нет, `degraded: false`.

Хэши артефактов (SHA-256 из `run_report.json`):

| Файл | SHA-256 |
|---|---|
| digest.md | `6b6c1e7c51605e84bc3a2e3e37d1d4fbedb29aa87ded9466d644af2a1bb2f20b` |
| post.txt | `d3081afe6082a67d9901310b296a769b1d8d851ca686cb3df66a952939dae705` |
| email.txt | `f251d1cfa283b664350119648ca734b7b66b864628b4b6a1381176ce2fae8949` |
| email_preview.html | `2bbea47f199ee53e57496145a4b31c13a577cc0f7b2a03c8fdf4774b9e7b58f8` |
| journal.csv | `43661964502c422ef4baf2bac4ea2da8f707bf1130a528ba34d8a3f1319a5880` |
| journal.json | `30bdfcad1a615bfb028cd44eb4c0d44400205aa9f4912d0245aea0f6bdd47279` |

## Демо новой публикации

Повтор события, прогон `20261107T234228-01bf83c0`, каталог `out/s14-extra-repeat`. Хэш набора с `--extra`: `531d5914bd6634bf566e41af2fe43000de76c8b7d97a0122cd9e15dc4e8394b9`. Счетчики: включено 3, объединено 2, событий 3. `reh-extra-repeat` объединен в `E02`. Токены: 27979 / 3572.

| Файл | SHA-256 |
|---|---|
| digest.md | `3e41f7f03ccb5ef2d20ece2ae6df17c147ca58d2434f797351c8d38937135933` |
| post.txt | `5cbf00541e2c8f8532a5651f0a3a3c7046bc50fa0b69a23f48d2a9843b5671bd` |
| email.txt | `758297248da0fc4a316180f80493d96a32aed2a212d4f795c387717b1cd7cbca` |
| email_preview.html | `28a4e97a0393bf4fd921d8da114771bd12a6d3a74be59cda775ec1ae0db4c276` |
| journal.csv | `40577a0c22fb02337c133f0964bd136557c2eebf7004eec9e1218f4aba428c4b` |
| journal.json | `8b8c7b2789c8d15599fdb51f2a763c14867e5d71f56d0b73209f4e659f08e413` |

Инъекция, прогон `20261107T234832-01bf83c0`, каталог `out/s14-extra-inject`. Хэш набора с `--extra`: `061b86430d4144b64480b331de3325e6a423cf7a8058c397b4a53b23685cb8ef`. Счетчики: включено 4, объединено 1, исключено 0, событий 4. Токены: 29116 / 3852. `verify.findings` пустой.

| Файл | SHA-256 |
|---|---|
| digest.md | `675c9b6a3e3249371a002e36dd9a57232533ee633b021d55f752762079d1b9f6` |
| post.txt | `df9086a08c22ee1dd828fff2285954b4018b5bf8a02344330ebe8c0f5935a929` |
| email.txt | `9475d16986ecc85675d27d72b9636cfa30f15f83ceb379b666364a905e6becd9` |
| email_preview.html | `fd0bc57e2fe6a93d354304ab5f5c9d868e16cbcd5ce91bbde488b3d5ec9bafe4` |
| journal.csv | `dfdea805c30097daa77a50ca6a6c8b0b3d6c1e2186d0ce490b8117ae8f9c8b3a` |
| journal.json | `ffd37636df54c64076bfa2bc4d1f9c9178108f8a624d633ef5fae7bcb5bd1063` |

## Обязательные сценарии

`go run ./cmd/digest test-mandatory --out out/s14-mandatory`. Mock, 07.11.2026 23:33:49-23:34:01 МСК. 8/8, инъекции 20/20, трудные входы: прогон не упал. Тот же хэш промптов.
