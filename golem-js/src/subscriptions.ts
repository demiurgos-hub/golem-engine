/** Releases one subscription. */
export type Unsubscribe = () => void;

/**
 * Release listeners once, in registration order. The first exception propagates
 * and stops cleanup; even skipped callbacks remain inactive on later calls.
 */
export function combineUnsubscribers(...items: Unsubscribe[]): Unsubscribe {
  let active = true;
  return () => {
    if (!active) return;
    active = false;
    for (const unsubscribe of items) unsubscribe();
  };
}
