> Архив первоначального предложения. Matrix исключён из проекта 20.09.2026.
> Этот документ не является действующим ТЗ. Актуальная версия: [SPEC.md](../../SPEC.md).

### **Техническое задание: Backend анонимного WebSocket-шлюза к Matrix**

#### **0\. Развертывание и конфигурация Matrix Homeserver**

До запуска разработки бэкенда требуется подготовить и сконфигурировать целевой Matrix Homeserver. Для легковесного MVP и анонимных временных чатов в качестве движка рекомендуется Conduit (написан на Rust, потребляет минимум RAM, не требует сложной БД) либо классический Synapse (если нужна максимальная совместимость со всеми фичами spec v1.11+).  
**0.1. Варианты развертывания (Docker Compose)**  
Развертывание выполняется в изолированном Docker-окружении с reverse proxy (Caddy или Nginx) для автоматического выпуска TLS-сертификатов.  
Пример конфигурации (docker-compose.yml на базе Conduit):

```
version: '3.8'

services:
  conduit:
    image: matrixconduit/conduit:latest
    restart: unless-stopped
    volumes:
      - conduit_data:/var/lib/matrix-conduit/
    environment:
      CONDUIT_SERVER_NAME: "matrix.yourdomain.local"
      CONDUIT_DATABASE_BACKEND: "rocksdb"
      CONDUIT_PORT: 6167
      CONDUIT_MAX_REQUEST_SIZE: 20000000 # 20MB
      CONDUIT_ALLOW_REGISTRATION: "true" # Необходимо для создания эфемерных пользователей
      CONDUIT_YES_I_PAY_RESPECTS_TO_SEMI_SYNAPSE: "true"
      CONDUIT_TRUSTED_SERVERS: "[]" # Отключаем федерацию для изоляции
    networks:
      - matrix-net

  # Reverse-proxy для терминации SSL и проксирования C-S API
  caddy:
    image: caddy:alpine
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - caddy_data:/data
    networks:
      - matrix-net

volumes:
  conduit_data:
  caddy_data:

networks:
  matrix-net:
    driver: bridge
```

**0.2. Требования к конфигурации Homeserver для анонимного режима**  
Чтобы бэкенд мог беспрепятственно управлять сессиями без участия живого администратора, сервер должен быть преднастроен по следующим критериям:

* **Регистрация пользователей (Registration):**  
  * Должна быть включена открытая регистрация через API (allow\_registration: true), либо сгенерирован мастер-токен регистрации (registration\_shared\_secret в Synapse), чтобы Go-сервис мог программно регистрировать пользователей вида @anon\_xxx:domain.  
  * Если используется Synapse — включить поддержку гостей: allow\_guest\_registration: true.  
* **Изоляция и приватность (No Federation):**  
  * Федерация должна быть отключена (federation.enabled: false или пустой список доверенных серверов). Комнаты не должны утекать во внешнюю сеть Matrix.  
  * Отключить публичный каталог комнат (enable\_room\_list\_search: false), чтобы анонимные комнаты не светились в глобальном поиске.  
* **Управление хранилищем и ротация (Media & History Retention):**  
  * Настроить политику автоматической очистки сообщений и медиа-файлов на стороне Homeserver (retention policy). Для Synapse: retention: enabled: true default\_policy: min\_lifetime: 1h max\_lifetime: 24h  
* **Служебный сервисный аккаунт (Admin Bot):**  
  * При инициализации Homeserver вручную или скриптом создается системный пользователь (например, @system\_bot:domain) с правами администратора.  
  * Токен доступа (access\_token) этого бота прокидывается в переменные окружения Go-бэкенда. Через него сервис будет создавать комнаты, генерировать инвайты и форсированно закрывать сессии.

**0.3. Критерии приемки инфраструктурного этапа (Definition of Done)**

* Homeserver успешно отвечает по HTTPS на эндпоинт проверки версии: GET https://matrix.yourdomain.local/\_matrix/client/versions возвращает 200 OK с поддерживаемыми версиями API.  
* Проверена возможность программной регистрации пользователя через POST /\_matrix/client/v3/register без капчи и email-верификации.  
* Системный бот успешно авторизован, его access\_token валиден для вызова POST /\_matrix/client/v3/createRoom.

#### **1\. Цель и концепция**

Разработать легковесный backend-сервис на **Go**, выступающий прокси-шлюзом между веб\-клиентом (через WebSockets) и Matrix Homeserver (Synapse/Dendrite/Conduit).  
Пользователь переходит по одноразовой ссылке с токеном комнаты, автоматически получает эфемерную гостевую сессию без ввода персональных данных, общается в реальном времени и при закрытии вкладки или истечении TTL теряет доступ.

#### **2\. Архитектурная схема**

\[ Frontend / SPA \]  
       ▲  
       │  (WebSocket: JSON RPC / Events)  
       ▼  
\[ Go Gateway Service \]  
   ├── Session Manager (RAM / Redis: mapping WS\_Conn ↔ Matrix\_Token)  
   ├── Room Dispatcher (валидация инвайт-токенов, TTL)  
   └── Matrix Worker (Client-Server API: sync, send, createRoom)  
       ▲  
       │  (Matrix Client-Server REST API / SSE Sync)  
       ▼  
