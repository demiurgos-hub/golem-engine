// vite's emptyOutDir removes cmd/golem-inspector/web/dist/.gitkeep, which
// go:embed needs so the hub builds before the UI has been built. Restore it.
import { writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

writeFileSync(fileURLToPath(new URL('../../cmd/golem-inspector/web/dist/.gitkeep', import.meta.url)), '');
