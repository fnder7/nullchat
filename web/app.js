import * as e2ee from './crypto.js';
import * as keystore from './keystore.js';

const $ = (id) => document.getElementById(id);
const MAX_RECONNECT_ATTEMPTS = 8;
const DEFAULT_TITLE = 'Тихий разговор';
const state = {
  credentials: null, socket: null, room: null, self: null, invite: '',
  // e2ee: what survives reload in sessionStorage. Keys live in IndexedDB (keystore.js).
  e2ee: null, secrets: null, privateKey: null, messageKey: null, peers: '', keyError: false, title: '',
  plain: new Map(), flags: new Map(), events: Promise.resolve(), outbox: Promise.resolve(), lastAccepted: 0,
  pending: new Map(), failed: new Map(), accepted: new Map(),
  ended: false, connected: false, retry: 0, reconnectTimer: null, totalSeconds: 0,
};
// A create/join request is encrypted once and reused verbatim on retry, so the
// Idempotency-Key replays the same room with key shares this tab still holds.
const prepared = { create: null, join: null };
const errors = {
  room_unavailable: 'Приглашение уже использовано, комната закрыта или её время истекло.',
  session_invalid: 'Сессия больше недоступна. Возможно, истекло время комнаты или разговор уже завершён.',
  invalid_input: 'Проверь данные. Название — до 60 символов, сообщение — от 1 до 4000 байт.',
  capacity_reached: 'Временно достигнут лимит соединений или сообщений. Повтори позже.',
  room_full: 'В комнате уже два участника.',
  message_limit: 'В комнате достигнут лимит сообщений.',
  connection_limit: 'Достигнут лимит подключений. Повтори позже.',
  database_unavailable: 'Сервер временно недоступен. Сессия сохранена, повтори позже.',
  idempotency_key_conflict: 'Этот ключ повтора уже использован для другого запроса.',
  rate_limited: 'Слишком много запросов. Подожди немного и попробуй снова.',
  message_id_conflict: 'Идентификатор сообщения уже использован для другого текста.',
  origin_forbidden: 'Этот адрес не разрешён для подключения.',
  join_proof_invalid: 'Неверный пароль или повреждённая ссылка. После 5 неудачных попыток приглашение сгорит.',
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
  if (!response.ok) throw new Error(errorOverrides[result.error] || errors[result.error] || 'Не удалось выполнить запрос. Попробуй ещё раз.');
  return result;
}
function checkStorage() {
  try { sessionStorage.setItem('nullchat:check', '1'); sessionStorage.removeItem('nullchat:check'); }
  catch { throw new Error('Для временной сессии нужен доступ к хранилищу вкладки. Разреши его в настройках браузера.'); }
}
const keyId = () => keystore.keyId(state.room.id, state.self.id);
async function storeKeys(keys) {
  // A number, not the server string: an unparsable date must never look expired.
  const expires = Date.parse(state.room.expires_at) || Date.now() + 61 * 60 * 1000;
  try { await keystore.putKeys(keyId(), { expires_ms: expires, ...keys }); }
  catch { showError('chat-error', 'Браузер не дал сохранить ключи: после обновления вкладки сессия не восстановится.'); }
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
  if (!keys) throw new Error('В этой вкладке нет ключей комнаты.');
  if (fresh) await storeKeys(keys);
  state.secrets = { titleKey: keys.titleKey, macKey: keys.macKey, roomSalt: keys.roomSalt };
  state.privateKey = keys.privateKey;
  state.peers = state.e2ee.peers || '';
  try { state.title = (await e2ee.decryptTitle(state.secrets, state.room.encrypted_title)) || DEFAULT_TITLE; }
  catch { state.title = 'Название не расшифровано'; }
  state.totalSeconds = Math.max(1, (Date.parse(state.room.expires_at) - Date.now()) / 1000);
  save();
  history.replaceState(null, '', `/#room=${state.room.id}`);
  $('home').hidden = true;
  $('chat').hidden = false;
  $('room-title').textContent = state.title;
  document.title = `${state.title} · nullchat`;
  await establishKey();
  renderParticipants();
  renderMessages();
  updateTimer();
  connect();
}

