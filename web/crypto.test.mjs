import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import * as e2ee from './crypto.js';

// The server validates blob shapes with constants in internal/chat/envelope.go.
// Read them from there so the two languages cannot drift apart silently.
const envelope = readFileSync(new URL('../internal/chat/envelope.go', import.meta.url), 'utf8');
const goConst = (name) => {
  const m = envelope.match(new RegExp(`\\b${name}\\s*=\\s*(\\d+)`));
  assert.ok(m, `constant ${name} not found in envelope.go`);
  return Number(m[1]);
};
const NONCE = goConst('NonceBytes'), TAG = goConst('TagBytes');
const GO = { pad: goConst('PadBlock'), maxPadded: goConst('MaxPadded'), publicKey: goConst('PublicKeyBytes'), mac: goConst('MACBytes') };
const validMessage = (n) => n - NONCE - TAG >= GO.pad && n - NONCE - TAG <= GO.maxPadded && (n - NONCE - TAG) % GO.pad === 0;
const size = (b64) => e2ee.fromB64(b64).length;

async function pair(password = '') {
  const secret = e2ee.randomSecret();
  const pwKey = await e2ee.passwordKey(secret, password);
  const creatorSecrets = await e2ee.roomSecrets(secret, pwKey);
  const guestSecrets = await e2ee.roomSecrets(secret, await e2ee.passwordKey(secret, password));
  const c = await e2ee.generateKeyPair(), g = await e2ee.generateKeyPair();
  return { secret, creatorSecrets, guestSecrets, c, g };
}

test('base64url и padding', () => {
  const bytes = Uint8Array.from([0, 251, 255, 1]);
  assert.deepEqual(e2ee.fromB64(e2ee.toB64(bytes)), bytes);
  assert.throws(() => e2ee.fromB64('a+b/'));
  for (const n of [0, 1, 255, 256, 4000]) {
    const padded = e2ee.pad(new Uint8Array(n).fill(7));
    assert.equal(padded.length % e2ee.PAD_BLOCK, 0);
    assert.equal(e2ee.unpad(padded).length, n);
  }
  assert.throws(() => e2ee.unpad(new Uint8Array(256)));
});

test('verifier зависит от пароля и совпадает у сторон', async () => {
  const plain = await pair();
  assert.equal(plain.creatorSecrets.verifier, plain.guestSecrets.verifier);
  assert.equal(size(plain.creatorSecrets.verifier), 32);
  const pwKey = await e2ee.passwordKey(plain.secret, 'кот');
  const withPassword = await e2ee.roomSecrets(plain.secret, pwKey);
  assert.notEqual(withPassword.verifier, plain.creatorSecrets.verifier);
});

test('доли ключа подписаны с ролью', async () => {
  const { creatorSecrets, guestSecrets, c, g } = await pair();
  const share = await e2ee.signShare(creatorSecrets, 'creator', c.publicKey);
  assert.equal(size(share.public_key), 65);
  assert.ok(await e2ee.verifyShare(guestSecrets, 'creator', share));
  assert.equal(await e2ee.verifyShare(guestSecrets, 'guest', share), false);
  assert.equal(await e2ee.verifyShare(guestSecrets, 'creator', { ...share, public_key: g.publicKey }), false);
  const other = await e2ee.roomSecrets(e2ee.randomSecret());
  assert.equal(await e2ee.verifyShare(other, 'creator', share), false);
});

test('обе стороны получают один ключ и код сверки', async () => {
  const { creatorSecrets, guestSecrets, c, g } = await pair('пароль');
  const kc = await e2ee.messageKey(creatorSecrets, c.privateKey, c.publicKey, g.publicKey, 'creator');
  const kg = await e2ee.messageKey(guestSecrets, g.privateKey, c.publicKey, g.publicKey, 'guest');
  const meta = { roomId: 'room', senderId: 'alice', clientId: 'uuid-1' };
  const ct = await e2ee.encryptMessage(kc, { ...meta, counter: 7, prev: 5, text: 'Привет <b>' });
  assert.ok(validMessage(size(ct)));
  assert.deepEqual(await e2ee.decryptMessage(kg, { ...meta, ciphertext: ct }), { counter: 7, prev: 5, text: 'Привет <b>' });
  await assert.rejects(e2ee.decryptMessage(kg, { ...meta, senderId: 'bob', ciphertext: ct }));
  assert.equal(await e2ee.safetyCode(c.publicKey, g.publicKey), await e2ee.safetyCode(c.publicKey, g.publicKey));
  assert.match(await e2ee.safetyCode(c.publicKey, g.publicKey), /^\d{4} \d{4} \d{4}$/);
});

test('размеры совпадают с проверками сервера (envelope.go)', async () => {
  const { creatorSecrets, c, g } = await pair();
  assert.equal(e2ee.PAD_BLOCK, GO.pad);
  assert.equal(size(await e2ee.encryptTitle(creatorSecrets, '')), NONCE + GO.pad + TAG);
  assert.equal(size(await e2ee.encryptTitle(creatorSecrets, '😀'.repeat(60))), NONCE + GO.pad + TAG);
  const share = await e2ee.signShare(creatorSecrets, 'creator', c.publicKey);
  assert.equal(size(share.public_key), GO.publicKey);
  assert.equal(e2ee.fromB64(share.public_key)[0], 4);
  assert.equal(size(share.mac), GO.mac);
  assert.equal(size(creatorSecrets.verifier), GO.mac);
  assert.equal(await e2ee.decryptTitle(creatorSecrets, await e2ee.encryptTitle(creatorSecrets, 'Вечер')), 'Вечер');
  const k = await e2ee.messageKey(creatorSecrets, c.privateKey, c.publicKey, g.publicKey, 'creator');
  const max = await e2ee.encryptMessage(k, { roomId: 'r', senderId: 's', clientId: 'c', counter: 1, text: 'я'.repeat(2000) });
  assert.equal(size(max), NONCE + GO.maxPadded + TAG);
  assert.ok(validMessage(size(max)));
  assert.ok(validMessage(size(await e2ee.encryptMessage(k, { roomId: 'r', senderId: 's', clientId: 'c', counter: 1, text: 'a' }))));
  await assert.rejects(e2ee.encryptMessage(k, { roomId: 'r', senderId: 's', clientId: 'c', counter: 1, text: 'a'.repeat(4001) }));
});

test('ключи неизвлекаемы', async () => {
  const { creatorSecrets, c } = await pair();
  assert.equal(c.privateKey.extractable, false);
  assert.equal(creatorSecrets.titleKey.extractable, false);
  assert.equal(creatorSecrets.macKey.extractable, false);
  await assert.rejects(crypto.subtle.exportKey('jwk', c.privateKey));
});

test('checkCounters находит повторы и удалённые сообщения', () => {
  const flags = e2ee.checkCounters([
    { senderId: 'a', counter: 1, prev: 0 },
    { senderId: 'b', counter: 1, prev: 0 },
    { senderId: 'a', counter: 3, prev: 1 }, // counter 2 failed to send: not an alarm
    { senderId: 'a', counter: 3, prev: 1 }, // replay
    { senderId: 'a', counter: 2, prev: 1 }, // reordered
    { senderId: 'b', counter: 3, prev: 2 }, // b:2 was accepted but is gone
    { senderId: 'a', counter: 4, prev: 0 }, // prev unknown (ack lost): no alarm
  ]);
  assert.deepEqual(flags, [
    { missing: false, replay: false }, { missing: false, replay: false }, { missing: false, replay: false },
    { missing: false, replay: true }, { missing: false, replay: true }, { missing: true, replay: false },
    { missing: false, replay: false },
  ]);
});
