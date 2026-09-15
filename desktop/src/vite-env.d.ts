/// <reference types="vite/client" />

// `tsconfig.json` pins `types` to vitest's globals, so Vite's own ambient
// declarations are not picked up automatically — and without them an
// `import … from "./thing.webp"` is an error rather than a URL. The grain
// backdrop is the first asset this application imports rather than references
// from CSS, which is why this file did not exist before.
