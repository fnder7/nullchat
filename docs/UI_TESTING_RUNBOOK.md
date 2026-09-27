# UI testing runbook for agents

Инструкция для повторяемого ручного UI/E2E-прогона из
[`UI_TEST_CASES.md`](UI_TEST_CASES.md). Не заменяет Go и PostgreSQL integration
tests: UI проверяет пользовательский контракт, автоматические suites — инварианты
и конкурентные сценарии.

## 1. Зафиксировать исходное состояние

```sh
git status --short
git rev-parse --short HEAD
```

Не изменять и не удалять существующие пользовательские файлы. В отчете указать
commit, браузер, дату и уже существовавшие изменения worktree.

## 2. Поднять окружение

Создать отдельный development-секрет и не печатать/не коммитить его:

```sh
export IDEMPOTENCY_ENCRYPTION_KEY="$(openssl rand -hex 32)"
docker compose up --build --abort-on-container-exit migrate
docker compose up --build -d postgres nullchat
curl --fail http://127.0.0.1:8080/readyz
```

Не выполнять `docker compose down -v`: это удаляет тестовые данные volume. Для
прогона используется только локальная development-БД из `compose.yaml`.

## 3. Подготовить браузер

- Открыть `http://127.0.0.1:8080` в Chrome DevTools MCP.
- Создателя, гостя и третьего пользователя открывать в разных isolated browser
  contexts. Разные вкладки одного context могут разделять storage и не подходят
  для проверки независимых участников.
- До негативных сценариев проверить чистую консоль и успешную загрузку HTML,
  CSS, JS и favicon.
- Для каждого значимого шага сохранять фактический видимый текст, состояние
  кнопок, счетчик сообщений и статус подключения.

## 4. Рекомендуемый порядок

1. Выполнить P0 `UI-01`—`UI-05` в комнате на 15 минут.
2. В той же комнате выполнить offline/reconnect и ручной retry (`UI-06`,
   `NET-01`—`NET-05`). Network emulation сама по себе может не закрыть уже
   открытый WebSocket; после включения Offline нужно принудительно закрыть
   тестовый socket или остановить backend.
3. Перезапустить только backend и проверить persistence (`UI-07`, `NET-06`):

   ```sh
   docker compose restart nullchat
   ```

4. Остановить PostgreSQL, проверить create/send error, затем вернуть БД и
   повторить операцию (`ROOM-05`, `NET-07`, `NET-08`):

   ```sh
   docker compose stop postgres
   docker compose up -d postgres
   docker compose ps
   ```

5. Завершить комнату гостем, затем отдельную комнату создателем (`END-01`,
   `END-02`). Обновить страницу и повторить invite после завершения.
6. Создать отдельную комнату с TTL 1 минута. Дождаться серверного события, не
   подменяя его изменением часов браузера (`END-05`—`END-07`).
7. Выполнить validation, XSS, storage, privacy и responsive кейсы.
8. Проверить viewport `390x844` и desktop. Сравнить
   `document.documentElement.scrollWidth` с `innerWidth`.
9. Выполнить Lighthouse snapshot для главной страницы и сохранить названия всех
   failed accessibility audits.

## 5. Предельные сценарии

`MSG-11` не следует выполнять отправкой 499 сообщений с паузой: rate limit
сделает прогон долгим и нестабильным. Допустима подготовка fixture напрямую в
development PostgreSQL:

1. Через UI создать отдельную комнату и сохранить ее `room_id` и
   `participant_id` без session/invite token в отчете.
2. Вставить 499 тестовых сообщений с последовательностями `1..499`, выставить
   `rooms.message_count = 499`, `next_message_sequence = 500`, а
   `sessions.last_send_at = NULL`.
3. Обновить UI: он должен показать `499 / 500` и 499 сообщений.
4. Отправить через UI 500-е сообщение, затем после rate-limit интервала — 501-е.
5. Ожидать `500 / 500` и неотправленное 501-е сообщение с причиной
   `message_limit`.

Fixture создается только в локальной development-БД. Никогда не выполнять этот
SQL против production или общей БД.

Идемпотентность create/join можно проверить из страницы повтором одинакового
fetch с одним `Idempotency-Key`. В отчете сравнивать только room/session equality;
не выводить сами session и invite tokens.

## 6. Privacy anti-enumeration

- Существующий и случайный `#room=<64 hex>` проверять в чистых contexts без
  `sessionStorage`.
- Сравнивать не только текст, но и Network: открытие `#room` без сессии не должно
  обращаться к backend за комнатой.
- Для invite сравнивать использованный, закрытый и случайный 64-символьный hex
  token. Все должны давать одинаковое публичное состояние без названия,
  участников и других данных комнаты.
- Успешный вход по действующему одноразовому приглашению не считается утечкой:
  possession of the invite предоставляет предусмотренный доступ.

## 7. Автоматический baseline

После UI-прогона выполнить:

```sh
go test -race ./...
go vet ./...
node --check web/app.js
docker compose run --rm test
```

Без `TEST_DATABASE_URL` локальные PostgreSQL tests могут быть пропущены, поэтому
Compose test service обязателен для полного прогона.

## 8. Отчет и завершение

- Создать `docs/test-reports/YYYY-MM-DD-ui-regression.md` по шаблону из
  `UI_TEST_CASES.md`.
- У каждого кейса должен быть `PASS`, `FAIL` или `BLOCKED`. Не считать
  непроверенный кейс успешным по коду или unit tests.
- Для `FAIL` записать ожидаемый и фактический результат, минимальные шаги и
  затронутую страницу.
- Отделить ожидаемые console/network ошибки негативных сценариев от
  необработанных ошибок приложения.
- Оставить `postgres` и `nullchat` запущенными только если это полезно следующему
  разработчику; явно сообщить их состояние. Не удалять volume без прямого
  запроса пользователя.
