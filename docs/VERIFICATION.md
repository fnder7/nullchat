# Проверка PostgreSQL persistence — 26.09.2026

Task 8 проверен в worktree `.worktrees/postgres-persistence`, отдельный Compose project `nullchat-task8`, свежий named volume `nullchat-task8_postgres_data`. БД приложения — `nullchat`, тестовая — `nullchat_test`; порты только на loopback: 5432 и 8080. В окружении сгенерирован случайный development key; значение не записано в репозиторий или этот отчёт. Вывод `docker compose config` перенаправлен в `/dev/null`, чтобы не раскрывать подставленный секрет.

Среда: Linux amd64, host Go `1.27.1-X:nodwarf5`, Docker `29.8.0`, Compose `v5.5.1`. Реально собранный target `build` использует Go `1.26.8`, PostgreSQL — `17.11` из `postgres:17-alpine`. Go floor проекта — 1.25. В Alpine builder установлен C toolchain для race; финальный scratch-образ содержит `/nullchat` и `/migrate`, работает как `65532:65532`. Оба бинарника по-прежнему собираются с `CGO_ENABLED=0`, `-trimpath`, `-ldflags="-s -w"`.

## Финальный whole-branch fix round

Финальная проверка выполнена в том же worktree от base `693e9fb`. Host Docker — `29.8.1`, Compose — `v5.5.1`. Добавлены очистка истёкших `idempotency_requests`, JSON `server_shutdown` перед close `1001`, стабильный `connection_limit` до и после upgrade-race, CA roots в scratch-образе, отказ title с NUL и миграция `0002_invites_room_idx.sql`. План, `docs/PLAN.md` и SDD ledger не менялись.

RED→GREEN подтверждён отдельно:

- `TestExpireDeletesExpiredIdempotencyAndAllowsKeyReuse` сначала сохранил одну истёкшую строку, затем прошёл и подтвердил недоступность replay до cleanup, удаление ciphertext и новый запрос с тем же ключом после cleanup.
- `TestExpireCleanupPreservesReplayLockOrder` воспроизвёл deadlock старого варианта room-lock → idempotency-delete; отдельный атомарный cleanup statement устранил цикл.
- `TestShutdownCancelsBlockedSubscribeAndWaitsForHandler` и process-restart test теперь получают JSON `server_shutdown`, затем close `1001`; pre-upgrade лимит возвращает HTTP `429 connection_limit`, post-upgrade race — JSON error и retryable close `1013`.
- `TestNormalizeTitleRejectsNUL` и migration test падали до исправлений; после изменения migration ledger содержит версии `1,2`, а `invites_room_idx` существует.
- До Dockerfile fix `docker cp` сообщил об отсутствии `/etc/ssl/certs/ca-certificates.crt`; после rebuild bundle извлечён из финального Compose image, `test -s` завершился с exit 0.

Фактически выполненные финальные команды:

| Команда / проверка | Результат |
|---|---|
| `TEST_DATABASE_URL=... go test -race ./...` | PASS; PostgreSQL package `11.502s`, race reports отсутствуют |
| `go vet ./...` | exit 0 |
| `go build ./cmd/server ./cmd/migrate` | exit 0 |
| `node --check web/app.js` | exit 0 |
| `docker compose config --quiet` | exit 0 |
| `docker build -t nullchat:final-review .` | exit 0; scratch image собран с CA bundle |
| `docker compose up --build --abort-on-container-exit migrate` | migration job exit 0 |
| `docker compose up --build -d postgres nullchat` | PostgreSQL healthy, migrate exit 0, API started |
| `curl --fail .../healthz` и `curl --fail .../readyz` | HTTP 200: `ok` и `ready` |
| `docker compose run --rm test` | PASS; PostgreSQL integration/race suite `17.256s` |
| SQL-проверка migration ledger/index | `versions=1,2`, `invite_index=true` |
| Извлечение CA bundle из `postgres-persistence-app:local` | bundle существует и не пуст |

Полная очистка комнат/истории, WAL/backup retention, outbox/replay, paging и multi-service остаются отложены. Физически удаляются только истёкшие idempotency envelopes; остальные обещания хранения не расширялись.

## Выполненные проверки Task 8

Все успешные host Go-команды использовали доступные для записи кэши:

