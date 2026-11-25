# lorvik-web

Vue 3 + TypeScript + Vite + Tailwind CSS v4 frontend for Lorvik.

- `bun install` installs dependencies.
- `bun run dev` starts Vite on :5173 and proxies `/api` to http://localhost:8080.
- `bun run build` type-checks (vue-tsc) and builds into `dist/`, keeping `dist/.gitkeep`.
- `VITE_API_BASE` sets the API base URL (default empty, same origin), e.g.
  `VITE_API_BASE=https://lorvik.example.com bun run build`.
