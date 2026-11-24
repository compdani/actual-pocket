import type * as T from './index';

// Look up the global on each call so a fetch installed or stubbed after this
// module loads is still used.
export const fetch: typeof T.fetch = (input, options) =>
  globalThis.fetch(input, options);
