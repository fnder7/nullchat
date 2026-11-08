// Stores non-extractable CryptoKeys between reloads. IndexedDB is shared by the
// origin and outlives the tab, so records carry expires_ms and are swept.
const DB_NAME = 'nullchat';
const STORE = 'keys';

function openDb() {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1);
    request.onupgradeneeded = () => request.result.createObjectStore(STORE);
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

async function run(mode, action) {
  const db = await openDb();
  try {
    return await new Promise((resolve, reject) => {
      const tx = db.transaction(STORE, mode);
      const request = action(tx.objectStore(STORE));
      tx.oncomplete = () => resolve(request?.result);
      tx.onerror = tx.onabort = () => reject(tx.error);
    });
  } finally { db.close(); }
}

export const keyId = (roomId, participantId) => `${roomId}:${participantId}`;
export const putKeys = (id, record) => run('readwrite', (store) => store.put(record, id));
export const getKeys = (id) => run('readonly', (store) => store.get(id));
export const deleteKeys = (id) => run('readwrite', (store) => store.delete(id));

export function sweepExpired(now = Date.now()) {
  return run('readwrite', (store) => {
    const cursor = store.openCursor();
    cursor.onsuccess = () => {
      const c = cursor.result;
      if (!c) return;
      // Delete only what is known to be expired; malformed records are left alone.
      if (typeof c.value?.expires_ms === 'number' && c.value.expires_ms <= now) c.delete();
      c.continue();
    };
    return null;
  });
}
