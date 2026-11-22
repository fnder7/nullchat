// Trust strip state, shown above the message list. Pure: no DOM, so the caller
// decides how each state looks.

// Returns 'hidden' | 'broken' | 'invite' | 'waiting' | 'verify' | 'verified' | 'later'.
export function trustState({ ended, keyError, alone, hasInvite, code, verified } = {}) {
  if (ended) return 'hidden';
  if (keyError) return 'broken';
  if (alone && hasInvite) return 'invite';
  if (alone) return 'waiting';
  if (!code) return 'hidden'; // the safety key is still being derived
  if (verified === 'match') return 'verified';
  if (verified === 'later') return 'later';
  return 'verify';
}

// Returns { label, tone } for the strip badge, or null when the state has none.
export function badge(state) {
  switch (state) {
    case 'verify':
    case 'later':
      return { label: 'Not verified', tone: 'warn' };
    case 'verified':
      return { label: '✓ Verified', tone: 'ok' };
    case 'broken':
      return { label: 'Key error', tone: 'danger' };
    default:
      return null;
  }
}

// The strip can collapse only after the user has answered the safety-code question.
export function collapsible(state) {
  return state === 'verified' || state === 'later';
}

export function inviteURL(origin, invite, secret, hasPassword) {
  return `${origin}/#invite=${invite}&k=${secret}${hasPassword ? '&pw=1' : ''}`;
}

// '1084 2945 3921' -> 'Safety code: 1 0 8 4, 2 9 4 5, 3 9 2 1'
export function codeLabel(code) {
  const groups = code.split(' ').map((group) => group.split('').join(' '));
  return `Safety code: ${groups.join(', ')}`;
}
