// Guest screen state. Pure: no DOM, no crypto import, so the caller decides how
// each state looks.

// The key is 32 bytes as unpadded base64url, which is exactly 43 characters.
const KEY_FORMAT = /^[A-Za-z0-9_-]{43}$/;

// Returns 'insecure' | 'damaged' | 'ready' for the link the guest opened.
export function linkState({ invite, k, secure } = {}) {
  if (!secure) return 'insecure';
  if (typeof invite !== 'string' || invite.length !== 64) return 'damaged';
  if (typeof k !== 'string' || !KEY_FORMAT.test(k)) return 'damaged';
  return 'ready';
}

// Maps a join response code to the screen that handles it.
export function joinOutcome(code) {
  switch (code) {
    case 'room_unavailable':
      return 'closed';
    case 'invalid_input':
      return 'damaged';
    case 'join_proof_invalid':
      return 'field';
    default:
      return 'transient';
  }
}
