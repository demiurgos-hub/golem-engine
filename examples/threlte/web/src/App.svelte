<script lang="ts">
  import { GolemProvider } from 'golem-threlte';
  import { createClient } from './generated/client/client.js';
  import { golemAdapter } from './generated/threlte/GolemThrelte.js';
  import Garden from './Garden.svelte';

  const source = {
    kind: 'owned' as const,
    config: {
      createClient,
      // Evaluated after provider mount, and again on each reconnect attempt.
      connectionOptions: async () => ({
        transport: 'websocket' as const,
        url: `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/api/ws`,
      }),
      maxReconnectAttempts: 0,
      reconnectBaseDelay: 500,
    },
  };
</script>

<GolemProvider adapter={golemAdapter} {source}>
  <Garden />
</GolemProvider>
