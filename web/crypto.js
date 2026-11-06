// End-to-end encryption for nullchat. The server only ever sees the outputs of
// this module; see docs/superpowers/specs/2026-11-06-e2ee-design.md.
const subtle = globalThis.crypto.subtle;
const enc = new TextEncoder();
const dec = new TextDecoder('utf-8', { fatal: true });

export const VERSION = 'nullchat-e2ee-v1';
export const PBKDF2_ITERATIONS = 600_000;
export const PAD_BLOCK = 256;
export const MAX_TEXT_BYTES = 4000;
const NONCE_BYTES = 12;
const EC = { name: 'ECDH', namedCurve: 'P-256' };

const label = (purpose) => enc.encode(`${VERSION} ${purpose}`);
const concat = (...parts) => {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let offset = 0;
  for (const p of parts) { out.set(p, offset); offset += p.length; }
  return out;
};

export function toB64(bytes) {
  let s = '';
  for (const b of new Uint8Array(bytes)) s += String.fromCharCode(b);
  return btoa(s).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}

export function fromB64(text) {
  if (typeof text !== 'string' || !/^[A-Za-z0-9_-]*$/.test(text)) throw new TypeError('invalid base64url');
  const s = atob(text.replaceAll('-', '+').replaceAll('_', '/') + '='.repeat((4 - text.length % 4) % 4));
  return Uint8Array.from(s, (c) => c.charCodeAt(0));
}

export const randomSecret = () => toB64(crypto.getRandomValues(new Uint8Array(32)));

// ISO/IEC 7816-4: 0x80 then zeros up to the next multiple of the block size.
export function pad(bytes, block = PAD_BLOCK) {
  const out = new Uint8Array(Math.ceil((bytes.length + 1) / block) * block);
  out.set(bytes);
  out[bytes.length] = 0x80;
  return out;
}

export function unpad(bytes) {
  let i = bytes.length - 1;
  while (i >= 0 && bytes[i] === 0) i--;
  if (i < 0 || bytes[i] !== 0x80) throw new Error('invalid padding');
  return bytes.subarray(0, i);
}

async function hkdf(ikm, info, salt = label('salt')) {
  const key = await subtle.importKey('raw', ikm, 'HKDF', false, ['deriveBits']);
  return new Uint8Array(await subtle.deriveBits({ name: 'HKDF', hash: 'SHA-256', salt, info }, key, 256));
}

const aesKey = (bits) => subtle.importKey('raw', bits, 'AES-GCM', false, ['encrypt', 'decrypt']);
const hmacKey = (bits, usages) => subtle.importKey('raw', bits, { name: 'HMAC', hash: 'SHA-256' }, false, usages);

// Returns '' without a password so the stored session carries no password material.
export async function passwordKey(secret, password) {
  if (!password) return '';
  const key = await subtle.importKey('raw', enc.encode(password.normalize('NFC')), 'PBKDF2', false, ['deriveBits']);
  const salt = concat(label('password'), fromB64(secret));
  return toB64(await subtle.deriveBits({ name: 'PBKDF2', hash: 'SHA-256', salt, iterations: PBKDF2_ITERATIONS }, key, 256));
}

export async function roomSecrets(secret, pwKey = '') {
  const s = fromB64(secret);
  if (s.length !== 32) throw new Error('invalid room secret');
  const pw = pwKey ? fromB64(pwKey) : new Uint8Array();
  const ikm = concat(s, Uint8Array.of(pw.length ? 1 : 0), pw);
  const [title, mac, join, roomSalt] = await Promise.all(['title', 'handshake', 'join', 'room-salt'].map((p) => hkdf(ikm, label(p))));
  const joinKey = await hmacKey(join, ['sign']);
  return {
    titleKey: await aesKey(title),
    macKey: await hmacKey(mac, ['sign', 'verify']),
    roomSalt,
    verifier: toB64(await subtle.sign('HMAC', joinKey, label('join'))),
  };
}

// The private key is non-extractable: script injected into the page can use it while
// the tab is open but cannot copy it. ECDH public keys are always exportable.
export async function generateKeyPair() {
  const pair = await subtle.generateKey(EC, false, ['deriveBits']);
  return { privateKey: pair.privateKey, publicKey: toB64(await subtle.exportKey('raw', pair.publicKey)) };
}

const shareData = (role, publicKey) => concat(label(`share ${role}`), fromB64(publicKey));

