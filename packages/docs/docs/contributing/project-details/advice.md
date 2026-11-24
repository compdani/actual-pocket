# Important Advice

- Any changes made to the `global.Actual` object must happen inside the browser preload script (`browser-preload.js`) and, for desktop-specific behavior, the Wails bridge (`src/util/wails.ts`).

- Similarly, any changes made to `global.Actual` should be manually tested in the desktop app as well as the browser.
