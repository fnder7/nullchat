import test from 'node:test';
import assert from 'node:assert/strict';
import { linkState, joinOutcome } from './invite.js';

const invite = 'a'.repeat(64);
const key = 'A'.repeat(43); // 43 base64url characters = 32 bytes

test('insecure wins', () => {
  assert.equal(linkState({ invite, k: key, secure: false }), 'insecure');
  assert.equal(linkState({ invite: 'short', k: '', secure: false }), 'insecure');
});

test('well-formed link is ready', () => {
  assert.equal(linkState({ invite, k: key, secure: true }), 'ready');
});

test('short token is damaged', () => {
  assert.equal(linkState({ invite: 'a'.repeat(63), k: key, secure: true }), 'damaged');
});

test('missing, short or invalid key is damaged', () => {
  assert.equal(linkState({ invite, k: '', secure: true }), 'damaged');
  assert.equal(linkState({ invite, k: 'A'.repeat(22), secure: true }), 'damaged'); // 16 bytes
  assert.equal(linkState({ invite, k: 'A'.repeat(42) + '+', secure: true }), 'damaged');
});

test('join codes map to states', () => {
  assert.equal(joinOutcome('room_unavailable'), 'closed');
  assert.equal(joinOutcome('invalid_input'), 'damaged');
  assert.equal(joinOutcome('join_proof_invalid'), 'field');
  assert.equal(joinOutcome('room_full'), 'transient');
  assert.equal(joinOutcome(undefined), 'transient');
});
