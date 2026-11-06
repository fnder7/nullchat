> Archive of the original proposal. Matrix was dropped from the project on 2026-09-20.
> This document is not a current specification. The current version: [SPEC.md](../../SPEC.md).

### **Technical specification: Backend for an anonymous WebSocket gateway to Matrix**

#### **0\. Deploying and configuring the Matrix Homeserver**

Before backend development starts, the target Matrix Homeserver must be prepared and configured. For a lightweight MVP and anonymous temporary chats, the recommended engine is Conduit (written in Rust, uses minimal RAM, needs no complex DB) or the classic Synapse (if maximum compatibility with all spec v1.11+ features is needed).  
**0.1. Deployment options (Docker Compose)**  
Deployment runs in an isolated Docker environment with a reverse proxy (Caddy or Nginx) for automatic TLS certificate issuance.  
Example configuration (docker-compose.yml based on Conduit):

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
      CONDUIT_ALLOW_REGISTRATION: "true" # Required to create ephemeral users
      CONDUIT_YES_I_PAY_RESPECTS_TO_SEMI_SYNAPSE: "true"
      CONDUIT_TRUSTED_SERVERS: "[]" # Disable federation for isolation
    networks:
      - matrix-net

  # Reverse proxy for SSL termination and proxying the C-S API
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

**0.2. Homeserver configuration requirements for anonymous mode**  
For the backend to manage sessions freely without a live administrator, the server must be preconfigured according to the following criteria:

* **User registration (Registration):**  
  * Open registration via the API must be enabled (allow\_registration: true), or a registration master token must be generated (registration\_shared\_secret in Synapse), so the Go service can programmatically register users like @anon\_xxx:domain.  
  * If Synapse is used, enable guest support: allow\_guest\_registration: true.  
* **Isolation and privacy (No Federation):**  
  * Federation must be disabled (federation.enabled: false or an empty list of trusted servers). Rooms must not leak into the external Matrix network.  
  * Disable the public room directory (enable\_room\_list\_search: false) so anonymous rooms do not show up in global search.  
* **Storage management and rotation (Media & History Retention):**  
  * Configure an automatic message and media cleanup policy on the Homeserver side (retention policy). For Synapse: retention: enabled: true default\_policy: min\_lifetime: 1h max\_lifetime: 24h  
* **Service account (Admin Bot):**  
  * When the Homeserver is initialized, a system user with administrator rights (for example, @system\_bot:domain) is created manually or by a script.  
  * This bot's access token (access\_token) is passed to the Go backend's environment variables. The service uses it to create rooms, generate invites and force-close sessions.

**0.3. Acceptance criteria for the infrastructure stage (Definition of Done)**

* The Homeserver responds over HTTPS on the version endpoint: GET https://matrix.yourdomain.local/\_matrix/client/versions returns 200 OK with the supported API versions.  
* Programmatic user registration via POST /\_matrix/client/v3/register without captcha or email verification has been verified.  
* The system bot is authenticated successfully, and its access\_token is valid for calling POST /\_matrix/client/v3/createRoom.

#### **1\. Goal and concept**

Build a lightweight backend service in **Go** that acts as a proxy gateway between the web client (over WebSockets) and a Matrix Homeserver (Synapse/Dendrite/Conduit).  
The user follows a one-time link with a room token, automatically gets an ephemeral guest session without entering personal data, chats in real time, and loses access when the tab is closed or the TTL expires.

#### **2\. Architecture diagram**

\[ Frontend / SPA \]  
       ▲  
       │  (WebSocket: JSON RPC / Events)  
       ▼  
\[ Go Gateway Service \]  
   ├── Session Manager (RAM / Redis: mapping WS\_Conn ↔ Matrix\_Token)  
   ├── Room Dispatcher (invite token validation, TTL)  
   └── Matrix Worker (Client-Server API: sync, send, createRoom)  
       ▲  
       │  (Matrix Client-Server REST API / SSE Sync)  
       ▼  
\[ Matrix Homeserver \] (Synapse / Dendrite)

#### **3\. Technology stack**

