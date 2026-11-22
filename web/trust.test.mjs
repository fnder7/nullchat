import test from 'node:test';
import assert from 'node:assert/strict';
import { trustState, badge, collapsible, inviteURL, codeLabel } from './trust.js';

test('ended wins over everything', () => {
  assert.equal(trustState({ ended: true, keyError: true, alone: true, hasInvite: true }), 'hidden');
});

test('key error wins over verification', () => {
  assert.equal(trustState({ keyError: true, code: '1', verified: 'match' }), 'broken');
});

test('alone with invite shows invite, without shows waiting', () => {
  assert.equal(trustState({ alone: true, hasInvite: true }), 'invite');
  assert.equal(trustState({ alone: true, hasInvite: false }), 'waiting');
});

test('two people, no code yet, stays hidden', () => {
  assert.equal(trustState({ alone: false, code: '' }), 'hidden');
});

test('verified values map to states', () => {
  assert.equal(trustState({ code: '1084 2945 3921' }), 'verify');
  assert.equal(trustState({ code: '1084 2945 3921', verified: 'match' }), 'verified');
  assert.equal(trustState({ code: '1084 2945 3921', verified: 'later' }), 'later');
});

test('badge per state', () => {
  assert.deepEqual(badge('verify'), { label: 'Not verified', tone: 'warn' });
  assert.deepEqual(badge('later'), { label: 'Not verified', tone: 'warn' });
  assert.deepEqual(badge('verified'), { label: '✓ Verified', tone: 'ok' });
  assert.deepEqual(badge('broken'), { label: 'Key error', tone: 'danger' });
  for (const state of ['invite', 'waiting', 'hidden']) assert.equal(badge(state), null, state);
});

test('collapsible only after an answer', () => {
  for (const state of ['verified', 'later']) assert.equal(collapsible(state), true, state);
  for (const state of ['hidden', 'broken', 'invite', 'waiting', 'verify']) assert.equal(collapsible(state), false, state);
});

test("invite URL keeps today's format", () => {
  assert.equal(inviteURL('https://x.io', 'tok', 'sec', true), 'https://x.io/#invite=tok&k=sec&pw=1');
  assert.equal(inviteURL('https://x.io', 'tok', 'sec', false), 'https://x.io/#invite=tok&k=sec');
});

test('code label spells digits', () => {
  assert.equal(codeLabel('1084 2945 3921'), 'Safety code: 1 0 8 4, 2 9 4 5, 3 9 2 1');
});