```sh
export GOMODCACHE=/tmp/nullchat-task4-gomod
export GOCACHE=/tmp/nullchat-task4-gocache
```

| Команда / проверка | Фактический результат |
|---|---|
| `go test -race ./...` | PASS после разрешения локальных сокетов; без TEST_DATABASE_URL DB-кейсы пропущены |
| `TEST_DATABASE_URL='postgres://nullchat:nullchat@127.0.0.1:5432/nullchat_test?sslmode=disable' go test -race ./... -count=1` | PASS, все пакеты; PostgreSQL `11.421s`, без race reports и пропуска DB-кейсов |
| `go vet ./...` | exit 0, без вывода |
| `go build ./cmd/server ./cmd/migrate` | exit 0, без вывода |
| `node --check web/app.js` | exit 0, без вывода |
| `docker compose config` (с ключом, вывод скрыт) | exit 0 |
| `env -u IDEMPOTENCY_ENCRYPTION_KEY docker compose config` | Ожидаемый отказ: обязательный ключ отсутствует; имён переменных достаточно, значений секретов в ошибке нет |
| `docker compose up --build --abort-on-container-exit migrate` | GREEN: образ собран, `/migrate` exit 0 на свежей БД; повторный запуск тоже exit 0 |
| `docker compose up --build -d postgres nullchat` | exit 0; PostgreSQL healthy, migration exited 0, API запущен после зависимостей |
| `curl --fail http://127.0.0.1:8080/healthz` | HTTP 200, `{"status":"ok"}` |
| `curl --fail http://127.0.0.1:8080/readyz` | HTTP 200, `{"status":"ready"}` |
| `TEST_DATABASE_URL='postgres://nullchat:nullchat@127.0.0.1:5432/nullchat_test?sslmode=disable' go test -tags=integration -race ./internal/storage/postgres` | PASS, `13.392s` |
| `docker compose run --rm test` | PASS, PostgreSQL suite `16.509s`; builder target, integration/race, отдельная `nullchat_test` |
| `docker compose restart nullchat` | exit 0; затем readiness HTTP 200 (`curl --fail --retry 10 --retry-connrefused --retry-delay 1 http://127.0.0.1:8080/readyz`) |
| `docker compose exec -T postgres psql -U nullchat -d nullchat -Atc 'SELECT version FROM schema_migrations'` | Одна строка: `1` после повторных миграций |
| `docker compose exec -T postgres psql -U nullchat -d nullchat_test -Atc 'SELECT current_database()'` | `nullchat_test`; init SQL сработал на свежем volume |

До изменения Compose acceptance-команда миграции дала ожидаемый RED: `no such service: migrate` (exit 1). После изменения выполнена реальная миграция и весь workflow выше; новые unit-тесты, проверяющие текст YAML/Dockerfile, не добавлялись.

Первый sandboxed `go test -race ./...` упал в `TestWebSocketCommandsReceiveFiniteOperationDeadline`: `httptest: failed to listen on a port: listen tcp6 [::1]:0: socket: operation not permitted`. Docker daemon сначала был недоступен с `permission denied ... /var/run/docker.sock`. Повтор с разрешённым доступом к локальным сокетам и Docker устранил оба ограничения; Docker и сеть реально использовались, пропущенных Compose-проверок нет.

## Что подтверждают integration tests

- `TestApplyMigrationsIsIdempotent`: повторное применение версионированной схемы.
- `TestConcurrentIdempotencyFirstUse`, `TestJoinIdempotencyReplayReturnsTheSameSession`, `TestCreateIdempotencyFingerprintConflict`, `TestJoinIdempotencyFingerprintConflict`: безопасный повтор create/join и конфликт изменённого запроса; `TestJoinConsumesInviteAtomically` — один гость при конкуренции.
- `TestOperationsCommitFailureDoesNotPublish`, `TestCreateCommitFailureRollsBack`, `TestJoinCommitFailureDoesNotConsumeOrPublish`: отсутствие частичного результата и публикации при сбое commit.
- `TestServerRestartPreservesCommittedRoom`: реальный server binary, HTTP create, WS send/ack, чтение сохранённого сообщения из БД, SIGTERM/close 1001 `server_shutdown`, новый процесс и восстановление прежней сессии/истории; также startup/periodic expiry и отсутствие автоматических миграций.
- `TestCommittedRoomSurvivesServiceRestart`: новый pool/service сохраняет комнату, сессию, сообщение и неиспользованное приглашение.
- Тесты TTL, controlled snapshot/subscribe handoff, дедупликации и revoke проверяют текущий single-process контракт. `TestDatabaseFailureIsSanitizedByHTTP` проверяет ошибки реального закрытого pool; `/healthz` остаётся независимым от БД.