> * **Language:** Go (1.22+)  
> * **HTTP / WebSocket:** gorilla/websocket or nhooyr.io/websocket  
> * **Matrix Client SDK:** maunium.net/go/mautrix (recommended) or direct use of the Matrix C-S API v3 through an HTTP client  
> * **Session and link storage:** Redis (for room TTLs, invite tokens and sessions) \+ in-memory sync engine  
> * **Containerization:** Docker, Docker Compose

#### **4\. Functional requirements**

**4.1. Link and room management (Admin/Room API)**

> * POST /api/v1/rooms: Create an anonymous room.  
  * Parameters: title (optional), ttl\_minutes (default 60), max\_participants (default 2).  
  * Backend action: the gateway's admin bot creates a private room in Matrix (POST /\_matrix/client/v3/createRoom) with visibility: private and guest\_can\_join: true.  
  * Response: a generated join\_token and a link like \[https://domain.tld/chat/\](https://domain.tld/chat/){join\_token}.  
> * GET /api/v1/rooms/{join\_token}/meta: Check room availability (whether it is active and the TTL has not expired).

**4.2. Ephemeral registration and authorization (Guest Auth)**

> * When the link is followed, the backend registers a temporary guest on the Homeserver via Guest Registration:  
  * POST /\_matrix/client/v3/register?kind=guest.  
  * Or creates a system user with a random ID (anon\_user\_\<nanoid\>) and a high-entropy password.  
> * Assigns a random displayname (for example, Anonymous \#42 or BlueFox).  
> * Invites / joins the user to the Matrix room (POST /\_matrix/client/v3/join/{roomIdOrAlias}).  
> * Generates an internal session token for the WebSocket connection.

**4.3. WebSocket Gateway (/ws?session\_token=...)**

> * **Inbound (Client \-\> Server):**  
  * send\_message: Send text (m.room.message, msgtype: m.text).  
  * typing: Typing indicator (m.typing).  
  * read\_receipt: Read receipt.  
  * leave: Explicitly leave the room (deauthorization).  
> * **Outbound (Server \-\> Client):**  
  * message\_received: A new message in the room (sender, body, timestamp, event\_id).  
  * user\_joined / user\_left: Participant events (m.room.member).  
  * typing\_notice: The other participant's typing status.  
  * room\_expired: Signal that the session ended on timeout.

**4.4. Lifecycle and anonymization (Garbage Collection)**

> * **Zero Logging:** The backend does not log client IP addresses or User-Agents.  
> * **Auto Cleanup:** When the room's TTL expires:  
  * The room is kicked / archived via the Admin API.  
  * Guest sessions are invalidated (POST /\_matrix/client/v3/logout).  
  * Redis keys are removed by the built-in TTL.

#### **5\. WebSocket protocol specification (JSON)**

**Client \-\> Server (example of sending a message):**

JSON  
{  
  "action": "send\_message",  
  "payload": {  
    "text": "Hi, is it safe here?"  
  }  
}

**Server \-\> Client (message broadcast):**

JSON  
{  
  "event": "new\_message",  
  "payload": {  
    "event\_id": "$xyz123...",  
    "sender": "Anonymous \#2",  
    "is\_self": false,  
    "text": "Yes, end-to-end encryption/ephemeral session.",  
    "timestamp": 1714000000000  
  }  
}

#### **6\. Non-functional requirements and security**

> * **Rate Limiting:** Limits on creating rooms and opening WS connections from a single IP (Leaky Bucket / Redis limiter).  
> * **Graceful Shutdown:** Close WebSocket connections properly with code 1001 (Going Away) and stop the Matrix /sync loops.  
> * **Concurreny Model:** Each active WebSocket client gets its own read goroutine (readPump), write goroutine (writePump) and a background Sync() listener from the Matrix SDK.  
> * **CORS / Origin Check:** Strict CheckOrigin validation in the WebSocket upgrader.

#### **7\. Development stages**

> 1. **MVP (Phase 1):** Matrix Client integration, invite link generation, a simple WS bridge for text messages.  
> 2. **Hardening (Phase 2):** session TTL cleaner, m.typing, participant statuses, a limit on the number of participants per room (1-on-1).  
> 3. **Advanced (Phase 3, optional):** Matrix E2EE support (Olm/Megolm via the Rust library matrix-sdk-crypto or gomautrix crypto).