function keyFailure() {
  state.keyError = true;
  state.messageKey = null;
  $('safety-panel').classList.add('broken');
  $('safety-code').textContent = 'ключ не прошёл проверку';
  $('safety-note').textContent = 'Ключ собеседника подменён или ссылка повреждена. Не продолжай разговор: заверши чат и создай новую комнату.';
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
    $('safety-code').textContent = 'ждём собеседника';
    return;
  }
  // Pinned in sessionStorage, so a key swapped across a reload is caught too.
  const peers = `${creator.key_share.public_key}.${guest.key_share.public_key}`;
  if (state.peers && state.peers !== peers) return keyFailure();
  if (!state.messageKey) {
    state.messageKey = await e2ee.messageKey(state.secrets, state.privateKey, creator.key_share.public_key, guest.key_share.public_key, own.role);
    state.plain.clear();
    $('safety-code').textContent = await e2ee.safetyCode(creator.key_share.public_key, guest.key_share.public_key);
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
    if (p.id === state.self.id) { const tag = document.createElement('small'); tag.textContent = 'это ты'; li.append(tag); }
    $('participants').append(li);
  }
  $('participant-count').textContent = `${state.room.participants.length} / 2`;
  $('share-panel').hidden = !state.invite || !state.e2ee?.secret || state.room.participants.length > 1 || state.ended;
  if (state.invite && state.e2ee?.secret) $('invite-link').value = `${location.origin}/#invite=${state.invite}&k=${state.e2ee.secret}${state.e2ee.has_password ? '&pw=1' : ''}`;
}
function addMessage(message, pending = false) {
  const plain = message.id ? state.plain.get(message.id) : { text: message.text };
  const flag = message.id ? state.flags.get(message.id) : null;
  if (flag?.replay) return; // replayed or reordered by the server; renderMessages explains
  const self = message.sender_id === state.self.id;
  const status = pending === true ? 'pending' : pending || 'accepted';
  const article = document.createElement('article'); article.className = `message${self ? ' self' : ''} ${status}${plain?.error ? ' undecryptable' : ''}`;
  const meta = document.createElement('div'); meta.className = 'message-meta';
  const name = document.createElement('span'); name.textContent = self ? 'Ты' : (state.room.participants.find((p) => p.id === message.sender_id)?.name || 'Собеседник');
  const time = document.createElement('span'); time.textContent = status === 'pending' ? 'Отправляется…' : status === 'failed' ? 'Не отправлено' : new Date(message.created_at).toLocaleTimeString('ru', { hour: '2-digit', minute: '2-digit' });
  meta.append(name, time);
  const bubble = document.createElement('div'); bubble.className = 'bubble';
  bubble.textContent = plain?.error ? '⚠ Не удалось расшифровать сообщение' : (plain?.text ?? '…');
  article.append(meta, bubble);
  if (flag?.missing) {
    const note = document.createElement('p'); note.className = 'message-note';
    note.textContent = 'Перед этим сообщением пропало одно, которое сервер уже принимал. Сервер мог его удалить.';
    article.append(note);
  }
  if (status === 'failed') {
    const footer = document.createElement('div'); footer.className = 'message-actions';
    const reason = document.createElement('span'); reason.textContent = message.error || 'Временная ошибка';
    const retry = document.createElement('button'); retry.type = 'button'; retry.className = 'retry-message'; retry.dataset.retryId = message.client_message_id; retry.textContent = 'Повторить';
    footer.append(reason, retry); article.append(footer);
  }
  $('messages').append(article);
}
function renderMessages() {
  const box = $('messages');
  const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 100;
  const oldTop = box.scrollTop;
  box.replaceChildren();
  if (!state.room.messages.length && !state.pending.size && !state.failed.size) {
    const alone = state.room.participants.length === 1;
    const empty = document.createElement('div'); empty.className = 'empty-state';
    const symbol = document.createElement('span'); symbol.className = 'empty-symbol'; symbol.textContent = '[ … ]';
    const title = document.createElement('strong'); title.textContent = alone ? 'Разговор ещё впереди' : 'Вы оба здесь';
    const p = document.createElement('p'); p.textContent = alone ? 'Поделись приглашением. Писать можно после входа собеседника — тогда появится общий ключ.' : 'Самое время сказать «привет».';
    empty.append(symbol, title, p); box.append(empty);
  }
  state.room.messages.forEach((m) => addMessage(m));
  state.pending.forEach((m) => addMessage(m, true));
  state.failed.forEach((m) => addMessage(m, 'failed'));
  if ([...state.flags.values()].some((f) => f.replay)) showError('chat-error', 'Сервер повторил или переставил сообщение. Оно скрыто.');
  $('message-count').textContent = `${state.room.messages.length} / 500`;
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
async function receiveMessage(message) {
  if (!state.room.messages.some((m) => m.id === message.id)) state.room.messages.push(message);
  state.room.messages.sort((a, b) => a.sequence - b.sequence);
  await decryptAll();
  if (message.sender_id === state.self.id) {
    state.accepted.set(message.client_message_id, message);
    state.pending.delete(message.client_message_id);
    state.failed.delete(message.client_message_id);
  }
  renderMessages();
}
function connection(connected, label) {
  state.connected = connected;
  $('connection-status').textContent = label;
  $('connection-dot').style.background = connected ? 'var(--accent)' : 'var(--muted)';
  const canSend = connected && !state.ended && Boolean(state.messageKey) && !state.keyError;
  $('message').disabled = !canSend;
  $('send-button').disabled = !canSend;
  $('message').placeholder = state.messageKey ? 'Что у тебя на уме?' : 'Ждём собеседника — после его входа появится общий ключ';
  $('reconnect-button').hidden = connected || state.ended || state.retry < MAX_RECONNECT_ATTEMPTS;
}
function finish(label) {
  state.ended = true;
  clearTimeout(state.reconnectTimer);
  connection(false, label);
  state.socket?.close(1000);
  sessionStorage.removeItem(`nullchat:${state.room.id}`);
  keystore.deleteKeys(keyId()).catch(() => {});
  state.secrets = null; state.privateKey = null; state.messageKey = null;
  state.invite = '';
  state.pending.clear();
  state.failed.clear();
  state.accepted.clear();
  state.plain.clear();
  state.room.messages = [];
  $('messages').replaceChildren();
  $('share-panel').hidden = true;
  $('end-button').hidden = true;
  $('message-form').hidden = true;
  document.querySelector('.composer-hint').hidden = true;
  $('ended-title').textContent = label;
  $('ended-panel').hidden = false;
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
    markFailed(entry, 'Соединение недоступно. Повтори после восстановления.');
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
    markFailed(entry, 'Не удалось передать сообщение. Нажми «Повторить».');
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
    else showError('chat-error', 'Сообщение не отправлено. Нажми «Повторить».');
  }).catch(() => markFailed(entry, 'Не удалось зашифровать сообщение. Нажми «Повторить».'));
}
function scheduleReconnect() {
  if (state.ended || state.reconnectTimer || state.retry >= MAX_RECONNECT_ATTEMPTS) {
    if (!state.ended && state.retry >= MAX_RECONNECT_ATTEMPTS) {
      connection(false, 'Сервер временно недоступен');
      showError('chat-error', 'Сервер не отвечает. Сессия сохранена — можно повторить подключение.');
    }
    return;
  }
  const base = Math.min(1000 * 2 ** state.retry, 10000);
  const delay = Math.min(10000, Math.round(base + Math.random() * Math.min(base / 2, 1000)));
  state.retry++;
  connection(false, 'Соединение прервано. Переподключаемся…');
  state.reconnectTimer = setTimeout(() => { state.reconnectTimer = null; connect(); }, delay);
}
async function handle(ws, data) {
  if (state.ended || state.socket !== ws) return;
  const { type, payload } = JSON.parse(data);
  if (type === 'snapshot') {
    state.room = payload.room; state.self = payload.self; state.retry = 0;
    await establishKey();
    await decryptAll();
    for (const m of state.room.messages) {
      if (m.sender_id !== state.self.id) continue;
      state.accepted.set(m.client_message_id, m);
      state.pending.delete(m.client_message_id);
      state.failed.delete(m.client_message_id);
    }
    connection(true, 'Соединение установлено');
    showError('chat-error'); renderParticipants(); renderMessages(); save();
    // Unacknowledged messages go first, in counter order, through the same queue.
    [...state.pending.values()].sort((a, b) => a.counter - b.counter).forEach((m) => enqueue(m, ws));
  } else if (type === 'message' || type === 'ack') await receiveMessage(payload);
  else if (type === 'participants') {
    state.room.participants = payload;
    await establishKey();
    await decryptAll();
    connection(state.connected, $('connection-status').textContent);
    renderParticipants(); renderMessages(); save();
  } else if (type === 'invite_revoked') {
    state.invite = ''; delete state.e2ee.secret; save(); renderParticipants();
    showError('chat-error', 'Приглашение аннулировано после 5 неверных попыток пароля. Заверши чат и создай новую комнату.');
  }
  else if (type === 'room_expired') finish('Время разговора истекло');
  else if (type === 'room_closed') finish('Разговор завершён');
  else if (type === 'server_shutdown') { connection(false, 'Сервер перезапускается…'); showError('chat-error', 'Сервер временно перезапускается. Сессия сохранена.'); }
  else if (type === 'error') {
    if (payload.code === 'session_invalid') { finish('Комната больше недоступна'); return; }
    const failed = state.pending.get(payload.client_message_id);
    if (failed) markFailed(failed, errors[payload.code] || 'Не удалось отправить сообщение. Нажми «Повторить».');
    showError('chat-error', errors[payload.code] || 'Не удалось отправить сообщение.');
  }
}
function connect() {
  if (state.ended) return;
  clearTimeout(state.reconnectTimer); state.reconnectTimer = null;
  connection(false, state.retry ? 'Восстанавливаем соединение…' : 'Подключаемся…');
  const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/ws`);
  state.socket = ws;
  ws.onopen = () => ws.send(JSON.stringify({ type: 'authenticate', session_token: state.credentials.session_token }));
  // Decryption is async: chain events so they are applied strictly in arrival order.
  ws.onmessage = (event) => {
    state.events = state.events.then(() => handle(ws, event.data)).catch(() => showError('chat-error', 'Не удалось обработать событие. Обнови вкладку.'));
  };
  ws.onclose = (event) => {
    if (state.ended) return;
    if (event.code === 1008) { finish('Подключение отклонено. Открой новую комнату'); return; }
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
}

async function prepareCreate() {
  if (prepared.create) return prepared.create;
  const title = $('title').value.trim();
  if ([...title].length > 60) throw new Error('Название — не больше 60 символов.');
  const password = $('use-password').checked ? $('password').value : '';
  if ($('use-password').checked && !password) throw new Error('Введи пароль или сними флажок.');
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
    const credentials = await api('/api/v1/rooms', request.body, '', request.key, { database_unavailable: 'Сервер временно недоступен. Комната не создана — попробуй ещё раз.' });
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
  catch { throw new Error('Ссылка повреждена: в ней нет ключа шифрования. Попроси отправить её заново целиком.'); }
  const needsPassword = params.get('pw') === '1';
  const password = needsPassword ? $('join-password').value : '';
  if (needsPassword && !password) throw new Error('Введи пароль комнаты.');
  const secrets = await e2ee.roomSecrets(secret, await e2ee.passwordKey(secret, password));
  const pair = await e2ee.generateKeyPair();
  const body = { invite_token: params.get('invite'), join_proof: secrets.verifier, key_share: await e2ee.signShare(secrets, 'guest', pair.publicKey) };
  // The guest never stores the link secret: derived non-extractable keys are enough.
  prepared.join = { key: crypto.randomUUID(), body, secrets, pair, e2ee: { has_password: Boolean(password), public_key: pair.publicKey, next_counter: 1 } };
  return prepared.join;
}
$('join-password').addEventListener('input', () => { prepared.join = null; });
$('join-button').addEventListener('click', async () => {
  $('join-button').disabled = true; showError('home-error');
  try {
    checkStorage();
    const request = await prepareJoin();
    const credentials = await api('/api/v1/join', request.body, '', request.key, { invalid_input: 'Приглашение недействительно. Проверь ссылку или попроси новую.' });
    prepared.join = null;
    $('join-password').value = '';
    await enter({ ...credentials, e2ee: request.e2ee }, { secrets: request.secrets, privateKey: request.pair.privateKey });
  } catch (e) { showError('home-error', e.message); }
  finally { $('join-button').disabled = false; }
});
$('copy-button').addEventListener('click', async () => {
  try { await navigator.clipboard.writeText($('invite-link').value); $('copy-button').textContent = 'Ссылка скопирована ✓'; }
  catch { $('invite-link').focus(); $('invite-link').select(); $('copy-button').textContent = 'Скопируй выделенную ссылку'; }
  setTimeout(() => { $('copy-button').textContent = 'Скопировать ссылку ↗'; }, 3000);
});
$('end-button').addEventListener('click', async () => {
  if (!confirm('Завершить разговор? Комната и история станут недоступны для вас обоих.')) return;
  $('end-button').disabled = true;
  try { await api('/api/v1/end', {}, state.credentials.session_token); finish('Разговор завершён'); }
  catch (e) { showError('chat-error', e.message); $('end-button').disabled = false; }
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
  // A failed message was never stored, so it is sealed again with a fresh ID and
  // counter: resending the old counter after newer messages would look like a replay.
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
  if (new TextEncoder().encode(text).length > e2ee.MAX_TEXT_BYTES) { showError('chat-error', 'Сообщение слишком длинное: максимум 4000 байт (около 2000 русских букв).'); return; }
  const entry = { client_message_id: crypto.randomUUID(), text, ciphertext: '', sender_id: state.self.id, status: 'pending' };
  state.pending.set(entry.client_message_id, entry);
  $('message').value = ''; $('message').style.height = 'auto';
  showError('chat-error');
  renderMessages(); $('messages').scrollTop = $('messages').scrollHeight;
  enqueue(entry);
});
$('message').addEventListener('keydown', (event) => { if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); $('message-form').requestSubmit(); } });
$('message').addEventListener('input', () => { $('message').style.height = 'auto'; $('message').style.height = `${Math.min($('message').scrollHeight, 120)}px`; });
setInterval(updateTimer, 1000);

const params = new URLSearchParams(location.hash.slice(1));
if (!globalThis.crypto?.subtle) {
  showError('home-error', 'Шифрование работает только по HTTPS или на localhost. Открой сервис по защищённому адресу.');
  $('create-button').disabled = true; $('join-button').disabled = true;
} else {
  keystore.sweepExpired().catch(() => {});
  if (params.has('invite')) {
    $('create-form').hidden = true; $('invite-panel').hidden = false; $('card-title').textContent = 'Тебя ждут';
    $('join-password-row').hidden = params.get('pw') !== '1';
    document.querySelector('.create-card .muted').textContent = 'Один разговор. Одно приглашение для тебя.';
  } else if (params.has('room')) {
    const restoreFailed = () => {
      $('home').hidden = false; $('chat').hidden = true;
      history.replaceState(null, '', '/');
      showError('home-error', 'Не удалось восстановить ключи этой вкладки. Создай новую комнату.');
    };
    try {
      checkStorage();
      const saved = sessionStorage.getItem(`nullchat:${params.get('room')}`);
      if (saved) enter(JSON.parse(saved)).catch(restoreFailed);
      else { history.replaceState(null, '', '/'); showError('home-error', 'Не удалось открыть комнату. Используй действующее приглашение или создай новую.'); }
    } catch { restoreFailed(); }
  }
}