Повторить только migration/restart cases можно после запуска PostgreSQL:

```sh
TEST_DATABASE_URL='postgres://nullchat:nullchat@127.0.0.1:5432/nullchat_test?sslmode=disable' \
  go test -tags=integration -race ./internal/storage/postgres \
  -run 'TestApplyMigrationsIsIdempotent|TestCommittedRoomSurvivesServiceRestart|TestServerRestartPreservesCommittedRoom' -count=1 -v
```

Это команда для повторения; в Task 8 эти случаи прошли внутри полных integration suites, а не отдельным запуском указанного фильтра. Тесты очищают таблицы: использовать только выделенную `nullchat_test`, не запускать suites параллельно против одной БД. Без TEST_DATABASE_URL DB-кейсы пропускаются независимо от `-tags=integration`.

## Границы проверки и оставшаяся работа

Durable event replay и transactional outbox отсутствуют. Между commit и broadcast возможна потеря события для подключённого клиента; reconnect получает полную сохранённую историю. Нет курсоров, paging, нескольких core/gateway и exactly-once доставки. TTL/end отзывают доступ; expiry worker удаляет истёкшие `idempotency_requests`, но физическая очистка остальных строк/истории, WAL и backup policy отложены. `/readyz` проверяет ping, а не схему: успешная миграция — отдельное условие запуска.

В Task 8 не повторялись интерактивный браузерный smoke, SIGKILL/power-loss, сетевой blackout, нагрузка, публичный HTTPS и удалённый CI. Compose restart проверил перезапуск/readiness; сохранность сообщения и сессии подтверждена process integration test, а не новым ручным браузерным сценарием. Исторические browser-результаты ниже относятся к memory-демо 20.09.2026.

После проверки контейнеры проекта `nullchat-task8` остановлены; named volume и собранные образы сохранены. Проверка не удаляла пользовательские данные.

# Историческая проверка memory-демо — 20.09.2026

## Автоматические проверки

- `go test -race ./...` — прошёл.
- `go vet ./...` — прошёл.
- `node --check web/app.js` — прошёл; Node нужен только для этой дополнительной проверки.
- Сборка самостоятельного бинарника: `go build -buildvcs=false -o bin/nullchat ./cmd/server`.

В рабочей среде Go `1.27.1-X:nodwarf5`, Linux amd64. `-buildvcs=false` отключает чтение VCS-метаданных: служебный `.git` этой среды не является полноценным Git-репозиторием.

## Chrome MCP

Проверено в независимых browser contexts:

- Создание комнаты и вход второго участника по приглашению.
- Обмен сообщениями в обе стороны.
- Обновление вкладки с восстановлением сессии и обоих сообщений.
- Принудительное закрытие WebSocket с автоматическим reconnect и восстановлением истории.
- Отказ третьему участнику при повторном использовании приглашения.
- Отображение `<script>…</script>` обычным текстом: исполняемые script-узлы не создаются.
- `sessionStorage` не содержит историю сообщений.
- Мобильный viewport 390×844: ширина документа 390, горизонтального переполнения нет.
- Комната на одну минуту закрывается с состоянием «Время разговора истекло».
- Явное завершение гостем закрывает чат создателя и очищает видимые сообщения.
- В проверенных сценариях нет console errors/warnings.

Это ручной browser smoke test, не постоянный browser-тест в CI. Реальный сетевой blackout, длительный soak-тест и нагрузка пока не проверены.

## Что не проверено

На 20.09.2026 Docker/Compose запуск не проверялся: Docker в той среде отсутствовал. Это историческое ограничение; фактическая проверка PostgreSQL/Compose от 26.09.2026 описана выше. Публичное HTTPS-развёртывание и GitHub Actions тогда не запускались.
