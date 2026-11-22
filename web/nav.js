// Same-tab navigation to a new invite or room link. Pure: both arguments are
// location.hash strings.

// True only when `next` carries an invite or room that differs from `current`'s.
export function needsReload(current, next) {
  const from = new URLSearchParams(String(current || '').replace(/^#/, ''));
  const to = new URLSearchParams(String(next || '').replace(/^#/, ''));
  return ['invite', 'room'].some((key) => to.has(key) && to.get(key) !== from.get(key));
}
