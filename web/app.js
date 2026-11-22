import * as e2ee from './crypto.js';
import * as keystore from './keystore.js';
import { linkify } from './linkify.js';
import { linkState, joinOutcome } from './invite.js';
import { needsReload } from './nav.js';
import { trustState, badge, collapsible, inviteURL, codeLabel } from './trust.js';

const $ = (id) => document.getElementById(id);
const MOBILE = matchMedia('(max-width:700px)');
const MAX_RECONNECT_ATTEMPTS = 8;
const DEFAULT_TITLE = 'Private conversation';
const state = {
  credentials: null, socket: null, room: null, self: null, invite: '', inviteShown: '',
  // e2ee: what survives reload in sessionStorage. Keys live in IndexedDB (keystore.js).
  e2ee: null, secrets: null, privateKey: null, messageKey: null, peers: '', keyError: false, title: '',
  plain: new Map(), flags: new Map(), events: Promise.resolve(), outbox: Promise.resolve(), lastAccepted: 0, lastSequence: 0,
  pending: new Map(), failed: new Map(), accepted: new Map(),
  ended: false, connected: false, retry: 0, reconnectTimer: null, totalSeconds: 0,
  unseen: 0, unread: 0, timerWarned: false, notification: null,
  trustOpen: false, composerFocus: false, atBottom: true, trustShown: '', safetyCode: undefined,
};
// A create/join request is encrypted once and reused verbatim on retry, so the
// Idempotency-Key replays the same room with key shares this tab still holds.
const prepared = { create: null, join: null };
const errors = {
  room_unavailable: 'This invite has already been used, or the room is closed or has expired.',
  session_invalid: 'This session is no longer available. The room may have expired or the conversation has ended.',
  invalid_input: 'Check your input. The title can be up to 60 characters, a message 1 to 4000 bytes.',
  capacity_reached: 'The connection or message limit has been reached for now. Try again later.',
  room_full: 'This room already has two participants.',
  message_limit: 'This room has reached its message limit.',
  connection_limit: 'The connection limit has been reached. Try again later.',
  database_unavailable: 'The server is temporarily unavailable. Your session is saved, try again later.',
  idempotency_key_conflict: 'This retry key was already used for a different request.',
  rate_limited: 'Too many requests. Wait a moment and try again.',
  message_id_conflict: 'This message ID was already used for a different message.',
  origin_forbidden: 'Connections from this address are not allowed.',
  join_proof_invalid: 'Wrong password or damaged link. After 5 failed attempts the invite is revoked.',
};

function showError(id, text = '') { $(id).textContent = text; $(id).hidden = !text; }
function save() {
  if (!state.credentials) return;
  sessionStorage.setItem(`nullchat:${state.room.id}`, JSON.stringify({ ...state.credentials, room: { ...state.room, messages: [] }, invite_token: state.invite, e2ee: state.e2ee }));
}
async function api(path, data, token, key = '', errorOverrides = {}) {
  const headers = { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(key ? { 'Idempotency-Key': key } : {}) };
  const response = await fetch(path, { method: 'POST', headers, body: JSON.stringify(data), signal: AbortSignal.timeout(10000) });
  const result = await response.json();
  if (!response.ok) {
    const err = new Error(errorOverrides[result.error] || errors[result.error] || 'The request failed. Please try again.');
    err.code = result.error;
    throw err;
  }
  return result;
}
function checkStorage() {
  try { sessionStorage.setItem('nullchat:check', '1'); sessionStorage.removeItem('nullchat:check'); }
  catch { throw new Error('A temporary session needs access to tab storage. Allow it in your browser settings.'); }
}
const keyId = () => keystore.keyId(state.room.id, state.self.id);
async function storeKeys(keys) {
  // A number, not the server string: an unparsable date must never look expired.
  const expires = Date.parse(state.room.expires_at) || Date.now() + 61 * 60 * 1000;
  try { await keystore.putKeys(keyId(), { expires_ms: expires, ...keys }); }
  catch { showError('chat-error', 'The browser did not let us store the keys: reloading this tab will not restore the session.'); }
}
async function loadKeys() {
  try { return await keystore.getKeys(keyId()); } catch { return null; }
}

// fresh = { secrets, privateKey } right after create/join; absent on reload.
async function enter(credentials, fresh = null) {
  state.credentials = credentials;
  state.room = credentials.room;
  state.self = credentials.self;
  state.invite = credentials.invite_token || '';
  state.e2ee = credentials.e2ee;
  const keys = fresh
    ? { privateKey: fresh.privateKey, titleKey: fresh.secrets.titleKey, macKey: fresh.secrets.macKey, roomSalt: fresh.secrets.roomSalt }
    : await loadKeys();
  if (!keys) throw new Error('This tab has no keys for the room.');
  if (fresh) await storeKeys(keys);
  state.secrets = { titleKey: keys.titleKey, macKey: keys.macKey, roomSalt: keys.roomSalt };
  state.privateKey = keys.privateKey;
  state.peers = state.e2ee.peers || '';
  try { state.title = (await e2ee.decryptTitle(state.secrets, state.room.encrypted_title)) || DEFAULT_TITLE; }
  catch { state.title = 'Title could not be decrypted'; }
  state.totalSeconds = Math.max(1, (Date.parse(state.room.expires_at) - Date.now()) / 1000);
  save();
  history.replaceState(null, '', `/#room=${state.room.id}`);
  $('home').hidden = true;
  $('invite').hidden = true;
  $('chat').hidden = false;
  document.body.classList.add('in-chat');
  syncViewport();
  $('room-title').textContent = state.title;
  $('chat-head-title').textContent = state.title;
  // The control that had focus is hidden now; without this, focus falls back to <body>.
  if (MOBILE.matches) $('chat-head-title').focus(); else $('room-title').focus();
  updateTitle();
  await establishKey();
  renderParticipants();
  renderMessages();
  renderNotify();
  updateTimer();
  connect();
}

const KEY_NOTE = 'Your contact\'s key was replaced or the link is damaged. Do not continue: end the chat and create a new room.';
function keyFailure() {
  closeSheet(); // the open sheet makes .conversation inert, which would silence the alert
  state.keyError = true;
  state.messageKey = null;
  renderTrust();
  // The strip is not a live region, and this is the one warning nobody may miss.
  $('safety-alert').textContent = `Key verification failed. ${KEY_NOTE}`;
  connection(state.connected, $('connection-status').textContent);
}

