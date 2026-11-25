# Map view

## 1. Purpose

A second view of a project beside the Board: cards on a canvas with their links, grouped by story and lane, so a long backlog reads as groups and dependencies. Built for 5,000 cards per project; every read is cursor-paged.

## 2. Behaviour

### 2.1 Which view opens

- A Board/Map switch sits at the left of both toolbars (`components/BoardMapSwitch.vue`). Routes: `/p/:slug/board`, `/p/:slug/map`, and `/p/:slug` (named `project`), which never renders and redirects (`router.ts`).
- The project route opens, in order: the view saved for this project in this browser (`lorvik.view.<slug>`); else the Map when the project has 100 open cards or fewer (`MAP_DEFAULT_MAX_OPEN`, open = backlog, ready, working, waiting), the Board above that.
- Going to a view by the switch or by a link that names it saves it. The project route's automatic pick is not saved, so a project that grows past 100 open cards opens on the Board for a viewer who never chose. The header's Board link, `g b`, a card page's "Board" link and the project switcher go through the project route (dashboard.md 4).

### 2.2 What the map draws

- Each story is a frame holding its children, with its status, lane, and progress (done or dropped over all children, from the server's group counts, so children need not be loaded). Cards outside any story sit in a frame per lane, plus "No lane". A lane frame, its closed box and its list are headed by the lane's name alone, since the list icon marks a lane; screen readers hear "Lane <name>" (#438). Notes appear only when they mention a drawn card.
- Status shows as colour and a text tag, kind as an icon. Magenta stays reserved for what needs the viewer (`humanAsk`).
- The Board's Awaiting you strip (decisions.md, dashboard.md 2.14) sits above the map's toolbar, at most 40% of the view's height, scrolling past that. Clicking the title of a row of this project moves the map to its card instead of opening it (`locate` in `views/MapView.vue`): what hides the card is turned off as for a hidden search hit (2.3), a closed box holding it opens on the canvas, or shows alone when opening it would pass 200 cards (2.4), "only cards with links" is turned off if it hides the card once the box's cards are read, and the card is highlighted and focused. Focus stays on the row until the card is drawn, and there if it never is within 10 s. The strip's heading stays in view while its rows scroll. The global project's rows, the Answer, Accept and Reject buttons, and a ctrl or middle click open the card as on the Board (#258 D).
- The Board's Replies strip (dashboard.md) sits under it, at most a fifth of the view's height, with Mark all read; a chip opens the card panel, as on the Board (#291).
- The minimap in the corner rings each card that needs the viewer in magenta, filled, and each closed box holding one, tinted, a few minimap pixels larger than the tile so it shows at any scale.
- Lines: blocks (arrow), relates, duplicates, supersedes, applies, and mention (card to note). Parent and child are the frame itself, not a line. Relates and duplicates have no direction (`isUndirected` in `lib/map/types.ts`, the one owner); they are drawn in the blocks order, box order between boxes and card order inside one (`flippedEdges` in `elkGraph.ts`), so two boxes linked both ways do not loop.
- Layout: ELK layered, in a worker (`lib/map/layout.ts`, `elk.ts`). A box's unlinked cards sit in a grid that reads wider than tall, up to 8 columns (`gridCols` in `lib/map/sizes.ts`), so a long lane does not set the whole map's height. Linked groups are packed into a canvas near the pane's shape by skyline packing (`lib/map/pack.ts`): tallest first, each where its top is nearest the canvas's top, so short boxes fill the space beside a tall one.
- Clicking a card opens the Board's side panel (dashboard.md 2.2).

### 2.3 Filters and search

- Filters: status (default: the open statuses; Done and Dropped are off), kind, lane (including "No lane"), story, and "only cards with links". They live in the URL and are remembered per project in this browser under `lorvik.map.filters.<slug>`; a Map link with no filter in it restores them.
- "Showing n of m" sits with the filters on the left: drawn items of the project's total.
- Search matches a card or story by number (`12`, `#12`, `s12`) or title, a note by title. A line just inside the map's top edge, under the box, reads "1 of n" (or "No matches"); Enter moves to the next match and centres it. An × in the box clears it. In the collapsed view the server is also asked (the card list's `q`, with the shown statuses), and a match inside a closed box highlights the box and offers to open it.
- The server is also asked for matching cards in every status (`findCards` in `lib/cardCache.ts`): the shown statuses and the others as two reads of at most 20 each, so hidden matches never crowd out shown ones. A match the map does not draw is counted on a chip under the box ("n more hidden by filters", or "n hidden by filters" with the "No matches" counter hidden when the map draws none); the chip opens a list of those cards, each with why it is hidden (its status, kind, lane, story, box or the links filter, or "Not loaded yet") (`lib/map/hidden.ts`). Choosing one turns off only what hides it, leaves the rest of the filters, and moves to the card, or to its closed box in the collapsed view, highlighted and focused; layouts that land while the turned-on statuses load keep moving the map to it for 1.5 s (`showHidden` in `views/MapView.vue`). Escape closes the list first, then clears the search.

### 2.4 Collapsing

- When the shown statuses hold more than 200 cards (`COLLAPSE_ABOVE`, judged from the project's counts by status before any card is read), the map draws one closed box per story and per lane instead of cards: title, matching count, status breakdown. No card pages are read for it.
- Lines between closed boxes merge into one per pair and link kind, labelled with the count (from `/map/group-links`); relates and duplicates merge across both directions. Clicking a label, or Enter or Space on it, lists those links in a popover.
- Clicking a box (`?list=<key>`) lists its cards beside the canvas, 50 a page. Expand opens it on the canvas (`?open=<key>,…`, a push, so Back closes it) while the opened boxes' cards stay within 200; otherwise it shows a map of that group alone (`?group=<key>`). An opened box has a Collapse button at the right of its head (`closeBox`, a push too), which draws it closed again (#286). Picking a story in the filter also shows it alone and clears any `group`.
- Group keys: `story:<number>`, `lane:<name>`, and `lane:` for no lane.
- In the collapsed view the kind and lane filters narrow only opened boxes, because the server counts boxes by status; while either filter is set, a note at the foot of the map says so.

### 2.5 Loading and live changes

- The map reads through the card cache shared with the Board (`lib/cardCache.ts`): one entry per project, at most 3 projects (least recently used dropped), data older than 5 minutes read again on the next visit, emptied on sign-out.
- In the cards view it reads the shown statuses' card pages and draws from the first page; a status turned on later loads on demand. Links, note mentions, notes and story groups come as whole paged lists, never per card.
- A view holds its project with what it needs (`holdProject`). On a live change, after 400 ms of quiet and after every read already running has finished, the cache re-reads only what a holding view needs; a project no view holds is marked stale instead.
- Layout runs at most twice a second (`RELAYOUT_MIN_MS`). A new layout waits while the viewer pans, scrolls, hovers or focuses a node. Switching project lets go of the hover, since the old project's tiles leave without a mouseleave and a hover left on one would hold back every layout. Vue Flow starts a move on every press on the pane but ends it only when the view changed, so the press ending (`pointerup` or `pointercancel`) ends the move too; before that, a click on a box held every later layout until a reload, as with Expand (#286). Cards glide to new places in 0.2 s while lines fade; reduced motion turns the glide off. The view is fitted to the map on every layout, and whenever the pane changes size (as when the Awaiting you strip appears), until the viewer scrolls, drags, clicks or moves focus on it, then left alone.
- A blocking progress note shows only until the first cards are drawn; after that a small note in the corner shows pages still arriving.

### 2.6 Keyboard

- Cards, story heads, notes and closed boxes are links or buttons, so Tab reaches them; arrows move to the nearest node in that direction (`lib/map/nav.ts`); with nothing focused the first arrow focuses the top-left card. Enter opens the card or a box's list. The card action keys (dashboard.md 2.9) act on the focused card. `/` focuses search, `f` fits, `+` and `-` zoom, Escape clears search and hover.

### 2.7 Intended changes

None.

### 2.8 Known defects

- Opening a box or a group alone reads the project's whole link list (paged), because the links route has no card filter.

## 3. Rules

- The map never shows a card the card list would hide: every route it reads applies the card list's project visibility.
- The URL holds the filters, the search and the box state (`list`, `open`, `group`); browser storage holds only the per-project view choice and filters.

## 4. Data

No tables. Browser storage: `lorvik.view.<slug>` (`board` or `map`) and `lorvik.map.filters.<slug>` (the filter query).

## 5. API

- `GET /projects/{slug}/cards` with `status` (comma list), `parent`, `kind`, `q`, and, for lane boxes, `lane`, `no_lane=true`, `no_story=true`.
- `GET /projects/{slug}/links` and `/mentions`: every link with an end in the project, and every note mention, paged.
- `GET /projects/{slug}/map/groups` and `/map/group-links`: per story and lane status counts, and link counts between groups by kind.
- `GET /projects/{slug}` for the counts by status, `GET /notes?project=` for note titles, and the event stream for live changes.

Shapes are in `internal/apitypes` and the route table in `docs/api.md`.

## 6. Jobs

None.

## 7. Why it is this way

- Separate paged lists for cards, links, mentions and groups instead of one map endpoint: a project can hold 5,000 cards, and a single response would be unbounded. Rejected: a bulk map endpoint (owner, 2026-11-06).
- Collapse is judged from server counts before reading cards, so a large project opens fast instead of reading every page to find out it is large.
- An automatic view pick is not saved, so the default can still follow the project's size; an explicit choice is kept.