\[ Matrix Homeserver \] (Synapse / Dendrite)

#### **3\. Технологический стек**

> * **Язык:** Go (1.22+)  
> * **HTTP / WebSocket:** gorilla/websocket или nhooyr.io/websocket  
> * **Matrix Client SDK:** maunium.net/go/mautrix (рекомендуется) либо прямая работа через HTTP-клиент с Matrix C-S API v3  
> * **Хранилище сессий и ссылок:** Redis (для TTL комнат, токенов приглашений и сессий) \+ in-memory sync engine  
> * **Контейнеризация:** Docker, Docker Compose

#### **4\. Функциональные требования**

**4.1. Управление ссылками и комнатами (Admin/Room API)**

> * POST /api/v1/rooms: Создание анонимной комнаты.  
  * Параметры: title (optional), ttl\_minutes (по умолчанию 60), max\_participants (по умолчанию 2).  
  * Действие бэкенда: Бот-администратор шлюза создает приватную комнату в Matrix (POST /\_matrix/client/v3/createRoom) с visibility: private и guest\_can\_join: true.  
  * Ответ: Сгенерированный join\_token и ссылка вида \[https://domain.tld/chat/\](https://domain.tld/chat/){join\_token}.  
> * GET /api/v1/rooms/{join\_token}/meta: Проверка доступности комнаты (активна ли, не истек ли TTL).

**4.2. Эфемерная регистрация и авторизация (Guest Auth)**

> * При переходе по ссылке бэкенд регистрирует временного гостя на Homeserver через Guest Registration:  
  * POST /\_matrix/client/v3/register?kind=guest.  
  * Либо создает системного юзера с рандомным ID (anon\_user\_\<nanoid\>) с паролем из энтропии.  
> * Присваивает рандомный displayname (например, Аноним \#42 или BlueFox).  
> * Инвайтит / джойнит пользователя в комнату Matrix (POST /\_matrix/client/v3/join/{roomIdOrAlias}).  
> * Генерирует внутренний session-токен для WebSocket-соединения.

**4.3. WebSocket Gateway (/ws?session\_token=...)**

> * **Inbound (Клиент \-\> Сервер):**  
  * send\_message: Отправка текста (m.room.message, msgtype: m.text).  
  * typing: Индикация набора текста (m.typing).  
  * read\_receipt: Отметка о прочтении.  
  * leave: Явный выход из комнаты (деавторизация).  
> * **Outbound (Сервер \-\> Клиент):**  
  * message\_received: Новое сообщение в комнате (sender, body, timestamp, event\_id).  
  * user\_joined / user\_left: События участников (m.room.member).  
  * typing\_notice: Статус набора текста собеседника.  
  * room\_expired: Сигнал завершения сессии по таймауту.

**4.4. Жизненный цикл и анонимизация (Garbage Collection)**

> * **Zero Logging:** Бэкенд не логирует IP-адреса и User-Agent клиентов.  
> * **Auto Cleanup:** По истечении TTL комнаты:  
  * Комната кикается / архивируется через Admin API.  
  * Сессии гостей инвалидируются (POST /\_matrix/client/v3/logout).  
  * Redis-ключи удаляются по встроенному TTL.

#### **5\. Спецификация WebSocket Протокола (JSON)**

**Клиент \-\> Сервер (Пример отправки сообщения):**

JSON  
{  
  "action": "send\_message",  
  "payload": {  
    "text": "Привет, здесь безопасно?"  
  }  
}

**Сервер \-\> Клиент (Трансляция сообщения):**

JSON  
{  
  "event": "new\_message",  
  "payload": {  
    "event\_id": "$xyz123...",  
    "sender": "Аноним \#2",  
    "is\_self": false,  
    "text": "Да, сквозное шифрование/эфемерная сессия.",  
    "timestamp": 1714000000000  
  }  
}

#### **6\. Нефункциональные требования и безопасность**

> * **Rate Limiting:** Ограничение на создание комнат и открытие WS-соединений с одного IP (Leaky Bucket / Redis limiter).  
> * **Graceful Shutdown:** Корректное закрытие WebSocket соединений с кодом 1001 (Going Away) и завершение Matrix /sync лупов.  
> * **Concurreny Model:** Для каждого активного WebSocket-клиента выделяется отдельная горутина чтения (readPump), записи (writePump) и фоновый Sync() listener из Matrix SDK.  
> * **CORS / Origin Check:** Жесткая валидация CheckOrigin в апгрейдере WebSockets.

#### **7\. Этапы разработки**

> 1. **MVP (Фаза 1):** Matrix Client интеграция, генерация ссылок-инвайтов, простой WS-мост текстовых сообщений.  
> 2. **Hardening (Фаза 2):** TTL-клинер сессий, m.typing, статусы участников, ограничение по числу участников в комнате (1-on-1).  
> 3. **Advanced (Фаза 3, опционально):** Поддержка Matrix E2EE (Olm/Megolm через Rust-библиотеку matrix-sdk-crypto или gomautrix crypto).