// Verifies both key shares against the link secret (and password), then derives the
// message key. Any mismatch — including a key swapped mid-conversation — is fatal.
async function establishKey() {
  if (state.keyError) return;
  const byRole = (role) => state.room.participants.find((p) => p.role === role);
  const creator = byRole('creator'), guest = byRole('guest');
  const own = state.room.participants.find((p) => p.id === state.self.id);
  if (!own || own.key_share?.public_key !== state.e2ee.public_key) return keyFailure();
  for (const p of [creator, guest]) {
    if (p && p.id !== own.id && !(await e2ee.verifyShare(state.secrets, p.role, p.key_share))) return keyFailure();
  }
  if (!creator || !guest) {
    if (state.peers) return keyFailure(); // a participant cannot leave once the key exists
    return;
  }
  // Pinned in sessionStorage, so a key swapped across a reload is caught too.
  const peers = `${creator.key_share.public_key}.${guest.key_share.public_key}`;
  if (state.peers && state.peers !== peers) return keyFailure();
  if (!state.messageKey) {
    state.messageKey = await e2ee.messageKey(state.secrets, state.privateKey, creator.key_share.public_key, guest.key_share.public_key, own.role);
    state.plain.clear();
    state.safetyCode = await e2ee.safetyCode(creator.key_share.public_key, guest.key_share.public_key);
  }
  if (state.peers === peers) return;
  state.peers = peers;
  state.e2ee.peers = peers;
  // The guest is in: the invite and the link secret are no longer needed anywhere.
  delete state.e2ee.secret;
  state.invite = '';
  save();
}

