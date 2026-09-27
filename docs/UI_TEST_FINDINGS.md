# UI test findings

Постоянный реестр замечаний, найденных UI/E2E-прогонами. Разовые результаты и
окружение находятся в `docs/test-reports/`; здесь хранятся стабильные finding ID,
статус и воспроизведение.

Статусы: `Open`, `Resolved`, `Accepted`. Severity: `Critical`, `High`, `Medium`,
`Low`.

## Open findings

Нет. Все семь первоначальных замечаний проверены повторно 2026-09-27.

## Resolved findings

### NC-UI-001 — футер описывает устаревшее хранение истории

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Medium`
- Cases: `VIEW-07`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: открыть главную или чат и прочитать текст футера.
- Expected: текст соответствует PostgreSQL persistence и не обещает потерю
  подтвержденной истории при restart backend.
- Actual: футер сообщает, что история хранится в памяти до закрытия комнаты или
  перезапуска сервера.
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.

### NC-UI-002 — название комнаты по умолчанию выбивается из локали

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Low`
- Cases: `ROOM-01`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: оставить название пустым и создать комнату.
- Expected: русское нейтральное название, согласованное с placeholder и UI.
- Actual: отображается `Quiet conversation`.
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.

### NC-UI-003 — malformed invite получает ошибку про сообщение

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Medium`
- Cases: `JOIN-04`, `PRIV-03`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: открыть `/#invite=invalid-token` и нажать «Войти в комнату».
- Expected: нейтральная ошибка о недействительном приглашении без сведений о
  комнате.
- Actual: «Проверь данные. Сообщение должно содержать от 1 до 4000 байт текста».
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.

### NC-UI-004 — create при отказе БД говорит о несуществующей сессии

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Low`
- Cases: `ROOM-05`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: остановить PostgreSQL и попытаться создать комнату.
- Expected: сообщение о временной недоступности и безопасном повторе создания.
- Actual: «Сервер временно недоступен. Сессия сохранена, повтори позже», хотя
  сессия еще не создана.
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.

### NC-UI-005 — успешный retry не скрывает старую ошибку БД

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Medium`
- Cases: `NET-08`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: отправить сообщение при остановленной БД, вернуть PostgreSQL и нажать
  «Повторить» у сообщения.
- Expected: сообщение подтверждено ровно один раз, временная ошибка скрыта.
- Actual: сообщение сохранено один раз, но баннер `database_unavailable` остается
  до следующей обычной отправки.
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.

### NC-UI-006 — главная страница не проходит все accessibility audits

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Medium`
- Cases: `VIEW-08`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: выполнить Lighthouse accessibility snapshot главной страницы.
- Expected: нет failed accessibility audits.
- Actual: score `96`; failed `color-contrast` для мелкого текста и
  `label-content-name-mismatch` для логотипа.
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.

### NC-UI-007 — текст без сессии может восприниматься как подтверждение комнаты

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Low`
- Cases: `PRIV-01`, `PRIV-04`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: в чистом browser context открыть существующий или случайный
  `/#room=<64 hex>` без `sessionStorage`.
- Expected: нейтральный текст, не подразумевающий существование конкретной
  комнаты, например «Не удалось открыть комнату. Используй действующее
  приглашение или создай новую».
- Actual: «В этой вкладке нет сессии комнаты. Попроси новое приглашение или
  создай свою комнату».
- Security evidence: реальный и случайный room ID дают одинаковый результат и
  не вызывают HTTP/WebSocket-запрос; фактическая enumeration не подтверждена.
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.
