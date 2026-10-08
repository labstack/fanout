# Agent dashboards Milestone 3 — build measurements

Task 1b measurements only. Controller light/dark live-chat, console/network and screenshot acceptance remains pending; automated fixtures are not browser evidence.

| MCP app output | Raw bytes | gzip -9 -n bytes |
|---|---:|---:|
| `internal/mcp/apps/panels.html` | 2,342,591 | 772,682 |

The five retired outputs totaled 7,076,818 raw bytes in the supplied baseline. The generic output is below the 2,500,000-byte target and reduces that aggregate by 4,734,227 bytes (66.90%).

SPA embedded tree: 2,972,143 → 2,974,801 raw bytes. Route-related chunk measurements below include shared chunks; no SPA threshold was specified.

| SPA chunk family | Before raw bytes | After raw bytes |
|---|---:|---:|
| `chat-*.js` | 17,950 | 19,565 |
| `chat.index-*.js` | 70 | 70 |
| `chat._threadId-*.js` | 70 | 70 |
| `dashboards.index-*.js` | 430 | 430 |
| `dashboards._dashboardId-*.js` | 517 | 517 |
| `page-*.js` | 1,118,815 | 1,118,815 |
| `settings-*.js` | 12,390 | 12,390 |
| `mcp-app-frame-*.js` | 242,399 | 243,366 |
| `index-*.js` | 705,786 | 705,786 |
| `preload-helper-*.js` | 16,963 | 17,039 |

`page-*.js` remains the shared dashboard page chunk. The MCP host frame remains lazy; the app build rejects runtime imports of SPA routes/auth/API modules and emits only `panels.html` with inlined styles, fonts and Blob worker code.

Verification: host Vitest and TypeScript gates, seeded MCP fragment calls, source boundary fixtures, app build graph/CSP fixtures, and temporary-output byte/path comparisons passed. Browser acceptance and the clean-source private audit binary run belong to the controller review gate.
