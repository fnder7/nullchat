import test from 'node:test';
import assert from 'node:assert/strict';
import { needsReload } from './nav.js';

test('same room is no reload', () => {
  assert.equal(needsReload('#room=a', '#room=a'), false);
});

test('empty fragment is no reload', () => {
  assert.equal(needsReload('#room=a', ''), false);
  assert.equal(needsReload('#room=a', '#'), false);
});

test('unrelated fragment is no reload', () => {
  assert.equal(needsReload('#room=a', '#top'), false);
});

test('different room reloads', () => {
  assert.equal(needsReload('#room=a', '#room=b'), true);
});

test('invite after room reloads', () => {
  assert.equal(needsReload('#room=a', '#invite=x&k=y'), true);
});

test('same invite is no reload', () => {
  assert.equal(needsReload('#invite=x&k=y', '#invite=x&k=y'), false);
});
