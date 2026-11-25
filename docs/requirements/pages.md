# HTML pages on cards and notes

## 1. Purpose

A card or a note may carry one self-contained HTML page beside its Markdown body, so an agent can explain a hard question, a design or a comparison with layout, diagrams and small interactions. The dashboard shows the page first, in a sandboxed frame that can never act as the viewer.

## 2. Behaviour

### 2.1 Storing a page

- `page` on card create and update, and on note create and update, sets the page; an empty string on update removes it. At most 1 MiB (`domain.MaxPageBytes`) valid UTF-8 and not only whitespace, else 422 on `page`. An empty file given to `--page-file` is a usage error, so it never removes a page by accident. The routes that take a page accept request bodies up to 3 MiB, because JSON escaping can grow a 1 MiB page; every other route keeps 1 MiB.
- With a page, the body may be empty; a note needs a body or a page.
- Setting the page a card or note already has changes nothing: no new version, no event.
- A page change is a text change: it saves the previous title, body and page as a revision, bumps the version, and its event names `page` among the changed fields. A settled decision's page is frozen with its text (409, as for the body, board.md).
- Answering a question with `decide` copies the question's page to the decision; when the question's body is empty, the decision's context reads "See the page."
- Search matches a card or note by its title and body, or by its page's visible text (`domain.PageText`: text outside `head`, `script`, `style`, `template` and `noscript`, plus `title` text and `alt`, `aria-label` and `title` values; at most 256 KiB). The two sides are a union of ids so each uses its own index.

### 2.2 Reading a page

- `page_bytes` on a card, a note and their briefs is the page's size, absent when there is none. The page itself is read only on its own route, as JSON (`HTMLPage`: `html`, `bytes`, `version`), never served as `text/html`. A card or note without a page is a 404 there.
- CLI: `--page-file PATH` (`-` reads stdin, not together with `-b -`) on `add`, `ask`, `edit`, `note new` and `note edit`; `--page-file ""` on an edit removes the page. `show` and `note show` print `Page: <size>, open <dashboard link>` after the meta line; `--page` prints the HTML source instead (exit 3 when there is none; with `--json`, the `HTMLPage`). A file over 4 MiB is refused before sending (exit 2); the 1 MiB limit is the server's.
- Dashboard: dashboard.md 2.13 (the frame, its lockdown, tabs, Expand, the tile mark).
- Agents: `skills/lorvik/reference/pages.md` says when to write a page and what works inside the frame.

### 2.3 Intended changes

- Serve pages from a separate content domain once Lorvik is hosted, so the dashboard can take a strict security policy of its own; while pages are inline it cannot, because a `srcdoc` frame inherits the dashboard's policy (#210, option C).

### 2.4 Known defects

None known.

## 3. Rules

- Reading a page needs read on its card or note; writing one needs what writing the body needs. The page routes apply the same visibility as the card and note routes.
- Page script never runs on the dashboard's origin. The frame is `sandbox="allow-scripts"` without `allow-same-origin`, so it gets an opaque origin: no access to the sign-in token in `localStorage`, the dashboard's document, cookies or storage. It cannot navigate the dashboard, open popups or modals, or submit forms.
- The frame's policy allows inline script and style, scripts and styles from `cdnjs.cloudflare.com`, `cdn.jsdelivr.net` and `unpkg.com`, and `data:` and `blob:` images; everything else is refused: fetch, XHR, WebSocket, beacons, fonts, outside images, frames, workers, objects, form actions, `<base>`, and `eval` or `new Function`.
- The dashboard takes one message from the frame, `{ lorvikPageHeight: n }`, only from its own frame's window, clamped to 240 to 4000 px, and sends nothing back. A page cannot answer a question or change anything.
- Accepted risks: a page can navigate its own frame, put data in a CDN request URL, or open a WebRTC connection (the policy says `webrtc 'block'`, which Chrome 156 does not enforce) to send data out, can freeze the tab with a runaway script, and can draw anything, including a fake form; the dashboard's label row around the frame is outside its reach.

## 4. Data

- `card_pages` and `note_pages`: one row per page, the HTML and its extracted text, with a full-text index on the text. Read only when the page is asked for.
- `cards.page_bytes` and `notes.page_bytes`: the page size, 0 for none, so lists show the mark without reading pages.
- `card_revisions.page` and `note_revisions.page`: null when the edit after that version left the page alone (its page is the next newer revision's that is not null, else the current one), `''` when that version had no page, otherwise the page at that version; reads resolve it, so a body-only edit never copies a page.

## 5. API

Routes and shapes are in `docs/api.md` (the `page` rows of cards and notes) and `internal/apitypes` (`HTMLPage`, `CreateCard.Page`, `UpdateCard.Page`, `PageBytes`).

- `GET /projects/{slug}/cards/{number}/page` and `GET /notes/{key}/page?project=`: read. Used by the card and note views and by `show --page`.
- `GET .../revisions/{version}` returns that version's page.

## 6. Jobs

None.

## 7. Why it is this way

- A frame with `srcdoc` inside the dashboard, not a page served by lorvikd: a page served from the dashboard's origin could read the token; a separate origin needs a second domain Lorvik does not have yet (owner, #210).
- Side tables, not a column on the card row: card lists and the board never carry up to 1 MiB per row, and the page loads only when opened (owner, #213).
- System fonts only and no outside requests besides three CDNs: pages render without a font service and cannot report who opened them (owner, #211).
