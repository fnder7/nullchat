import test from 'node:test';
import assert from 'node:assert/strict';
import { linkify } from './linkify.js';

const links = (text) => linkify(text).filter((s) => s.href);

test('plain text stays one segment', () => {
  assert.deepEqual(linkify('just talk'), [{ text: 'just talk' }]);
  assert.deepEqual(linkify(''), []);
});

test('finds http, https and www links inside text', () => {
  assert.deepEqual(linkify('see https://example.com/a?b=1#c now'), [
    { text: 'see ' },
    { text: 'https://example.com/a?b=1#c', href: 'https://example.com/a?b=1#c' },
    { text: ' now' },
  ]);
  assert.deepEqual(links('www.example.org'), [{ text: 'www.example.org', href: 'https://www.example.org/' }]);
  assert.equal(links('http://a.io and https://b.io').length, 2);
});

test('segments rebuild the original text exactly', () => {
  const text = 'a https://x.io/(y), b www.z.dev. c';
  assert.equal(linkify(text).map((s) => s.text).join(''), text);
});

test('trailing punctuation is left out of the link', () => {
  assert.equal(links('go to https://example.com.')[0].text, 'https://example.com');
  assert.equal(links('(https://example.com)')[0].text, 'https://example.com');
  assert.equal(links('really? https://example.com/x?!')[0].text, 'https://example.com/x');
});

test('balanced brackets stay in the link', () => {
  assert.equal(links('https://en.wikipedia.org/wiki/Tor_(network)')[0].text, 'https://en.wikipedia.org/wiki/Tor_(network)');
});

test('other schemes and look-alikes are never links', () => {
  for (const text of ['javascript:alert(1)', 'data:text/html,hi', 'file:///etc/passwd', 'mailto:a@b.c', 'xhttps://evil.io', 'https://localhost', 'https://']) {
    assert.deepEqual(links(text), [], text);
  }
});

test('markup is not a link boundary trick', () => {
  assert.equal(links('https://a.io/"onmouseover="x')[0].text, 'https://a.io/');
  assert.equal(links('<https://a.io>')[0].text, 'https://a.io');
});
