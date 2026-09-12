import assert from "node:assert/strict";
import { after, test } from "node:test";

import {
  getKnowledgeCurrent,
  getKnowledgeHistory,
  getMemoryCurrent,
  getMemoryHistory,
  memoryWrite,
} from "./api/client.ts";
import { demo } from "./demo/data.ts";

const originalFetch = globalThis.fetch;

after(() => {
  globalThis.fetch = originalFetch;
});

test("keyed memory and knowledge reads send key while preserving response memory_key", async () => {
  const requestedURLs: string[] = [];
  globalThis.fetch = async (input) => {
    requestedURLs.push(String(input));
    return Response.json({
      revision_id: "rev-1",
      memory_id: "mem-1",
      domain: "memory",
      namespace: "user/test/memory/notes",
      memory_key: "stable.key",
      status: "canonical",
      created_at: "2026-09-12T00:00:00Z",
      author: { agent_id: "test" },
      confidence: 0.9,
      tags: [],
      payload: { summary: "nested response" },
    });
  };

  const current = await getMemoryCurrent("user/test/memory/notes", "stable.key");
  await getMemoryHistory("user/test/memory/notes", "stable.key");
  await getKnowledgeCurrent("user/test/knowledge/docs", "stable/key");
  await getKnowledgeHistory("user/test/knowledge/docs", "stable/key");

  assert.deepEqual(requestedURLs, [
    "/v1/memory/current?namespace=user%2Ftest%2Fmemory%2Fnotes&key=stable.key",
    "/v1/memory/history?namespace=user%2Ftest%2Fmemory%2Fnotes&key=stable.key",
    "/v1/knowledge/current?namespace=user%2Ftest%2Fknowledge%2Fdocs&key=stable%2Fkey",
    "/v1/knowledge/history?namespace=user%2Ftest%2Fknowledge%2Fdocs&key=stable%2Fkey",
  ]);
  assert.equal(current.memory_key, "stable.key");
  assert.equal(current.payload.summary, "nested response");
});

test("memory writes send flat content and keep nested response content", async () => {
  let requestBody: unknown;
  globalThis.fetch = async (_input, init) => {
    requestBody = JSON.parse(String(init?.body));
    return Response.json({
      revision_id: "rev-2",
      memory_id: "mem-2",
      domain: "memory",
      namespace: "user/test/memory/notes",
      memory_key: "flat.write",
      status: "canonical",
      created_at: "2026-09-12T00:00:00Z",
      author: { agent_id: "test" },
      confidence: 0.9,
      tags: [],
      payload: {
        summary: "summary",
        body: "body",
        data: { exact: 7 },
        data_schema_hash: "schema-hash",
      },
    });
  };

  const result = await memoryWrite({
    namespace: "user/test/memory/notes",
    memory_key: "flat.write",
    author: { agent_id: "test" },
    summary: "summary",
    body: "body",
    data: { exact: 7 },
    data_schema_hash: "schema-hash",
  });

  assert.deepEqual(requestBody, {
    namespace: "user/test/memory/notes",
    memory_key: "flat.write",
    author: { agent_id: "test" },
    summary: "summary",
    body: "body",
    data: { exact: 7 },
    data_schema_hash: "schema-hash",
  });
  assert.equal("payload" in (requestBody as object), false);
  assert.equal(result.memory_key, "flat.write");
  assert.deepEqual(result.payload.data, { exact: 7 });
});

test("demo memory writes translate flat requests into nested revision responses", () => {
  const result = demo.memoryWrite({
    namespace: "user/test/memory/notes",
    memory_key: "demo.write",
    author: { agent_id: "demo" },
    summary: "summary",
    body: "body",
    data: { exact: 7 },
    data_schema_hash: "schema-hash",
  });

  assert.equal(result.memory_key, "demo.write");
  assert.deepEqual(result.payload, {
    summary: "summary",
    body: "body",
    data: { exact: 7 },
    data_schema_hash: "schema-hash",
  });
});
