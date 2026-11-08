-- End-to-end encryption (ARCHITECTURE.md, decision 006): the server stores only
-- opaque client-encrypted fields and key shares. Rooms created before this
-- migration hold plaintext that E2EE clients cannot read, so they are ended and
-- their plaintext is removed. Rooms live at most 60 minutes.
UPDATE rooms SET status = 'closed', closed_at = COALESCE(closed_at, clock_timestamp()) WHERE status = 'open';
UPDATE sessions SET revoked_at = COALESCE(revoked_at, clock_timestamp());
UPDATE invites SET consumed_at = COALESCE(consumed_at, clock_timestamp());
DELETE FROM idempotency_requests;
DELETE FROM messages;
UPDATE rooms SET title = '';

ALTER TABLE rooms RENAME COLUMN title TO encrypted_title;
ALTER TABLE rooms
  ADD COLUMN join_verifier_hash bytea NOT NULL DEFAULT '\x'::bytea,
  ADD COLUMN failed_joins integer NOT NULL DEFAULT 0 CHECK (failed_joins >= 0);
-- Role is derived from slot: 1 is the creator, 2 the guest.
ALTER TABLE participants
  ADD COLUMN public_key text NOT NULL DEFAULT '',
  ADD COLUMN key_mac text NOT NULL DEFAULT '';
ALTER TABLE messages RENAME COLUMN text TO ciphertext;
