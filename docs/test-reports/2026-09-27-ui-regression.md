# UI regression report — 2026-09-27

- Commit: `15d3b78`
- Окружение: Docker Compose, PostgreSQL 17, локальный backend
- Браузер: управляемый Chrome, независимые isolated contexts
- Объем: полный P0 и P1 из `docs/UI_TEST_CASES.md`

## Итог

- `PASS`: 56
- `FAIL`: 7
- `BLOCKED`: 0
- Блокирующих потерю данных или доступ постороннего к комнате дефектов не найдено.
- PostgreSQL persistence после restart backend и временного отказа БД работает.
- Полные замечания: `docs/UI_TEST_FINDINGS.md` (`NC-UI-001`—`NC-UI-007`).

## P0

| ID | Результат | Комментарий |
|---|---|---|
| UI-01—UI-09 | PASS | Создание, два независимых участника, обмен, refresh, reconnect, restart backend, завершение и проверка консоли выполнены. Ошибки консоли во время намеренного offline/restart были ожидаемыми. |

## P1

| Группа / ID | Результат | Комментарий |
|---|---|---|
| ROOM-01 | FAIL | Пустое название превращается в английское `Quiet conversation`, хотя весь UI и placeholder русские. |
| ROOM-02—ROOM-04 | PASS | 60 символов принимаются; TTL 1/15/30/60 соответствует серверному `expires_at`; повтор с одним idempotency key возвращает ту же комнату и сессию. |
| ROOM-05 | FAIL | Функционально 503 обработан, но create показывает «Сессия сохранена», хотя сессия еще не создана. |
| JOIN-01—JOIN-03, JOIN-05—JOIN-06 | PASS | Вход, повтор join, запрет третьему участнику, отсутствие доступа по одному room URL и refresh работают. |
| JOIN-04 | FAIL | Malformed invite показывает текст «Сообщение должно содержать от 1 до 4000 байт», не относящийся к приглашению. |
| MSG-01—MSG-11 | PASS | Enter/Shift+Enter, пустой ввод, 4000-byte boundary, oversize, XSS, retry/dedup, порядок, persistence и лимит 500 проверены. 499 сообщений были подготовлены fixture в локальной БД; 500-е принято, 501-е получило `message_limit`. |
| NET-01—NET-07 | PASS | Offline, auto/manual reconnect, pending message, restart backend и отказ БД проверены. |
| NET-08 | FAIL | После восстановления БД retry сохраняет сообщение ровно один раз, но старый баннер `database_unavailable` остается до следующей обычной отправки. |
| END-01—END-07 | PASS | Завершение обоими ролями, refresh, invite после закрытия, реальный TTL 1 минута и сохранение исходного `expires_at` работают. |
| SEC-01—SEC-05 | PASS | Истории нет в `sessionStorage`; session удаляется; доступ без token отсутствует; внутренние DB-детали не показаны; title/message XSS не исполняется. |
| PRIV-01—PRIV-02, PRIV-04 | PASS | Реальный и случайный room URL неразличимы; backend не запрашивается. Used/closed/random well-formed invite возвращают одинаковое публичное состояние. |
| PRIV-03 | FAIL | Утечки нет, но malformed invite получает нерелевантную ошибку про сообщение. |
| VIEW-01—VIEW-06 | PASS | Desktop/mobile `390x844`, keyboard focus/Enter, disabled states, длинный title и таймер проверены; горизонтального overflow нет. |
| VIEW-07 | FAIL | Футер утверждает, что история хранится в памяти и теряется при restart backend; это противоречит текущей PostgreSQL persistence. |
| VIEW-08 | FAIL | Lighthouse accessibility: 96/100, провалены `color-contrast` и `label-content-name-mismatch`. |

## Findings

Детальные замечания, шаги воспроизведения и privacy-выводы вынесены в постоянный
реестр `docs/UI_TEST_FINDINGS.md`. Этот файл хранит только результаты конкретного
прогона.

## Автоматические проверки

| Проверка | Результат |
|---|---|
| `go test -race ./...` | PASS |
| `go vet ./...` | PASS |
| `node --check web/app.js` | PASS |
| `docker compose run --rm test` | PASS (`internal/storage/postgres`, 17.645s) |

## Состояние окружения

Тестовые `postgres` и `nullchat` оставлены запущенными; named volume не удалялся.
