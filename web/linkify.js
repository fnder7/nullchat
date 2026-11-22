// Splits message text into plain and link segments. Pure: the caller builds DOM
// nodes from the result, so message text never passes through innerHTML.
const CANDIDATE = /(?:\bhttps?:\/\/|\bwww\.)[^\s<>"'`]+/gi;
const TRAILING = /[.,;:!?…'"»)\]}]$/;
const PAIRS = { ')': '(', ']': '[', '}': '{' };

const count = (text, ch) => text.split(ch).length - 1;

// Sentence punctuation after a link is not part of it; a closing bracket is kept
// only while it balances one inside the link, as in a Wikipedia URL.
function trimTrailing(url) {
  while (TRAILING.test(url)) {
    const last = url.at(-1);
    const open = PAIRS[last];
    if (open && count(url, open) >= count(url, last)) break;
    url = url.slice(0, -1);
  }
  return url;
}

function toHref(raw) {
  try {
    const url = new URL(/^www\./i.test(raw) ? `https://${raw}` : raw);
    if ((url.protocol !== 'http:' && url.protocol !== 'https:') || !url.hostname.includes('.')) return '';
    return url.href;
  } catch { return ''; }
}

export function linkify(text) {
  const segments = [];
  let last = 0;
  for (const match of text.matchAll(CANDIDATE)) {
    const raw = trimTrailing(match[0]);
    const href = toHref(raw);
    if (!href) continue;
    if (match.index > last) segments.push({ text: text.slice(last, match.index) });
    segments.push({ text: raw, href });
    last = match.index + raw.length;
  }
  if (last < text.length) segments.push({ text: text.slice(last) });
  return segments;
}
