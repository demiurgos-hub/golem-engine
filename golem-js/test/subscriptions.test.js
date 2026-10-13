import assert from "node:assert/strict";
import test from "node:test";
import { combineUnsubscribers } from "../dist/index.js";

test("unsubscribe composition is ordered, idempotent and reentrant", () => {
  const calls=[];
  const stop=combineUnsubscribers(()=>{calls.push(1);stop();},()=>calls.push(2));
  stop();stop();assert.deepEqual(calls,[1,2]);
  const empty=combineUnsubscribers();empty();empty();
});

test("a throwing cleanup stops this pass and permanently deactivates skipped callbacks", () => {
  const failure=new Error("cleanup");const calls=[];
  const stop=combineUnsubscribers(()=>calls.push(1),()=>{calls.push(2);throw failure;},()=>calls.push(3));
  assert.throws(stop,error=>error===failure);assert.doesNotThrow(stop);assert.deepEqual(calls,[1,2]);
});
