import type { GameClient } from "golem-engine";
import type { GolemPluginConfig } from "../src/index.js";

declare const client: GameClient;

const synchronousConfig = {
  createClient: () => client,
  connectionOptions: () => "ws://localhost/game",
} satisfies GolemPluginConfig;

const asynchronousConfig = {
  createClient: () => client,
  connectionOptions: async () => ({
    url: "https://localhost/game",
    transport: "webtransport" as const,
  }),
} satisfies GolemPluginConfig;

const fallbackPlanConfig = {
  createClient: () => client,
  connectionOptions: async () => ({
    candidates: [
      {
        url: "https://localhost/api/wt",
        transport: "webtransport" as const,
      },
      {
        url: "ws://localhost/api/ws",
        transport: "websocket" as const,
      },
    ],
    resolveOptions: async (endpoint: {
      transport: "websocket" | "webtransport";
      url: string;
    }) => endpoint,
  }),
} satisfies GolemPluginConfig;

synchronousConfig.connectionOptions;
asynchronousConfig.connectionOptions;
fallbackPlanConfig.connectionOptions;