export async function signShare(secrets, role, publicKey) {
  return { public_key: publicKey, mac: toB64(await subtle.sign('HMAC', secrets.macKey, shareData(role, publicKey))) };
}

export async function verifyShare(secrets, role, share) {
  try {
    if (!(await subtle.verify('HMAC', secrets.macKey, fromB64(share.mac), shareData(role, share.public_key)))) return false;
    await subtle.importKey('raw', fromB64(share.public_key), EC, false, []); // rejects points not on the curve
    return true;
  } catch { return false; }
}

export async function messageKey(secrets, privateKey, creatorPublic, guestPublic, selfRole) {
  const peer = await subtle.importKey('raw', fromB64(selfRole === 'creator' ? guestPublic : creatorPublic), EC, false, []);
  const shared = new Uint8Array(await subtle.deriveBits({ name: 'ECDH', public: peer }, privateKey, 256));
  const info = concat(label('messages'), fromB64(creatorPublic), fromB64(guestPublic));
  return aesKey(await hkdf(shared, info, secrets.roomSalt));
}

export async function safetyCode(creatorPublic, guestPublic) {
  const view = new DataView(await subtle.digest('SHA-256', concat(label('safety'), fromB64(creatorPublic), fromB64(guestPublic))));
  return [0, 4, 8].map((o) => String(view.getUint32(o) % 10000).padStart(4, '0')).join(' ');
}

async function seal(key, plaintext, aad) {
  const iv = crypto.getRandomValues(new Uint8Array(NONCE_BYTES));
  return toB64(concat(iv, new Uint8Array(await subtle.encrypt({ name: 'AES-GCM', iv, additionalData: aad }, key, plaintext))));
}

async function open(key, blob, aad) {
  const b = fromB64(blob);
  return new Uint8Array(await subtle.decrypt({ name: 'AES-GCM', iv: b.subarray(0, NONCE_BYTES), additionalData: aad }, key, b.subarray(NONCE_BYTES)));
}

export async function encryptTitle(secrets, title) {
  const bytes = enc.encode(title);
  if (bytes.length >= PAD_BLOCK) throw new RangeError('title too long');
  return seal(secrets.titleKey, pad(bytes), label('title'));
}

export async function decryptTitle(secrets, blob) {
  return dec.decode(unpad(await open(secrets.titleKey, blob, label('title'))));
}

const messageAad = ({ roomId, senderId, clientId }) => enc.encode(JSON.stringify([`${VERSION} message`, roomId, senderId, clientId]));

const HEADER_BYTES = 9; // version, counter, prev

// counter: strictly increasing per sender in transmission order.
// prev: counter of the sender's latest message the server had already accepted
// (0 if none). It lets the peer notice deleted messages without false alarms
// for sends that simply failed.
export async function encryptMessage(key, { roomId, senderId, clientId, counter, prev = 0, text }) {
  const body = enc.encode(text);
  if (body.length > MAX_TEXT_BYTES) throw new RangeError('text too long');
  const header = new Uint8Array(HEADER_BYTES);
  const view = new DataView(header.buffer);
  header[0] = 1;
  view.setUint32(1, counter);
  view.setUint32(5, prev);
  return seal(key, pad(concat(header, body)), messageAad({ roomId, senderId, clientId }));
}

export async function decryptMessage(key, { roomId, senderId, clientId, ciphertext }) {
  const plain = unpad(await open(key, ciphertext, messageAad({ roomId, senderId, clientId })));
  if (plain.length < HEADER_BYTES || plain[0] !== 1) throw new Error('unsupported message version');
  const view = new DataView(plain.buffer, plain.byteOffset);
  return { counter: view.getUint32(1), prev: view.getUint32(5), text: dec.decode(plain.subarray(HEADER_BYTES)) };
}

// Messages must be in sequence order. A counter that does not increase is a replay
// or reordering. A prev that names a message absent from the history means the
// server dropped an accepted message.
export function checkCounters(messages) {
  const last = new Map();
  const seen = new Set();
  return messages.map(({ senderId, counter, prev }) => {
    if (counter <= (last.get(senderId) ?? 0)) return { missing: false, replay: true };
    last.set(senderId, counter);
    const missing = prev > 0 && !seen.has(`${senderId}:${prev}`);
    seen.add(`${senderId}:${counter}`);
    return { missing, replay: false };
  });
}
