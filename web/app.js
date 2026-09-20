'use strict';

const $ = (id) => document.getElementById(id);
const MAX_RECONNECT_ATTEMPTS = 8;
const state = { credentials: null, socket: null, room: null, self: null, invite: '', pending: new Map(), failed: new Map(), accepted: new Map(), ended: false, connected: false, retry: 0, reconnectTimer: null, totalSeconds: 0 };
const errors = {
  room_unavailable: 'Приглашение уже использовано, комната закрыта или её время истекло.',
  session_invalid: 'Сессия больше недоступна. Возможно, истекло время комнаты или разговор уже завершён.',
  invalid_input: 'Проверь данные. Сообщение должно содержать от 1 до 4000 байт текста.',
  capacity_reached: 'Временно достигнут лимит соединений или сообщений. Повтори позже.',
  room_full: 'В комнате уже два участника.',
  message_limit: 'В комнате достигнут лимит сообщений.',
  connection_limit: 'Достигнут лимит подключений. Повтори позже.',
  database_unavailable: 'Сервер временно недоступен. Сессия сохранена, повтори позже.',
  idempotency_key_conflict: 'Этот ключ повтора уже использован для другого запроса.',
  rate_limited: 'Слишком много запросов. Подожди немного и попробуй снова.',
  message_id_conflict: 'Идентификатор сообщения уже использован для другого текста.',
  origin_forbidden: 'Этот адрес не разрешён для подключения.',
};

