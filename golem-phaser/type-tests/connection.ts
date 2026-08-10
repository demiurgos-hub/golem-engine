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

synchronousConfig.connectionOptions;
asynchronousConfig.connectionOptions;
