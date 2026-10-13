import assert from 'node:assert/strict';
import test from 'node:test';
import { combineUnsubscribers } from 'golem-engine';
import { combineUnsubscribers as adapterCombine } from '../dist/core.js';
test('adapter retains the shared unsubscribe entrypoint', () => {
  assert.equal(adapterCombine, combineUnsubscribers);
});