function renderParticipants() {
  $('participants').replaceChildren();
  for (const p of state.room.participants) {
    const li = document.createElement('li');
    const avatar = document.createElement('span'); avatar.className = 'avatar'; avatar.textContent = p.name[0];
    const name = document.createElement('span'); name.textContent = p.name;
    li.append(avatar, name);
    if (p.id === state.self.id) { const tag = document.createElement('small'); tag.textContent = 'you'; li.append(tag); }
    $('participants').append(li);
  }
  const status = state.room.participants.length === 1 && !state.ended ? 'Waiting for your contact' : '';
  if ($('participants-status').textContent !== status) $('participants-status').textContent = status; // a rewrite may be re-announced
  renderTrust();
}
// The strip above the log. The body is rebuilt only when the derived state changes, so
// participant re-renders keep the focus and the Copy feedback. verified and later show the
// strip only while state.trustOpen; every other state decides for itself.
const trustNow = () => trustState({
  ended: state.ended, keyError: state.keyError, alone: state.room?.participants.length === 1,
  hasInvite: !!(state.invite && state.e2ee?.secret), code: state.safetyCode, verified: state.e2ee?.verified,
});
function renderTrust() {
  state.composerFocus = document.activeElement === $('message'); // a disabled composer may lose focus without a focusout
  // Two people but no code yet (the key is still being derived): keep what is shown.
  if (!state.ended && !state.keyError && state.room.participants.length > 1 && !state.safetyCode) return;
  const next = trustNow();
  // The keyboard needs the room: hide the strip while typing. state.e2ee.verified and state.trustOpen stay as they are.
  const forComposer = MOBILE.matches && state.composerFocus && ['verify', 'verified', 'later'].includes(next);
  const shown = forComposer || (collapsible(next) && !state.trustOpen) ? 'hidden' : next;
  const b = badge(next);
  $('trust-badge').hidden = !b;
  if (b) { $('trust-badge').textContent = b.label; $('trust-badge').dataset.tone = b.tone; }
  $('trust-badge').setAttribute('aria-expanded', String(shown !== 'hidden'));
  if (shown === state.trustShown) return;
  const before = state.trustShown;
  state.trustShown = shown;
  const body = $('trust-body');
  const hadFocus = $('trust-strip').contains(document.activeElement);
  body.replaceChildren();
  $('trust-strip').hidden = shown === 'hidden';
  $('trust-strip').dataset.tone = shown === 'waiting' || shown === 'broken' ? 'danger' : '';
  if (shown === 'invite') buildInvite(body);
  else if (shown === 'waiting') {
    $('trust-title').textContent = 'Invite revoked';
    const p = document.createElement('p'); p.textContent = 'The invite was revoked after 5 wrong password attempts. End the chat and create a new room.';
    body.append(p);
  } else if (shown === 'verify' || shown === 'later') {
    buildVerify(body);
    if (shown === 'verify' && before === 'invite') $('trust-status').textContent = 'Your contact joined. Compare the safety code.';
  } else if (shown === 'verified') buildVerified(body);
  else if (shown === 'broken') buildBroken(body);
  if (hadFocus && shown !== 'hidden') $('trust-title').focus();
}
function trustButton(label, className, onClick) {
  const button = document.createElement('button'); button.type = 'button'; button.className = className; button.textContent = label;
  button.addEventListener('click', onClick);
  return button;
}
function trustCode() {
  const code = document.createElement('strong'); code.className = 'trust-code';
  code.textContent = state.safetyCode; code.setAttribute('aria-label', codeLabel(state.safetyCode));
  return code;
}
function buildVerify(body) {
  $('trust-title').textContent = 'Is this really them?';
  const text = document.createElement('p'); text.textContent = 'Compare this code with your contact by voice or in another app. If it matches, you are talking to the right person.';
  const actions = document.createElement('div'); actions.className = 'trust-actions';
  const mismatch = trustButton("It doesn't match", 'danger', () => openEndDialog('mismatch', mismatch));
  actions.append(trustButton('It matches', 'primary trust-primary', () => setVerified('match')), mismatch, trustButton('Later', 'trust-link-button', () => setVerified('later')));
  body.append(trustCode(), text, actions);
}
function buildVerified(body) {
  $('trust-title').textContent = 'Safety code';
  const text = document.createElement('p'); text.textContent = 'You confirmed this code matches.';
  const actions = document.createElement('div'); actions.className = 'trust-actions';
  actions.append(
    trustButton('Compare again', 'trust-link-button', () => setVerified(undefined)),
    trustButton('Hide', 'trust-link-button', () => { state.trustOpen = false; renderTrust(); $('trust-badge').focus(); }));
  body.append(trustCode(), text, actions);
}
function buildBroken(body) {
  $('trust-title').textContent = 'Key verification failed';
  const note = document.createElement('p'); note.textContent = KEY_NOTE;
  const actions = document.createElement('div'); actions.className = 'trust-actions';
  const end = trustButton('End the chat', 'danger', () => openEndDialog('broken', end));
  actions.append(end);
  body.append(note, actions);
}
// The verdict stays in this tab's sessionStorage: it is never sent to the server or the contact.
function setVerified(value) {
  if (value === undefined) delete state.e2ee.verified; else state.e2ee.verified = value;
  save();
  state.trustOpen = false;
  renderTrust();
  if (value !== undefined) $('trust-badge').focus();
  if (value === 'match') $('trust-status').textContent = 'Marked as verified.';
}
$('trust-badge').addEventListener('click', () => {
  const now = trustNow();
  if (now === 'broken') $('trust-title').focus();
  else if (now === 'verify') setVerified('later');
  else if (collapsible(now)) { state.trustOpen = !state.trustOpen; renderTrust(); }
});
function buildInvite(body) {
  const url = inviteURL(location.origin, state.invite, state.e2ee.secret, state.e2ee.has_password);
  $('trust-title').textContent = 'Send this link to your contact';
  const warn = document.createElement('p'); warn.textContent = 'Anyone who opens this link takes the seat. Send it somewhere private.';
  const link = document.createElement('p'); link.className = 'trust-link'; link.textContent = url;
  const actions = document.createElement('div'); actions.className = 'trust-actions';
  const copy = document.createElement('button'); copy.type = 'button'; copy.className = 'secondary'; copy.textContent = 'Copy link';
  copy.addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(url); copy.textContent = 'Link copied'; }
    catch { getSelection().selectAllChildren(link); copy.textContent = 'Copy the selected link'; }
    setTimeout(() => { copy.textContent = 'Copy link'; }, 3000);
  });
  actions.append(copy);
  if (typeof navigator.share === 'function') {
    const share = document.createElement('button'); share.type = 'button'; share.className = 'secondary'; share.textContent = 'Share…';
    share.addEventListener('click', async () => {
      try { await navigator.share({ url }); } catch (e) { if (e.name !== 'AbortError') copy.click(); }
    });
    actions.append(share);
  }
  body.append(warn, link);
  if (state.e2ee.has_password) { const pw = document.createElement('p'); pw.textContent = 'Send the password separately.'; body.append(pw); }
  body.append(actions);
}
function buildMessage(message, pending = false) {
  const plain = message.id ? state.plain.get(message.id) : { text: message.text };
  const flag = message.id ? state.flags.get(message.id) : null;
  if (flag?.replay) return null; // replayed or reordered by the server; renderMessages explains
  const self = message.sender_id === state.self.id;
  const status = pending === true ? 'pending' : pending || 'accepted';
  const article = document.createElement('article'); article.className = `message${self ? ' self' : ''} ${status}${plain?.error ? ' undecryptable' : ''}`;
  // The same key while a message goes pending -> accepted, so its node is patched, not re-announced.
  article.dataset.key = `${message.sender_id}:${message.client_message_id}`;
  article.dataset.group = `${message.sender_id}|${status}`;
  const meta = document.createElement('div'); meta.className = 'message-meta';
  const name = document.createElement('span'); name.textContent = self ? 'You' : (state.room.participants.find((p) => p.id === message.sender_id)?.name || 'Contact');
  const time = document.createElement('span'); time.textContent = status === 'pending' ? 'Sending…' : status === 'failed' ? 'Not sent' : new Date(message.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  meta.append(name, time);
  if (self && status === 'accepted') {
    const check = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    check.setAttribute('class', 'sent-check'); check.setAttribute('viewBox', '0 0 12 12'); check.setAttribute('aria-hidden', 'true');
    const tick = document.createElementNS('http://www.w3.org/2000/svg', 'path'); tick.setAttribute('d', 'M2 6.5l2.5 2.5L10 3.5');
    check.append(tick);
    const sr = document.createElement('span'); sr.className = 'sr-only'; sr.textContent = 'Sent';
    meta.append(check, sr);
  }
  article.dataset.group += `|${time.textContent}`;
  const bubble = document.createElement('div'); bubble.className = 'bubble';
  if (plain?.error) bubble.textContent = '⚠ This message could not be decrypted';
  else fillText(bubble, plain?.text ?? '…');
  article.append(meta, bubble);
  if (flag?.missing) {
    const note = document.createElement('p'); note.className = 'message-note';
    note.textContent = 'A message the server had already accepted is missing before this one. The server may have deleted it.';
    article.append(note);
  }
  if (status === 'failed') {
    const footer = document.createElement('div'); footer.className = 'message-actions';
    const reason = document.createElement('span'); reason.textContent = message.error || 'Temporary error';
    const retry = document.createElement('button'); retry.type = 'button'; retry.className = 'retry-message'; retry.dataset.retryId = message.client_message_id; retry.textContent = 'Retry';
    footer.append(reason, retry); article.append(footer);
  }
  return article;
}
// Links are built as nodes, never as markup: the text itself is the visible label,
// so what the reader sees is where the link goes.
function fillText(parent, text) {
  for (const segment of linkify(text)) {
    if (!segment.href) { parent.append(segment.text); continue; }
    const a = document.createElement('a');
    a.href = segment.href; a.textContent = segment.text;
    a.target = '_blank'; a.rel = 'noopener noreferrer nofollow'; a.referrerPolicy = 'no-referrer';
    a.title = `Opens ${new URL(segment.href).hostname} in a new tab`;
    parent.append(a);
  }
}
function buildEmptyState() {
  const alone = state.room.participants.length === 1;
  const empty = document.createElement('div'); empty.className = 'empty-state'; empty.dataset.key = `empty:${alone}`;
  const symbol = document.createElement('span'); symbol.className = 'empty-symbol'; symbol.textContent = '[ … ]';
  const title = document.createElement('strong'); title.textContent = alone ? 'The conversation is still ahead' : 'You are both here';
  const p = document.createElement('p'); p.textContent = alone ? 'Your contact has not joined yet.' : 'Time to say hello.';
  empty.append(symbol, title, p);
  return empty;
}
// Copies what differs from a freshly built node into the one already in the log. Text
// is written only where it changed, so a screen reader hears the change and no more.
function patchNode(node, fresh) {
  if (node.isEqualNode(fresh)) return;
  // Mixed content (text around links): text nodes are not children, so swap it whole.
  const mixed = [...node.childNodes, ...fresh.childNodes].some((n) => n.nodeType === Node.TEXT_NODE) && (node.childElementCount || fresh.childElementCount);
  if (mixed || node.childElementCount !== fresh.childElementCount) { node.replaceChildren(...fresh.childNodes); node.className = fresh.className; return; }
  node.className = fresh.className;
  [...node.children].forEach((child, i) => {
    const next = fresh.children[i];
    if (child.childElementCount || next.childElementCount) { if (!child.isEqualNode(next)) patchNode(child, next); }
    else if (child.textContent !== next.textContent) child.textContent = next.textContent;
    if (child.className !== next.className) child.className = next.className;
  });
}
// #messages is a live region (role="log"): rebuilding it would make a screen reader read
// the whole history again on every event. Nodes are reused by key; only new ones are added.
function renderMessages() {
  const box = $('messages');
  const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 100;
  const oldTop = box.scrollTop;
  const wanted = [];
  if (!state.room.messages.length && !state.pending.size && !state.failed.size) wanted.push(buildEmptyState());
  state.room.messages.forEach((m) => wanted.push(buildMessage(m)));
  state.pending.forEach((m) => wanted.push(buildMessage(m, true)));
  state.failed.forEach((m) => wanted.push(buildMessage(m, 'failed')));
  const current = new Map([...box.children].map((node) => [node.dataset.key, node]));
  const keep = new Set();
  let index = 0;
  let prevGroup = '';
  for (const fresh of wanted) {
    if (!fresh || keep.has(fresh.dataset.key)) continue;
    // Same sender, same status, same minute as the message above: the name and time are
    // not repeated on screen (they stay for screen readers).
    const group = fresh.dataset.group || '';
    fresh.classList.toggle('continued', group !== '' && group === prevGroup);
    prevGroup = group;
    let node = current.get(fresh.dataset.key);
    if (node) patchNode(node, fresh); else node = fresh;
    keep.add(node.dataset.key);
    if (box.children[index] !== node) box.insertBefore(node, box.children[index] || null);
    index += 1;
  }
  for (const node of [...box.children]) if (!keep.has(node.dataset.key)) node.remove();
  if ([...state.flags.values()].some((f) => f.replay)) showError('chat-error', 'The server replayed or reordered a message. It is hidden.');
  $('message-count').hidden = !(state.room.messages.length >= 450);
  $('message-count').textContent = `${state.room.messages.length} / 500 messages`;
  box.scrollTop = atBottom ? box.scrollHeight : oldTop;
}

// Decrypts new history entries and re-checks per-sender counters in sequence order.
async function decryptAll() {
  if (!state.messageKey) return;
  for (const m of state.room.messages) {
    if (state.plain.has(m.id)) continue;
    try { state.plain.set(m.id, await e2ee.decryptMessage(state.messageKey, { roomId: state.room.id, senderId: m.sender_id, clientId: m.client_message_id, ciphertext: m.ciphertext })); }
    catch { state.plain.set(m.id, { error: true }); }
  }
  const ok = state.room.messages.filter((m) => !state.plain.get(m.id)?.error);
  const flags = e2ee.checkCounters(ok.map((m) => ({ senderId: m.sender_id, counter: state.plain.get(m.id).counter, prev: state.plain.get(m.id).prev })));
  state.flags = new Map(ok.map((m, i) => [m.id, flags[i]]));
  const own = ok.filter((m, i) => m.sender_id === state.self.id && !flags[i].replay).map((m) => state.plain.get(m.id).counter);
  state.lastAccepted = Math.max(0, ...own);
  const next = Math.max(state.e2ee.next_counter, state.lastAccepted + 1);
  if (next !== state.e2ee.next_counter) { state.e2ee.next_counter = next; save(); }
}
const nearBottom = (box = $('messages')) => box.scrollHeight - box.scrollTop - box.clientHeight < 100;
function showUnseen() {
  $('jump-button').hidden = !state.unseen;
  $('jump-button').textContent = `${state.unseen} new message${state.unseen === 1 ? '' : 's'} ↓`;
}
function updateTitle() {
  document.title = `${state.unread ? `(${state.unread}) ` : ''}${state.title} · nullchat`;
}
// A message from the contact that lands out of view: a jump button inside the chat,
// a counter in the tab title while the tab is in the background.
function noteIncoming(message) {
  if (!nearBottom()) { state.unseen += 1; showUnseen(); }
  if (document.hidden) {
    state.unread += 1; updateTitle();
    const sender = state.room.participants.find((p) => p.id === message.sender_id)?.name || 'Your contact';
    notify(state.unread === 1 ? `${sender} sent a message` : `${sender}: ${state.unread} new messages`);
  }
}

// System notifications, opt-in, only while the tab is in the background. They never
// carry message text or the room title: the OS keeps notifications in its own history,
// outside end-to-end encryption. The sender's pseudonym is already known to the server.
const NOTIFY_KEY = 'nullchat:notify';
const notifySupported = () => 'Notification' in globalThis;
const notifyOn = () => notifySupported() && Notification.permission === 'granted' && sessionStorage.getItem(NOTIFY_KEY) === '1';
function renderNotify() {
  $('notify-panel').hidden = state.ended;
  const blocked = !notifySupported() || Notification.permission === 'denied';
  $('notify-button').hidden = blocked;
  if (blocked) { $('notify-note').textContent = 'Notifications are blocked in this browser.'; return; }
  $('notify-button').textContent = notifyOn() ? 'Turn off notifications' : 'Turn on notifications';
  $('notify-note').textContent = 'Shown only while this tab is in the background, without the message text.';
}
function notify(body) {
  if (!notifyOn()) return;
  state.notification?.close();
  try {
    // One notification per room: a newer one replaces the last instead of piling up.
    const n = new Notification('nullchat', { body, tag: `nullchat:${state.room.id}`, icon: '/favicon.svg' });
    n.onclick = () => { window.focus(); n.close(); };
    state.notification = n;
  } catch { /* e.g. Android Chrome allows only service-worker notifications */ }
}
function clearNotification() { state.notification?.close(); state.notification = null; }
async function receiveMessage(message) {
  const incoming = message.sender_id !== state.self.id && !state.room.messages.some((m) => m.id === message.id);
  if (!state.room.messages.some((m) => m.id === message.id)) state.room.messages.push(message);
  state.room.messages.sort((a, b) => a.sequence - b.sequence);
  await decryptAll();
  if (message.sender_id === state.self.id) {
    state.accepted.set(message.client_message_id, message);
    state.pending.delete(message.client_message_id);
    state.failed.delete(message.client_message_id);
  }
  renderMessages();
  if (incoming && !state.flags.get(message.id)?.replay) noteIncoming(message);
}
function connection(connected, label) {
  state.connected = connected;
  $('connection-status').textContent = label;
  $('connection-dot').style.background = connected ? 'var(--accent)' : 'var(--muted)';
  const canSend = connected && !state.ended && Boolean(state.messageKey) && !state.keyError;
  $('message').disabled = !canSend;
  $('send-button').disabled = !canSend || !$('message').value.trim();
  $('message').placeholder = state.messageKey ? 'What\'s on your mind?' : 'Waiting for your contact…';
  $('reconnect-button').hidden = connected || state.ended || state.retry < MAX_RECONNECT_ATTEMPTS;
}
function finish(label) {
  state.ended = true;
  $('chat').classList.add('ended');
  if ($('participants-status').textContent) $('participants-status').textContent = '';
  closeSheet({ restoreFocus: false });
  if ($('end-dialog').open) $('end-dialog').close();
  clearTimeout(state.reconnectTimer);
  connection(false, label);
  state.socket?.close(1000);
  sessionStorage.removeItem(`nullchat:${state.room.id}`);
  keystore.deleteKeys(keyId()).catch(() => {});
  e2ee.wipe(state.secrets?.roomSalt);
  state.secrets = null; state.privateKey = null; state.messageKey = null;
  state.invite = '';
  state.pending.clear();
  state.failed.clear();
  state.accepted.clear();
  state.plain.clear();
  state.room.messages = [];
  $('messages').replaceChildren();
  renderTrust();
  $('end-button').hidden = true;
  $('chat-head-timer').textContent = 'Ended';
  $('chat-head-timer').classList.remove('ending');
  $('sheet-button').hidden = true;
  $('message-form').hidden = true;
  document.querySelector('.composer-hint').hidden = true;
  $('ended-title').textContent = label;
  $('ended-panel').hidden = false;
  state.unseen = 0; showUnseen();
  $('timer-card').classList.remove('ending');
  clearNotification(); renderNotify();
  $('ended-title').focus();
}
function socketIsOpen(socket = state.socket) {
  return socket && socket.readyState === WebSocket.OPEN;
}
function markFailed(entry, reason) {
  state.pending.delete(entry.client_message_id);
  entry.status = 'failed'; entry.error = reason;
  state.failed.set(entry.client_message_id, entry);
  renderMessages();
}
function transmit(entry, socket = state.socket) {
  if (!socketIsOpen(socket) || socket !== state.socket || !state.connected) {
    markFailed(entry, 'No connection. Retry once it is back.');
    return false;
  }
  entry.status = 'pending'; entry.error = '';
  state.failed.delete(entry.client_message_id);
  state.pending.set(entry.client_message_id, entry);
  try {
    // A retry resends the same ciphertext: a fresh nonce would be a message_id_conflict.
    socket.send(JSON.stringify({ type: 'send_message', client_message_id: entry.client_message_id, ciphertext: entry.ciphertext }));
    return true;
  } catch {
    markFailed(entry, 'The message could not be sent. Press “Retry”.');
    return false;
  }
}
// Every send goes through one queue: encryption is async, and counters must reach
// the server in the order they were assigned. The pause respects the server's
// 200 ms per-session send limit.
function enqueue(entry, socket = state.socket) {
  state.outbox = state.outbox.then(async () => {
    if (state.ended || !state.pending.has(entry.client_message_id)) return;
    if (!entry.ciphertext) {
      entry.counter = state.e2ee.next_counter++;
      save();
      entry.ciphertext = await e2ee.encryptMessage(state.messageKey, { roomId: state.room.id, senderId: state.self.id, clientId: entry.client_message_id, counter: entry.counter, prev: state.lastAccepted, text: entry.text });
    }
    if (transmit(entry, socket)) await new Promise((resolve) => setTimeout(resolve, 220));
    else showError('chat-error', 'Message not sent. Press “Retry”.');
  }).catch(() => markFailed(entry, 'The message could not be encrypted. Press “Retry”.'));
}
function scheduleReconnect() {
  if (state.ended || state.reconnectTimer || state.retry >= MAX_RECONNECT_ATTEMPTS) {
    if (!state.ended && state.retry >= MAX_RECONNECT_ATTEMPTS) {
      connection(false, 'Server temporarily unavailable');
      showError('chat-error', 'The server is not responding. Your session is saved, you can reconnect.');
    }
    return;
  }
  const base = Math.min(1000 * 2 ** state.retry, 10000);
  const delay = Math.min(10000, Math.round(base + Math.random() * Math.min(base / 2, 1000)));
  state.retry++;
  connection(false, 'Connection lost. Reconnecting…');
  state.reconnectTimer = setTimeout(() => { state.reconnectTimer = null; connect(); }, delay);
}
async function handle(ws, data) {
  if (state.ended || state.socket !== ws) return;
  const { type, payload } = JSON.parse(data);
  if (type === 'snapshot') {
    state.room = payload.room; state.self = payload.self; state.retry = 0;
    state.lastSequence = state.room.messages.reduce((max, m) => Math.max(max, m.sequence), 0);
    await establishKey();
    await decryptAll();
    for (const m of state.room.messages) {
      if (m.sender_id !== state.self.id) continue;
      state.accepted.set(m.client_message_id, m);
      state.pending.delete(m.client_message_id);
      state.failed.delete(m.client_message_id);
    }
    connection(true, 'Connected');
    showError('chat-error'); renderParticipants(); renderMessages(); save();
    // Unacknowledged messages go first, in counter order, through the same queue.
    [...state.pending.values()].sort((a, b) => a.counter - b.counter).forEach((m) => enqueue(m, ws));
  } else if (type === 'message') {
    // Message events follow the snapshot in sequence order with no gaps. A skipped
    // sequence means a message was stored but its event never reached this tab:
    // recover the history rather than report it as deleted by the server.
    if (payload.sequence > state.lastSequence + 1) { resync(); return; }
    state.lastSequence = Math.max(state.lastSequence, payload.sequence);
    await receiveMessage(payload);
  } else if (type === 'ack') await receiveMessage(payload); // may overtake the peer's event; not part of the gap check
  else if (type === 'participants') {
    const joined = payload.length > state.room.participants.length && payload.some((p) => p.id !== state.self.id);
    state.room.participants = payload;
    if (joined && document.hidden) notify('Your contact joined the room');
    await establishKey();
    await decryptAll();
    connection(state.connected, $('connection-status').textContent);
    renderParticipants(); renderMessages(); save();
  } else if (type === 'invite_revoked') {
    state.invite = ''; delete state.e2ee.secret; save(); renderParticipants();
  }
  else if (type === 'room_expired') finish('The conversation time is over');
  else if (type === 'room_closed') finish('Conversation ended');
  else if (type === 'server_shutdown') { connection(false, 'Server restarting…'); showError('chat-error', 'The server is restarting. Your session is saved.'); }
  else if (type === 'error') {
    if (payload.code === 'session_invalid') { finish('The room is no longer available'); return; }
    const failed = state.pending.get(payload.client_message_id);
    if (failed) markFailed(failed, errors[payload.code] || 'The message could not be sent. Press “Retry”.');
    showError('chat-error', errors[payload.code] || 'The message could not be sent.');
    // The commit may have succeeded even though the server reported a failure. The
    // snapshot settles it: a stored message leaves the failed list, so "Retry"
    // cannot send it a second time.
    if (failed && payload.code === 'database_unavailable') resync();
  }
}
// The event stream skipped a message, or a send ended with an unknown outcome:
// fetch a fresh snapshot instead of guessing what the server stored.
function resync() {
  if (state.ended) return;
  const old = state.socket;
  connect();
  old?.close(1000);
}
function connect() {
  if (state.ended) return;
  clearTimeout(state.reconnectTimer); state.reconnectTimer = null;
  connection(false, state.retry ? 'Restoring the connection…' : 'Connecting…');
  const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/ws`);
  state.socket = ws;
  ws.onopen = () => ws.send(JSON.stringify({ type: 'authenticate', session_token: state.credentials.session_token }));
  // Decryption is async: chain events so they are applied strictly in arrival order.
  ws.onmessage = (event) => {
    state.events = state.events.then(() => handle(ws, event.data)).catch(() => showError('chat-error', 'An event could not be processed. Reload the tab.'));
  };
  ws.onclose = (event) => {
    // A socket replaced by resync() is not a lost connection.
    if (state.ended || state.socket !== ws) return;
    if (event.code === 1008) { finish('Connection rejected. Open a new room'); return; }
    if (event.code === 1013) showError('chat-error', errors[event.reason] || errors.database_unavailable);
    scheduleReconnect();
  };
  ws.onerror = () => { /* onclose drives reconnection; never discard pending messages here. */ };
}
function updateTimer() {
  if (!state.room || state.ended) return;
  const remaining = Math.max(0, Math.ceil((Date.parse(state.room.expires_at) - Date.now()) / 1000));
  $('timer').textContent = `${String(Math.floor(remaining / 60)).padStart(2, '0')}:${String(remaining % 60).padStart(2, '0')}`;
  $('timer-bar').style.width = `${Math.min(100, remaining / state.totalSeconds * 100)}%`;
  $('timer-card').classList.toggle('ending', remaining <= 60);
  $('chat-head-timer').textContent = $('timer').textContent;
  $('chat-head-timer').classList.toggle('ending', remaining <= 60);
  // Announced once; a one-minute room does not need to be told at its start.
  if (remaining <= 60 && !state.timerWarned && state.totalSeconds > 90) {
    state.timerWarned = true;
    $('timer-alert').textContent = 'One minute left. The room will close and its history will be gone.';
  }
}

async function prepareCreate() {
  if (prepared.create) return prepared.create;
  const title = $('title').value.trim();
  if ([...title].length > 60) throw new Error('The title can be at most 60 characters.');
  const password = $('use-password').checked ? $('password').value : '';
  if ($('use-password').checked && !password) throw new Error('Enter a password or untick the box.');
  const secret = e2ee.randomSecret();
  const secrets = await e2ee.roomSecrets(secret, await e2ee.passwordKey(secret, password));
  const pair = await e2ee.generateKeyPair();
  const body = {
    encrypted_title: await e2ee.encryptTitle(secrets, title),
    ttl_minutes: Number($('ttl').value),
    key_share: await e2ee.signShare(secrets, 'creator', pair.publicKey),
    join_verifier: secrets.verifier,
  };
  prepared.create = { key: crypto.randomUUID(), body, secrets, pair, e2ee: { secret, has_password: Boolean(password), public_key: pair.publicKey, next_counter: 1 } };
  return prepared.create;
}
$('use-password').addEventListener('change', () => { $('password-row').hidden = !$('use-password').checked; prepared.create = null; });
for (const id of ['title', 'ttl', 'password']) $(id).addEventListener('input', () => { prepared.create = null; });
$('create-form').addEventListener('submit', async (event) => {
  event.preventDefault(); showError('home-error'); $('create-button').disabled = true;
  try {
    checkStorage();
    const request = await prepareCreate();
    const credentials = await api('/api/v1/rooms', request.body, '', request.key, { database_unavailable: 'The server is temporarily unavailable. The room was not created, please try again.' });
    prepared.create = null;
    $('password').value = '';
    await enter({ ...credentials, e2ee: request.e2ee }, { secrets: request.secrets, privateKey: request.pair.privateKey });
  } catch (e) { showError('home-error', e.message); }
  finally { $('create-button').disabled = false; }
});
async function prepareJoin() {
  if (prepared.join) return prepared.join;
  const params = new URLSearchParams(location.hash.slice(1));
  let secret;
  try { secret = params.get('k') || ''; if (e2ee.fromB64(secret).length !== 32) throw new Error(); }
  catch { throw new Error('The link is damaged: it has no encryption key. Ask for the full link again.'); }
  const needsPassword = params.get('pw') === '1';
  const password = needsPassword ? $('join-password')?.value || '' : '';
  if (needsPassword && !password) throw Object.assign(new Error('Enter the room password.'), { code: 'password_empty' });
  const secrets = await e2ee.roomSecrets(secret, await e2ee.passwordKey(secret, password));
  const pair = await e2ee.generateKeyPair();
  const body = { invite_token: params.get('invite'), join_proof: secrets.verifier, key_share: await e2ee.signShare(secrets, 'guest', pair.publicKey) };
  // The guest never stores the link secret: derived non-extractable keys are enough.
  prepared.join = { key: crypto.randomUUID(), body, secrets, pair, e2ee: { has_password: Boolean(password), public_key: pair.publicKey, next_counter: 1 } };
  return prepared.join;
}
const END_COPY = {
  end: 'End the conversation? The room and its history will be gone for both of you.',
  mismatch: "The codes don't match. The person on the other end may not be your contact. End the conversation for both?",
  broken: 'Key verification failed, so this conversation is not safe. End it for both?',
};
function openSheet() {
  const sheet = $('room-sheet');
  if (sheet.classList.contains('open')) return;
  sheet.classList.add('open');
  sheet.setAttribute('role', 'dialog');
  sheet.setAttribute('aria-modal', 'true');
  sheet.setAttribute('aria-label', 'Room details');
  $('sheet-backdrop').hidden = false;
  $('sheet-button').setAttribute('aria-expanded', 'true');
  document.querySelector('.chat-head').inert = true;
  document.querySelector('.conversation').inert = true;
  $('sheet-close').focus();
}
function closeSheet({ restoreFocus = true } = {}) {
  const sheet = $('room-sheet');
  if (!sheet.classList.contains('open')) return;
  sheet.classList.remove('open');
  sheet.removeAttribute('role');
  sheet.removeAttribute('aria-modal');
  sheet.removeAttribute('aria-label');
  $('sheet-backdrop').hidden = true;
  $('sheet-button').setAttribute('aria-expanded', 'false');
  document.querySelector('.chat-head').inert = false;
  document.querySelector('.conversation').inert = false;
  if (restoreFocus) $('sheet-button').focus();
}
$('sheet-button').addEventListener('click', () => openSheet());
$('sheet-close').addEventListener('click', () => closeSheet());
$('sheet-backdrop').addEventListener('click', () => closeSheet());
document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && !$('end-dialog').open) closeSheet(); });
MOBILE.addEventListener('change', () => { if (!MOBILE.matches) closeSheet({ restoreFocus: false }); syncViewport(); });
// iOS Safari keeps the layout viewport under the keyboard: follow the visual one while the chat is on screen.
let lastHeight = 0;
function syncViewport() {
  const chat = $('chat'), vv = globalThis.visualViewport;
  if (!vv || chat.hidden || !MOBILE.matches) { chat.style.removeProperty('--app-height'); chat.style.removeProperty('top'); return; }
  const box = $('messages'), changed = vv.height !== lastHeight;
  lastHeight = vv.height;
  chat.style.setProperty('--app-height', `${vv.height}px`);
  chat.style.top = `${vv.offsetTop}px`;
  // Android shrinks the layout before this event fires, so use the flag recorded earlier.
  if (changed && state.atBottom) box.scrollTop = box.scrollHeight;
}
if (globalThis.visualViewport) {
  visualViewport.addEventListener('resize', syncViewport);
  visualViewport.addEventListener('scroll', syncViewport);
}
$('message').addEventListener('focusin', () => { state.atBottom = nearBottom(); state.composerFocus = true; renderTrust(); });
$('message').addEventListener('focusout', () => { state.composerFocus = false; renderTrust(); });
let endReturnTo = null;
function openEndDialog(reason, returnTo) {
  endReturnTo = returnTo;
  $('end-dialog-text').textContent = END_COPY[reason] || END_COPY.end;
  $('end-confirm').disabled = false;
  if (!$('end-dialog').open) $('end-dialog').showModal();
}
$('end-button').addEventListener('click', () => openEndDialog('end', $('end-button')));
$('end-cancel').addEventListener('click', () => $('end-dialog').close());
$('end-dialog').addEventListener('close', () => (endReturnTo?.isConnected ? endReturnTo : $('trust-title')).focus());
$('end-confirm').addEventListener('click', async () => {
  $('end-confirm').disabled = true;
  $('end-button').disabled = true;
  try { await api('/api/v1/end', {}, state.credentials.session_token); finish('Conversation ended'); }
  catch (e) { $('end-button').disabled = false; $('end-dialog').close(); showError('chat-error', e.message); }
});
$('reconnect-button').addEventListener('click', () => {
  if (state.ended) return;
  state.retry = 0;
  showError('chat-error');
  connect();
});
$('messages').addEventListener('click', (event) => {
  const button = event.target.closest('[data-retry-id]');
  if (!button) return;
  const entry = state.failed.get(button.dataset.retryId);
  if (!entry) return;
  // Offline, keep the original ID: the next snapshot may show the message was stored.
  if (!state.connected) { showError('chat-error', 'No connection. Retry once it is back.'); return; }
  // A message still failed after a snapshot was not stored, so it is sealed again with
  // a fresh ID and counter: resending the old counter after newer messages would look
  // like a replay.
  state.failed.delete(entry.client_message_id);
  entry.client_message_id = crypto.randomUUID();
  entry.ciphertext = '';
  entry.status = 'pending'; entry.error = '';
  state.pending.set(entry.client_message_id, entry);
  showError('chat-error');
  renderMessages();
  enqueue(entry);
});
$('message-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const text = $('message').value.trim();
  if (!text || !state.connected || state.ended || !state.messageKey) return;
  if (new TextEncoder().encode(text).length > e2ee.MAX_TEXT_BYTES) { showError('chat-error', 'Message too long: 4000 bytes at most (about 2000 Cyrillic or 1000 emoji characters).'); return; }
  const entry = { client_message_id: crypto.randomUUID(), text, ciphertext: '', sender_id: state.self.id, status: 'pending' };
  state.pending.set(entry.client_message_id, entry);
  $('message').value = ''; $('message').style.height = 'auto'; updateComposer();
  showError('chat-error');
  renderMessages(); $('messages').scrollTop = $('messages').scrollHeight;
  enqueue(entry);
});
$('message').addEventListener('keydown', (event) => { if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); $('message-form').requestSubmit(); } });
// Send is offered only for something sendable; the byte budget shows up near its end.
function updateComposer() {
  const bytes = new TextEncoder().encode($('message').value.trim()).length;
  $('send-button').disabled = $('message').disabled || !bytes;
  $('byte-count').hidden = bytes < e2ee.MAX_TEXT_BYTES * 0.8;
  $('byte-count').textContent = bytes > e2ee.MAX_TEXT_BYTES ? `${bytes - e2ee.MAX_TEXT_BYTES} bytes over the limit` : `${e2ee.MAX_TEXT_BYTES - bytes} bytes left`;
  $('byte-count').classList.toggle('over', bytes > e2ee.MAX_TEXT_BYTES);
}
$('message').addEventListener('input', () => { $('message').style.height = 'auto'; $('message').style.height = `${Math.min($('message').scrollHeight, 120)}px`; updateComposer(); });
$('messages').addEventListener('scroll', () => { state.atBottom = nearBottom(); if (state.unseen && nearBottom()) { state.unseen = 0; showUnseen(); } }, { passive: true });
$('jump-button').addEventListener('click', () => {
  const box = $('messages');
  box.scrollTo({ top: box.scrollHeight, behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
  state.unseen = 0; showUnseen();
  // The button is gone now; hand focus to where the next action happens.
  if (!$('message').disabled) $('message').focus(); else box.focus();
});
document.addEventListener('visibilitychange', () => {
  if (document.hidden) return;
  clearNotification();
  if (state.unread) { state.unread = 0; updateTitle(); }
});
$('notify-button').addEventListener('click', async () => {
  if (notifyOn()) sessionStorage.setItem(NOTIFY_KEY, '0');
  else {
    // Asked only here, on a click: browsers ignore or penalise prompts nobody asked for.
    const permission = Notification.permission === 'granted' ? 'granted' : await Notification.requestPermission();
    if (permission === 'granted') sessionStorage.setItem(NOTIFY_KEY, '1');
  }
  renderNotify();
});
setInterval(updateTimer, 1000);

const INVITE_TERMINAL = {
  closed: {
    title: "This link can't be opened",
    lines: ["It may have been used already, turned off after wrong passwords, or the conversation is over. For privacy, nullchat doesn't say which.", "If you haven't opened this link before, tell the person who sent it, using a different app, and ask for a new one."],
    action: true,
  },
  damaged: { title: 'This link is incomplete', lines: ['Part of the link is missing. Ask for the full link again, and copy it in one piece.'], action: true },
  insecure: { title: 'Open this link over HTTPS', lines: ['Encryption only works on a secure connection. Open the link at its https:// address.'], action: false },
};
const INVITE_FACTS = ['Messages are encrypted in your browser. The server only sees ciphertext.', 'The room closes by itself when its time runs out.', 'No sign-up, no profile.'];
function inviteEl(tag, text, props = {}) {
  const el = document.createElement(tag);
  if (text) el.textContent = text;
  return Object.assign(el, props);
}
// Builds the guest screen for one state: 'ready', 'closed', 'damaged' or 'insecure'.
function renderInvite(inviteState) {
  const body = $('invite-body');
  body.replaceChildren();
  state.inviteShown = inviteState;
  const terminal = INVITE_TERMINAL[inviteState];
  if (terminal) {
    $('invite-title').textContent = terminal.title;
    for (const line of terminal.lines) body.append(inviteEl('p', line));
    if (terminal.action) body.append(inviteEl('a', 'Create your own room', { className: 'primary', href: '/' }));
    $('invite-title').focus();
    return;
  }
  $('invite-title').textContent = "You're invited to a private conversation";
  body.append(inviteEl('p', 'This link holds one seat and works once. When you join, the room opens for the two of you.'));
  const facts = inviteEl('ul', '', { className: 'invite-facts' });
  for (const fact of INVITE_FACTS) facts.append(inviteEl('li', fact));
  body.append(facts);
  const form = inviteEl('form', '', { id: 'join-form', noValidate: true });
  let input = null;
  if (params.get('pw') === '1') {
    form.append(inviteEl('label', 'Room password', { htmlFor: 'join-password' }));
    input = inviteEl('input', '', { id: 'join-password', type: 'password', autocomplete: 'off', maxLength: 200 });
    input.setAttribute('aria-describedby', 'join-password-hint');
    form.append(input);
    form.append(inviteEl('p', 'The person who invited you should send it separately from the link.', { id: 'join-password-hint', className: 'field-hint' }));
    const fieldError = inviteEl('p', '', { id: 'join-field-error', className: 'error', hidden: true });
    fieldError.setAttribute('role', 'alert');
    form.append(fieldError);
  }
  const submit = inviteEl('button', 'Join the conversation', { id: 'join-button', className: 'primary' });
  submit.type = 'submit';
  form.append(submit);
  const joinError = inviteEl('p', '', { id: 'join-error', className: 'error', hidden: true });
  joinError.setAttribute('role', 'alert');
  form.append(joinError);
  const status = inviteEl('p', '', { id: 'join-status', className: 'sr-only' });
  status.setAttribute('role', 'status');
  form.append(status);
  body.append(form);
  body.append(inviteEl('a', 'Or create your own room', { className: 'text-link', href: '/' }));
  form.addEventListener('submit', submitJoin);
  input?.addEventListener('input', () => {
    prepared.join = null;
    input.removeAttribute('aria-invalid');
    input.setAttribute('aria-describedby', 'join-password-hint');
    showError('join-field-error');
  });
}
// The error goes under the password field, or under the button when the link has none.
function showJoinFieldError(text) {
  const input = $('join-password');
  if (!input) { showError('join-error', text); return; }
  showError('join-field-error', text);
  input.setAttribute('aria-invalid', 'true');
  input.setAttribute('aria-describedby', 'join-password-hint join-field-error');
  input.focus();
}
async function submitJoin(event) {
  event.preventDefault();
  const button = $('join-button');
  if (button.disabled) return;
  button.disabled = true; button.textContent = 'Joining…';
  $('join-status').textContent = 'Joining the conversation…';
  showError('join-error');
  const field = $('join-password');
  if ($('join-field-error')) showError('join-field-error');
  if (field) {
    field.removeAttribute('aria-invalid');
    field.setAttribute('aria-describedby', 'join-password-hint');
    field.readOnly = true;
  }
  try {
    checkStorage();
    const request = await prepareJoin();
    const credentials = await api('/api/v1/join', request.body, '', request.key);
    await enter({ ...credentials, e2ee: request.e2ee }, { secrets: request.secrets, privateKey: request.pair.privateKey });
    prepared.join = null;
    if ($('join-password')) $('join-password').value = '';
  } catch (e) {
    const outcome = e.code === 'password_empty' ? 'field' : joinOutcome(e.code);
    if (outcome === 'closed') { renderInvite('closed'); return; }
    if (outcome === 'damaged') { renderInvite('damaged'); return; }
    button.disabled = false; button.textContent = 'Join the conversation';
    if (field) field.readOnly = false;
    $('join-status').textContent = '';
    if (outcome === 'field') showJoinFieldError(e.code === 'password_empty' ? 'Enter the room password.' : 'Wrong password, or the link is damaged. After 5 wrong attempts the link stops working.');
    else showError('join-error', e.message);
  }
}

const params = new URLSearchParams(location.hash.slice(1));
if (params.has('invite')) {
  $('home').hidden = true; $('invite').hidden = false;
  if (globalThis.crypto?.subtle) keystore.sweepExpired().catch(() => {});
  renderInvite(linkState({ invite: params.get('invite'), k: params.get('k'), secure: Boolean(globalThis.crypto?.subtle) }));
} else if (!globalThis.crypto?.subtle) {
  showError('home-error', 'Encryption works only over HTTPS or on localhost. Open the service at a secure address.');
  $('create-button').disabled = true;
} else {
  keystore.sweepExpired().catch(() => {});
  if (params.has('room')) {
    const restoreFailed = () => {
      $('home').hidden = false; $('chat').hidden = true;
      document.body.classList.remove('in-chat');
      syncViewport();
      history.replaceState(null, '', '/');
      showError('home-error', 'This tab\'s keys could not be restored. Create a new room.');
    };
    try {
      checkStorage();
      const saved = sessionStorage.getItem(`nullchat:${params.get('room')}`);
      if (saved) enter(JSON.parse(saved)).catch(restoreFailed);
      else { history.replaceState(null, '', '/'); showError('home-error', 'The room could not be opened. Use a valid invite or create a new room.'); }
    } catch { restoreFailed(); }
  }
}

// replaceState (enter, invite cleanup) fires no hashchange, so a link pasted into
// this tab is the only way here; the event carries the entry it replaced.
window.addEventListener('hashchange', (event) => {
  if (needsReload(new URL(event.oldURL).hash, new URL(event.newURL).hash)) location.reload();
});
