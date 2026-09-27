CREATE TABLE rooms (
  id text PRIMARY KEY,
  title text NOT NULL,
  status text NOT NULL CHECK (status IN ('open', 'closed', 'expired')),
  expires_at timestamptz NOT NULL,
  closed_at timestamptz,
  next_message_sequence integer NOT NULL DEFAULT 1 CHECK (next_message_sequence > 0),
  message_count integer NOT NULL DEFAULT 0 CHECK (message_count >= 0),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX rooms_expires_at_idx ON rooms (expires_at);

CREATE TABLE participants (
  id text PRIMARY KEY,
  room_id text NOT NULL REFERENCES rooms(id),
  slot smallint NOT NULL CHECK (slot IN (1, 2)),
  name text NOT NULL,
  UNIQUE (room_id, slot), UNIQUE (room_id, name), UNIQUE (room_id, id)
);
CREATE TABLE sessions (
  token_hash bytea PRIMARY KEY,
  participant_id text NOT NULL,
  room_id text NOT NULL,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  last_send_at timestamptz,
  FOREIGN KEY (room_id, participant_id) REFERENCES participants(room_id, id)
);
CREATE INDEX sessions_room_idx ON sessions (room_id);
CREATE TABLE invites (
  token_hash bytea PRIMARY KEY,
  room_id text NOT NULL REFERENCES rooms(id),
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz
);
CREATE TABLE messages (
  id text PRIMARY KEY,
  room_id text NOT NULL,
  participant_id text NOT NULL,
  client_message_id text NOT NULL,
  sequence integer NOT NULL CHECK (sequence > 0),
  text text NOT NULL,
  created_at timestamptz NOT NULL,
  FOREIGN KEY (room_id) REFERENCES rooms(id),
  FOREIGN KEY (room_id, participant_id) REFERENCES participants(room_id, id),
  UNIQUE (room_id, sequence), UNIQUE (room_id, participant_id, client_message_id)
);
CREATE INDEX messages_room_sequence_idx ON messages (room_id, sequence);
CREATE TABLE idempotency_requests (
  key_hash bytea PRIMARY KEY,
  endpoint text NOT NULL,
  request_fingerprint bytea NOT NULL,
  response_ciphertext bytea NOT NULL,
  room_id text REFERENCES rooms(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);
CREATE INDEX idempotency_requests_expires_at_idx ON idempotency_requests (expires_at);
