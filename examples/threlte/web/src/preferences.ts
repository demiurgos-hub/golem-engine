import { writable } from 'svelte/store';

// Presentation policy belongs to the application, outside synchronized entities.
export const externalMotion = writable(false);