function showError(id, text = '') { $(id).textContent = text; $(id).hidden = !text; }
function save() {
  if (!state.credentials) return;
  sessionStorage.setItem(`nullchat:${state.room.id}`, JSON.stringify({ ...state.credentials, room: { ...state.room, messages: [] }, invite_token: state.invite }));
}
async function api(path, data, token) {
  const response = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) }, body: JSON.stringify(data), signal: AbortSignal.timeout(10000) });
  const result = await response.json();
  if (!response.ok) throw new Error(errors[result.error] || 'Не удалось выполнить запрос. Попробуй ещё раз.');
  return result;
}
function checkStorage() {
  try { sessionStorage.setItem('nullchat:check', '1'); sessionStorage.removeItem('nullchat:check'); }
  catch { throw new Error('Для временной сессии нужен доступ к хранилищу вкладки. Разреши его в настройках браузера.'); }
}
function enter(credentials) {
  state.credentials = credentials;
  state.room = credentials.room;
  state.self = credentials.self;
  state.invite = credentials.invite_token || '';
  state.totalSeconds = Math.max(1, (Date.parse(state.room.expires_at) - Date.now()) / 1000);
  save();
  history.replaceState(null, '', `/#room=${state.room.id}`);
  $('home').hidden = true;
  $('chat').hidden = false;
  $('room-title').textContent = state.room.title;
  document.title = `${state.room.title} · nullchat`;
  renderParticipants();
  renderMessages();
  updateTimer();
  connect();
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
  $('share-panel').hidden = !state.invite || state.room.participants.length > 1 || state.ended;
  if (state.invite) $('invite-link').value = `${location.origin}/#invite=${state.invite}`;
}
function addMessage(message, pending = false) {
  const self = message.sender_id === state.self.id;
  const status = pending === true ? 'pending' : pending || 'accepted';
  const article = document.createElement('article'); article.className = `message${self ? ' self' : ''} ${status}`;
  const meta = document.createElement('div'); meta.className = 'message-meta';
  const name = document.createElement('span'); name.textContent = self ? 'Ты' : (state.room.participants.find((p) => p.id === message.sender_id)?.name || 'Собеседник');
  const time = document.createElement('span'); time.textContent = status === 'pending' ? 'Отправляется…' : status === 'failed' ? 'Не отправлено' : new Date(message.created_at).toLocaleTimeString('ru', { hour: '2-digit', minute: '2-digit' });
  meta.append(name, time);
  const bubble = document.createElement('div'); bubble.className = 'bubble'; bubble.textContent = message.text;
  article.append(meta, bubble);
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
  if (!state.room.messages.length && !state.pending.size) {
    const empty = document.createElement('div'); empty.className = 'empty-state';
    const symbol = document.createElement('span'); symbol.className = 'empty-symbol'; symbol.textContent = '[ … ]';
    const title = document.createElement('strong'); title.textContent = state.room.participants.length === 1 ? 'Разговор ещё впереди' : 'Вы оба здесь';
    const p = document.createElement('p'); p.textContent = state.room.participants.length === 1 ? 'Поделись приглашением. Или оставь первое сообщение.' : 'Самое время сказать «привет».';
    empty.append(symbol, title, p); box.append(empty);
  }
  state.room.messages.forEach((m) => addMessage(m));
  state.pending.forEach((m) => addMessage(m, true));
  state.failed.forEach((m) => addMessage(m, 'failed'));
  $('message-count').textContent = `${state.room.messages.length} / 500`;
  box.scrollTop = atBottom ? box.scrollHeight : oldTop;
}
function receiveMessage(message) {
  if (!state.room.messages.some((m) => m.id === message.id)) state.room.messages.push(message);
  state.room.messages.sort((a, b) => a.sequence - b.sequence);
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
  $('message').disabled = !connected || state.ended;
  $('send-button').disabled = !connected || state.ended;
  $('reconnect-button').hidden = connected || state.ended || state.retry < MAX_RECONNECT_ATTEMPTS;
}
function finish(label) {
  state.ended = true;
  clearTimeout(state.reconnectTimer);
  connection(false, label);
  state.socket?.close(1000);
  sessionStorage.removeItem(`nullchat:${state.room.id}`);
  state.invite = '';
  state.pending.clear();
  state.failed.clear();
  state.accepted.clear();
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
    socket.send(JSON.stringify({ type: 'send_message', client_message_id: entry.client_message_id, text: entry.text }));
    return true;
  } catch {
    markFailed(entry, 'Не удалось передать сообщение. Нажми «Повторить».');
    return false;
  }
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
function connect() {
  if (state.ended) return;
  clearTimeout(state.reconnectTimer); state.reconnectTimer = null;
  connection(false, state.retry ? 'Восстанавливаем соединение…' : 'Подключаемся…');
  const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/ws`);
  state.socket = ws;
  ws.onopen = () => ws.send(JSON.stringify({ type: 'authenticate', session_token: state.credentials.session_token }));
  ws.onmessage = (event) => {
    if (state.ended) return;
    const { type, payload } = JSON.parse(event.data);
    if (type === 'snapshot') {
      state.room = payload.room; state.self = payload.self; state.retry = 0;
      for (const m of state.room.messages) {
        if (m.sender_id !== state.self.id) continue;
        state.accepted.set(m.client_message_id, m);
        state.pending.delete(m.client_message_id);
        state.failed.delete(m.client_message_id);
      }
      connection(true, 'Соединение установлено');
      showError('chat-error'); renderParticipants(); renderMessages(); save();
      let delay = 0;
      for (const m of state.pending.values()) {
        setTimeout(() => { if (state.socket === ws && state.connected && state.pending.has(m.client_message_id)) transmit(m, ws); }, delay);
        delay += 250;
      }
    } else if (type === 'message' || type === 'ack') receiveMessage(payload);
    else if (type === 'participants') { state.room.participants = payload; renderParticipants(); renderMessages(); save(); }
    else if (type === 'room_expired') finish('Время разговора истекло');
    else if (type === 'room_closed') finish('Разговор завершён');
    else if (type === 'server_shutdown') { connection(false, 'Сервер перезапускается…'); showError('chat-error', 'Сервер временно перезапускается. Сессия сохранена.'); }
    else if (type === 'error') {
      if (payload.code === 'session_invalid') { finish('Комната больше недоступна'); return; }
      const failed = state.pending.get(payload.client_message_id);
      if (failed) markFailed(failed, errors[payload.code] || 'Не удалось отправить сообщение. Нажми «Повторить».');
      showError('chat-error', errors[payload.code] || 'Не удалось отправить сообщение.');
    }
  };
  ws.onclose = (event) => {
    if (state.ended) return;
    if (event.code === 1008) { finish('Подключение отклонено. Открой новую комнату'); return; }
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

$('create-form').addEventListener('submit', async (event) => {
  event.preventDefault(); showError('home-error'); $('create-button').disabled = true;
  try { checkStorage(); enter(await api('/api/v1/rooms', { title: $('title').value, ttl_minutes: Number($('ttl').value) })); }
  catch (e) { showError('home-error', e.message); }
  finally { $('create-button').disabled = false; }
});
$('join-button').addEventListener('click', async () => {
  $('join-button').disabled = true; showError('home-error');
  try { checkStorage(); enter(await api('/api/v1/join', { invite_token: new URLSearchParams(location.hash.slice(1)).get('invite') })); }
  catch (e) { showError('home-error', e.message); }
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
  transmit(entry);
  renderMessages();
});
$('message-form').addEventListener('submit', (event) => {
  event.preventDefault();
  const text = $('message').value.trim();
  if (!text || !state.connected || state.ended) return;
  if (new TextEncoder().encode(text).length > 4000) { showError('chat-error', 'Сообщение слишком длинное: максимум 4000 байт (около 2000 русских букв).'); return; }
  const id = crypto.randomUUID();
  const entry = { client_message_id: id, text, sender_id: state.self.id, status: 'pending' };
  state.pending.set(id, entry);
  const sent = transmit(entry);
  $('message').value = ''; $('message').style.height = 'auto';
  if (sent) showError('chat-error');
  else showError('chat-error', 'Сообщение не отправлено. Нажми «Повторить».');
  renderMessages(); $('messages').scrollTop = $('messages').scrollHeight;
});
$('message').addEventListener('keydown', (event) => { if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); $('message-form').requestSubmit(); } });
$('message').addEventListener('input', () => { $('message').style.height = 'auto'; $('message').style.height = `${Math.min($('message').scrollHeight, 120)}px`; });
setInterval(updateTimer, 1000);

const params = new URLSearchParams(location.hash.slice(1));
if (params.has('invite')) {
  $('create-form').hidden = true; $('invite-panel').hidden = false; $('card-title').textContent = 'Тебя ждут';
  document.querySelector('.create-card .muted').textContent = 'Один разговор. Одно приглашение для тебя.';
} else if (params.has('room')) {
  try {
    checkStorage();
    const saved = sessionStorage.getItem(`nullchat:${params.get('room')}`);
    if (saved) enter(JSON.parse(saved));
    else { history.replaceState(null, '', '/'); showError('home-error', 'В этой вкладке нет сессии комнаты. Попроси новое приглашение или создай свою комнату.'); }
  } catch { showError('home-error', 'Не удалось восстановить сессию. Создай новую комнату.'); }
}
