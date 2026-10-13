import assert from "node:assert/strict";
import test from "node:test";
import { cloneCertificateHash, normalizeCertificateHash } from "../dist/certificate_hash.js";
import { redactURL, sanitizeCredentialText, sanitizeEndpointErrorText } from "../dist/credential_sanitization.js";
import { classifyAckInterval, isAllowedFallbackPair } from "../dist/realtime_policy.js";
import { GameClient, connectOptionsFromRealtimeConfig, connectPlanFromRealtimeConfig, fetchRealtimeConfig } from "../dist/index.js";

test("certificate normalization and cloning preserve view bounds and isolate both callers", () => {
  for (const [size, make] of [[32, b => b.buffer], [40, b => b.subarray(4, 36)], [40, b => new DataView(b.buffer, 4, 32)]]) {
    const backing = new Uint8Array(size).fill(9);
    const value = make(backing);
    const original = { algorithm: " SHA-256 ", value };
    const normalized = normalizeCertificateHash(original);
    const cloned = cloneCertificateHash(original);
    assert.equal(normalized.algorithm, "sha-256");
    assert.equal(cloned.algorithm, " SHA-256 ");
    assert.equal(cloned.value.byteLength, 32);
    backing.fill(7);
    assert.deepEqual([...new Uint8Array(normalized.value)], Array(32).fill(9));
    assert.deepEqual([...new Uint8Array(cloned.value)], Array(32).fill(9));
    new Uint8Array(normalized.value).fill(1);
    new Uint8Array(cloned.value).fill(2);
    assert.deepEqual([...backing], Array(backing.length).fill(7));
  }
  const value = "AB cd ".repeat(16);
  assert.equal(normalizeCertificateHash({algorithm:"Sha-256",value}).value.byteLength,32);
  assert.deepEqual(cloneCertificateHash({algorithm:"unchanged",value}),{algorithm:"unchanged",value});
  for (const [value, error] of [["a",/even length/],["gg",/non-hex/],["",/length 0/],["aa",/length 1/]]) {
    assert.throws(()=>normalizeCertificateHash({algorithm:"sha-256",value}),error);
  }
  assert.throws(()=>normalizeCertificateHash({algorithm:"sha-1",value}),/unsupported.*sha-1/);
});

test("credential policy shares base patterns while retaining endpoint-specific scrubbing", () => {
  assert.equal(redactURL("wss://user:pass@example.test/game?ticket=abc#frag"),"wss://example.test/game");
  assert.equal(redactURL("not a url"),"<invalid-url>");
  for (const name of ["ticket","token","access_token","authorization","auth","session","secret","api_key"]) {
    assert.equal(sanitizeCredentialText(`${name}=hidden`),`${name}=<redacted>`);
    assert.equal(sanitizeEndpointErrorText(`{"${name}":"hidden"}`,"invalid"),`{"${name}":"<redacted>"}`);
  }
  const text = "HTTP 403 https://user:pass@example.test/game?x=hidden &other=hidden token:hidden";
  assert.equal(sanitizeCredentialText(text),"HTTP 403 https://example.test/game &other=<redacted> token:<redacted>");
  const error = new Error("user pass a b a%20b a+b");
  assert.equal(sanitizeEndpointErrorText(error,"https://user:pass@example.test/?ticket=a+b"),"<redacted> <redacted> <redacted> <redacted> <redacted>");
});

const fetchConfig = body => fetchRealtimeConfig("https://example.test/config", {fetch:async()=>new Response(JSON.stringify(body))});
const endpoint = transport => ({transport,url: transport === "websocket" ? "wss://example.test" : "https://example.test"});
const client = () => new GameClient({entityManager:{applyUpdate(){},get(){}},decode:b=>b,encode:()=>new Uint8Array(),encodePacket:()=>new Uint8Array(),createChannel:()=>({onOpen(){},onClose(){},onMessage(){},close(){}})});

test("fallback direction is enforced independently at fetch, plan construction and runtime", async () => {
  for (const primary of ["webtransport","websocket"]) for (const fallback of ["webtransport","websocket"]) {
    const allowed = primary === "webtransport" && fallback === "websocket";
    const cfg = {...endpoint(primary),fallback:endpoint(fallback)};
    assert.equal(isAllowedFallbackPair(primary,fallback),allowed);
    const c=client();
    try {
      if (allowed) { await fetchConfig(cfg);connectPlanFromRealtimeConfig(cfg);c.connect({candidates:[endpoint(primary),endpoint(fallback)]}); }
      else {
        await assert.rejects(fetchConfig(cfg),/realtime config fallback/);
        assert.throws(()=>connectPlanFromRealtimeConfig(cfg),/realtime config fallback/);
        assert.throws(()=>c.connect({candidates:[endpoint(primary),endpoint(fallback)]}),/connection plan fallback/);
      }
    } finally { c.disconnect(); }
  }
  assert.equal(isAllowedFallbackPair(undefined,"websocket"),false);
});

test("ACK validation retains independent entrypoints and contextual errors", async () => {
  for (const value of [0,1,0x7fffffff,-1,0x80000000,0.5,NaN,Infinity,"1",null]) {
    const cfg={...endpoint("webtransport"),eventualAckIntervalMs:value};
    const classification=classifyAckInterval(value);
    const c=client();
    try {
      if (classification === undefined) {
        await fetchConfig(cfg);connectOptionsFromRealtimeConfig(cfg);connectPlanFromRealtimeConfig(cfg);c.connect({candidates:[cfg]});
      } else {
        // JSON cannot represent NaN/Infinity, and fetched null means omitted.
        if (value !== null && Number.isFinite(value) || typeof value === "string") await assert.rejects(fetchConfig(cfg),/eventualAckIntervalMs/);
        assert.throws(()=>connectOptionsFromRealtimeConfig(cfg),classification==="not-integer"?/finite integer/:/out of int32 range/);
        assert.throws(()=>connectPlanFromRealtimeConfig(cfg),/eventualAckIntervalMs/);
        assert.throws(()=>c.connect({candidates:[cfg]}),/candidate 0 eventualAckIntervalMs is invalid/);
      }
    } finally { c.disconnect(); }
  }
  assert.equal((await fetchConfig({...endpoint("webtransport"),eventualAckIntervalMs:null})).eventualAckIntervalMs,undefined);
  const value="AB ".repeat(32);
  const cfg=await fetchConfig({...endpoint("webtransport"),serverCertificateHashes:[{algorithm:" SHA-256 ",value}]});
  assert.deepEqual(cfg.serverCertificateHashes,[{algorithm:"sha-256",value}]);
});
