# Go Best Practices for AI Coding Agents

Research input for a future "Go coding skill". Every rule cites a primary source. Items marked **[DISAGREE]** are places where authoritative sources differ. Items marked **[UNVERIFIED]** were not confirmed from the fetched page text.

## Version facts

- Verified on 2026-10-18 at https://go.dev/doc/devel/release
- Current stable: **Go 1.27** (go1.27.0 released 2026-08-19; latest patch **go1.27.2**, 2026-10-08).
- Previous supported: Go 1.26 (go1.26.0 2026-02-10; latest patch go1.26.9, 2026-10-08). Both 1.27.2 and 1.26.9 carry security fixes (go command, crypto/tls, html/template, net/http, net/textproto, os).
- Support policy: each major release is supported until two newer majors exist. Go 1.25 (go1.25.14, 2026-08-19) is now unsupported. Go 1.24 and older are unsupported.
- Cadence: majors in February and August.
- Feature-to-version map (all verified in the release notes, https://go.dev/doc/go1.NN):

| Feature | Version |
|---|---|
| Per-iteration loop variables (needs `go 1.22` in go.mod) | 1.22 |
| `for i := range n`, `min`/`max`/`clear` builtins (1.21) | 1.21/1.22 |
| Range-over-func, `iter` package | 1.23 |
| Generic type aliases, `tool` directive in go.mod, `testing.B.Loop`, `T.Context`, `T.Chdir`, `os.Root`, `omitzero`, `strings.Lines/SplitSeq`, `weak`, `runtime.AddCleanup` | 1.24 |
| `sync.WaitGroup.Go`, `testing/synctest` (GA), `T.Attr`, vet `waitgroup` and `hostport`, container-aware GOMAXPROCS, `go.mod ignore` | 1.25 |
| `new(expr)`, `errors.AsType`, `go fix` rewritten with modernizers, Green Tea GC default, `slog.NewMultiHandler`, `T.ArtifactDir`, `go mod init` writes `go 1.(N-1).0` | 1.26 |
| Generic methods (methods with own type params), `encoding/json/v2` + `jsontext`, `synctest.Sleep`, goroutineleak pprof profile GA, `go mod tidy` merges require blocks, vet printf `%w` pointer check | 1.27 |

- Go 1.27 language changes (https://go.dev/doc/go1.27): generic methods, struct literal keys may be promoted fields, more general function type inference.
- Practical guidance for the skill: new modules should set the `go` line deliberately. `go mod init` now writes one minor version below the toolchain (1.26+), so a 1.27 toolchain writes `go 1.26.0`. Raise it with `go get go@1.27.0` if newer features are needed. The `go` line gates language semantics (loopvar needs >= 1.22, range-over-func needs >= 1.23).
- Caveat: https://go.dev/doc/faq still says methods cannot have their own type parameters (`#generic_methods`). That FAQ entry is stale as of Go 1.27; the release notes win.

---

## 0. How to use this document

- Rules are grouped by the six requested topics (sections 1 to 6).
- Source abbreviations: **EG** Effective Go, **CRC** Code Review Comments, **GSD** Google Style Decisions, **GBP** Google Best Practices, **UBER** Uber guide.
- EG itself states it was written in 2009 and does not cover generics or modules (https://go.dev/doc/effective_go). Prefer newer sources when they conflict.

---

## 1. Idiomatic Go

### 1.1 Formatting, naming, structure

- Run `gofmt` (or `goimports`) always; never hand-align. Rationale: formatting is not debatable and diffs stay minimal. https://go.dev/doc/effective_go#formatting, https://go.dev/wiki/CodeReviewComments#gofmt
- Packages: lowercase, single word, no underscores, no `util`/`common`/`misc`/`api`/`types`/`helper`. Rationale: package name is part of every call site. https://go.dev/wiki/CodeReviewComments#package-names, https://google.github.io/styleguide/go/decisions
- Do not repeat the package name in identifiers: `yamlconfig.Parse`, `bufio.Reader`, `ring.New`. Rationale: call site already reads `pkg.Name`. https://go.dev/doc/effective_go#package-names, https://google.github.io/styleguide/go/best-practices
- No `Get` prefix on getters: `Owner()` and `SetOwner()`. Use `Compute`/`Fetch` for expensive or remote reads. https://go.dev/doc/effective_go#Getters, https://google.github.io/styleguide/go/decisions
- MixedCaps only; never underscores or `ALL_CAPS` constants. https://go.dev/doc/effective_go#mixed-caps, https://go.dev/wiki/CodeReviewComments#mixed-caps
- Initialisms keep one case: `URL`, `urlPath`, `ServeHTTP`, `appID`, `xmlHTTPRequest`. https://go.dev/wiki/CodeReviewComments#initialisms
- Receiver names: one or two letters, consistent per type, never `this`/`self`/`me`. https://go.dev/wiki/CodeReviewComments#receiver-names
- Variable name length proportional to scope; omit type words (`users`, not `userSlice`). https://go.dev/wiki/CodeReviewComments#variable-names, https://google.github.io/styleguide/go/decisions
- One-method interfaces are named method plus `-er` (`Reader`, `Writer`). Use `String`, not `ToString`. https://go.dev/doc/effective_go#interface-names
- Avoid shadowing predeclared identifiers (`error`, `string`, `len`, `new`). Rationale: compiles but breaks later code in scope. https://github.com/uber-go/guide/blob/master/style.md (Avoid Using Built-In Names)
- Do not name variables after imported std packages (`url`, `path`) outside tiny scopes. https://google.github.io/styleguide/go/best-practices
- Doc comments: full sentence starting with the identifier name, on every exported name. Package comment directly above `package` with no blank line. https://go.dev/wiki/CodeReviewComments#doc-comments, https://go.dev/wiki/CodeReviewComments#package-comments
- Imports: rename only on collision; group stdlib first. https://go.dev/wiki/CodeReviewComments#imports
- `import .` is forbidden in practice (CRC allows only in external test packages with circular deps; GSD says never). `import _` only in `main` or tests. https://go.dev/wiki/CodeReviewComments#import-dot, https://google.github.io/styleguide/go/decisions

### 1.2 Control flow

- Handle the error first and return; keep the happy path unindented; no `else` after a terminal `if`. https://go.dev/wiki/CodeReviewComments#indent-error-flow, https://go.dev/doc/effective_go#if
- Do not write `if x, err := f(); err != nil { ... } else { use(x) }` when `x` is needed afterwards; declare on a separate line so `x` stays in scope. https://go.dev/wiki/CodeReviewComments#indent-error-flow
- No in-band error signalling (`-1`, `""`, `nil`). Return `(T, error)` or `(T, bool)`. https://go.dev/wiki/CodeReviewComments#in-band-errors
- Prefer synchronous functions; callers can add concurrency but cannot remove it. https://go.dev/wiki/CodeReviewComments#synchronous-functions
- Use `switch` over long `if/else` chains; no `fallthrough` unless intended; no `break` at the end of a case. https://go.dev/doc/effective_go#switch, https://google.github.io/styleguide/go/decisions
- `Must*` helpers only at package init or in test helpers. https://google.github.io/styleguide/go/decisions

### 1.3 Types, receivers, values

- Make the zero value useful (`sync.Mutex`, `bytes.Buffer`). Rationale: removes constructors and init-order bugs. https://go.dev/doc/effective_go#data
- Prefer `var t []string` (nil slice) over `t := []string{}`. Exception: JSON encoding where `[]` versus `null` matters. Check emptiness with `len(s) == 0`; never design APIs that distinguish nil from empty. https://go.dev/wiki/CodeReviewComments#declaring-empty-slices, https://google.github.io/styleguide/go/decisions
- Receiver kind: pointer if the method mutates, the type holds a `sync.Mutex`, the struct is large, or it holds pointers to mutable state. Value for maps, funcs, chans, small immutable value types (`time.Time`). Never mix kinds on one type. "When in doubt, use a pointer." https://go.dev/wiki/CodeReviewComments#receiver-type, https://go.dev/doc/faq#methods_on_values_or_pointers
- Do not pass `*string` or `*io.Reader` to save bytes; pointers to interfaces are almost always a bug. https://go.dev/wiki/CodeReviewComments#pass-values, https://go.dev/doc/faq#pointer_to_interface
- Do not copy a `T` whose methods are on `*T` (`bytes.Buffer`, `sync.Mutex`, `sync.WaitGroup`). `go vet copylock` catches this. https://go.dev/wiki/CodeReviewComments#copying
- Use composite literals `&T{...}`; always use field names for out-of-package struct literals. https://go.dev/doc/effective_go#composite_literals, https://google.github.io/styleguide/go/decisions
- Constants/enums: `iota`; start at `iota + 1` when the zero value should mean "unset". https://github.com/uber-go/guide/blob/master/style.md (Start Enums at One)
- Use `any`, not `interface{}`. https://google.github.io/styleguide/go/decisions
- `new(expr)` (Go 1.26) replaces `ptr := func(v int) *int {...}` helpers for optional pointer fields: `Age: new(yearsSince(born))`. https://go.dev/doc/go1.26

### 1.4 Interfaces

- Define interfaces in the consuming package, not the producing one. Producers return concrete types. Do not create "mock-friendly" interfaces on the producer side. https://go.dev/wiki/CodeReviewComments#interfaces, https://google.github.io/styleguide/go/decisions
- Do not define an interface before there is a real second implementation or consumer need. Same sources.
- Keep interfaces small (1 to 2 methods). https://go.dev/doc/effective_go#interfaces_and_types
- Compile-time check for exported types that must satisfy an interface: `var _ http.Handler = (*Handler)(nil)`. https://github.com/uber-go/guide/blob/master/style.md, https://go.dev/doc/effective_go#blank_implements
- **[DISAGREE]** EG says a constructor that exists only to implement an interface should return the interface (`crc32.NewIEEE` returns `hash.Hash32`, https://go.dev/doc/effective_go#generality). CRC and GSD say "accept interfaces, return concrete types". Skill default: return concrete types; return an interface only to hide a family of implementations behind one constructor.

### 1.5 Embedding

- Embedding is composition with method promotion, not inheritance; promoted methods run with the inner type as receiver. https://go.dev/doc/effective_go#embedding
- **[DISAGREE]** UBER says avoid embedding in public structs (leaks implementation, blocks evolution, makes later changes breaking; write delegating methods). EG encourages embedding for composing interfaces and promoting methods. Skill default: embed interfaces freely; avoid embedding concrete types in exported structs. https://github.com/uber-go/guide/blob/master/style.md
- Do not embed `sync.Mutex`; use a named non-embedded field so `Lock`/`Unlock` do not leak into the API. https://github.com/uber-go/guide/blob/master/style.md (Zero-value Mutexes)

### 1.6 Boundaries and globals

- Copy slices and maps when receiving them into a struct and when returning internal ones. Rationale: aliasing causes races and surprise mutation. https://github.com/uber-go/guide/blob/master/style.md (Copy Slices and Maps at Boundaries)
- Avoid mutable package-level state; inject dependencies (`now func() time.Time`). https://github.com/uber-go/guide/blob/master/style.md (Avoid Mutable Globals)
- Avoid `init()`; if unavoidable it must be deterministic and free of I/O and env access. Never start goroutines in `init`. https://github.com/uber-go/guide/blob/master/style.md
- Call `os.Exit` / `log.Fatal` only in `main`, ideally once. Put logic in `run() error` so defers run and the logic is testable. https://github.com/uber-go/guide/blob/master/style.md (Exit in Main)
- Tag every field of structs that are marshaled to JSON/YAML. https://github.com/uber-go/guide/blob/master/style.md (Use field tags in marshaled structs)
- `math/rand` and `math/rand/v2` must never generate keys or tokens; use `crypto/rand` (`rand.Text` for text). https://go.dev/wiki/CodeReviewComments#crypto-rand

### 1.7 Named results, options, generics-adjacent style

- Named results only for documentation of same-typed returns (`lat, long float64`) or to mutate in a deferred closure. Do not name results just to enable naked returns; naked returns only in very short functions. https://go.dev/wiki/CodeReviewComments#named-result-parameters
- Option struct when most callers set options or options are shared; variadic functional options when most callers need none or options are many and rare. Contexts never go in option structs. https://google.github.io/styleguide/go/best-practices
- Use `strconv` rather than `fmt` for primitive conversions and pre-size containers only with measured evidence. UBER recommends `strconv` and capacity hints; GBP says size hints only with empirical evidence. **[DISAGREE, mild]** https://github.com/uber-go/guide (Performance section), https://google.github.io/styleguide/go/best-practices
- Specify channel direction in signatures (`<-chan T`, `chan<- T`). https://google.github.io/styleguide/go/best-practices

### 1.8 Where the four guides disagree (summary)

| Topic | Positions | Skill default |
|---|---|---|
| Embedding in public structs | UBER: avoid. EG: encouraged | Avoid for concrete types |
| Return interface vs concrete | EG: interface ok. CRC/GSD: concrete | Concrete |
| Channel buffer size | UBER: 0 or 1 only. EG shows buffered semaphores/worker pools | Unbuffered or 1 unless justified; size to a known worker count |
| Line length | UBER: soft 99. GSD/CRC: no limit | Follow repo; gofmt does not wrap |
| Import groups | UBER: 2 groups. GSD: stdlib, other, proto, blank | stdlib then everything else; goimports |
| Assertion libraries | GSD/GBP: avoid. UBER: neutral; widely used in industry | Follow repo; see 5.4 |
| `panic` for unrecoverable | GBP prefers `log.Fatal`; UBER/CRC say return errors, panic only for truly irrecoverable/startup | Return errors; panic only for programmer errors/startup |
| Atomics | UBER: use `go.uber.org/atomic`. stdlib has typed atomics (`atomic.Bool`, `atomic.Int64`) | Use stdlib `sync/atomic` typed values; UBER guidance predates them |
| `else` after terminal `if` | All agree: omit | Omit |

---

## 2. Error handling

### 2.1 Core mechanics

- `error` is an interface with `Error() string`; return `error`, not a concrete pointer type. Rationale: returning a nil `*MyErr` as `error` yields a non-nil interface. https://go.dev/doc/faq#nil_error, https://go.dev/blog/error-handling-and-go
- Never discard errors with `_` (EG calls `fi, _ := os.Stat(path)` "terrible practice"). If truly safe, comment why. https://go.dev/doc/effective_go#blank, https://go.dev/wiki/CodeReviewComments#handle-errors, https://google.github.io/styleguide/go/decisions
- Error strings: lowercase first letter (unless proper noun/acronym), no trailing punctuation. They are embedded in larger messages. https://go.dev/wiki/CodeReviewComments#error-strings
- Compare with `errors.Is` (not `==`) and extract with `errors.As` (not type assertions) once wrapping is possible. https://go.dev/blog/go1.13-errors, https://pkg.go.dev/errors
- **Prefer `errors.AsType[E](err)` (Go 1.26+)** over `errors.As`: generic, type-safe, no pre-declared target variable, faster. Signature `func AsType[E error](err error) (E, bool)`. Use `errors.As` only for `go` < 1.26 modules. https://pkg.go.dev/errors, https://go.dev/doc/go1.26
- `errors.As` panics if the target is not a non-nil pointer to an error type or interface. A pointer-to-pointer (`var e *QueryError; errors.As(err, &e)`) is correct. https://go.dev/blog/go1.13-errors, https://pkg.go.dev/errors
- `errors.Join(errs...)` (Go 1.20): discards nils, returns nil if all nil, message is each error joined by newline, result implements `Unwrap() []error`. Use it to aggregate independent failures (validation, multi-close). https://pkg.go.dev/errors
- `errors.Unwrap` does NOT unwrap `Join` results (single-error form only). Use `Is`/`As`, which walk the whole tree (pre-order DFS). https://pkg.go.dev/errors
- `fmt.Errorf` supports multiple `%w` verbs (result implements `Unwrap() []error`). [UNVERIFIED on the fmt page in this run; errors package docs confirm the `Unwrap() []error` form.] https://pkg.go.dev/errors
- Custom `Is(target error) bool` should compare shallowly and must not call `Unwrap`. https://pkg.go.dev/errors
- Do not return `errors.ErrUnsupported` bare; return an error context-bearing that satisfies `errors.Is(err, errors.ErrUnsupported)`. https://pkg.go.dev/errors
- Never match on error strings; give errors structure (sentinels, types, fields) when callers must distinguish them. https://google.github.io/styleguide/go/best-practices

### 2.2 Sentinel versus typed versus plain

UBER decision table (https://github.com/uber-go/guide/blob/master/style.md, Error Types):

| Caller must match? | Message | Use |
|---|---|---|
| No | static | `errors.New` |
| No | dynamic | `fmt.Errorf` |
| Yes | static | package-level `var ErrX = errors.New(...)` |
| Yes | dynamic | custom type with `Error()` method |

- Naming: sentinels `ErrFoo` (exported) / `errFoo`; custom types `FooError`. https://github.com/uber-go/guide/blob/master/style.md (Error Naming)
- Add behavior interfaces (like `net.Error` with `Timeout()`) to let callers distinguish transient from permanent failures without concrete types. https://go.dev/blog/error-handling-and-go
- To expose an identifiable condition, return an error wrapping a sentinel: `fmt.Errorf("%q: %w", name, ErrNotFound)`. https://go.dev/blog/go1.13-errors
- If you document that a function wraps a sentinel, never return the sentinel bare; wrap it (`fmt.Errorf("%w", ErrPermission)`) so callers are forced to `errors.Is` and you may add context later. https://go.dev/blog/go1.13-errors

### 2.3 Wrap versus return as-is

- "Wrapping an error makes that error part of your API." Wrap with `%w` only if you are willing to keep returning that cause forever. https://go.dev/blog/go1.13-errors
- Do not wrap when it leaks implementation (for example `sql.ErrNoRows` from an internal storage layer). Translate to your own sentinel or use `%v`. https://go.dev/blog/go1.13-errors
- Three options per UBER: return as-is (nothing to add), `%w` (callers may inspect; "a good default for most wrapped errors"), `%v` (hide cause; can upgrade to `%w` later). https://github.com/uber-go/guide/blob/master/src/error-wrap.md
- GBP: use `%v` at RPC/IPC/storage boundaries (translate to canonical codes); use `%w` when callers need programmatic inspection or the API documents what can be unwrapped. https://google.github.io/styleguide/go/best-practices
- Keep added context short and non-stuttering. Do not write `"failed to open file: %w"` at every level; write `"open config: %w"`. Do not repeat what the inner error already says (`os.Open` errors already include the path). https://github.com/uber-go/guide/blob/master/src/error-wrap.md, https://google.github.io/styleguide/go/best-practices
- Put `%w` at the END (`"context: %w"`) so printed text reads outermost to innermost. **Exception** (GBP): sentinel/category errors may go at the front (`"%w: invalid header"`). https://google.github.io/styleguide/go/best-practices
- **[DISAGREE, mild]** UBER suggests an `err`/"Failed" marker only when an error leaves the program (logs); GBP and CRC say never add "failed:" filler that conveys nothing. Skill default: no "failed to" prefixes inside return chains.
- Handle each error once: either return it or log it, never both. Logging and returning produces duplicate log lines. https://github.com/uber-go/guide/blob/master/style.md (Handle Errors Once), https://google.github.io/styleguide/go/best-practices
- Go 1.27 vet `printf` check flags `fmt.Errorf("...: %w", p)` where `p` is `*E` but only `E` implements `error`; this breaks `errors.Is`/`As`. Fix the receiver kind. https://go.dev/doc/go1.27

### 2.4 Typed-nil trap (error specific)

- Never `return p` where `p` is a typed nil pointer in an `error`-returning function. Return the literal `nil`. https://go.dev/doc/faq#nil_error
- Corollary: declare `error` (not `*MyError`) as the return type, and do not assign `var err *MyError` then return `err`.

### 2.5 Panics versus errors

- Library code must not panic for ordinary failures; return errors. https://go.dev/doc/effective_go#panic, https://go.dev/wiki/CodeReviewComments#dont-panic
- Acceptable `panic`: truly unrecoverable invariant violation, or startup initialization (`template.Must`, `regexp.MustCompile` at package var). https://github.com/uber-go/guide/blob/master/style.md (Don't Panic)
- Internal panic/recover for parsers is acceptable only if the panic never crosses the package boundary (convert to `error`) and `recover` re-panics on foreign values. https://go.dev/doc/effective_go#recover, https://google.github.io/styleguide/go/best-practices
- `recover` works only directly inside a deferred function. https://go.dev/doc/effective_go#recover
- **[DISAGREE]** Recovering to keep a server alive: EG shows isolating a failing goroutine with recover; GBP says do not recover panics merely to avoid crashes (corrupted state) and calls `net/http`'s per-handler recovery a historical mistake. Skill default: do not add blanket recover; if you add recover middleware, log the stack and return 500.
- In tests prefer `t.Fatal` over `panic`. https://github.com/uber-go/guide/blob/master/style.md
- Type assertions: always comma-ok (`v, ok := x.(T)`) unless a panic is the intended behavior. https://go.dev/doc/effective_go#interface_conversions, https://github.com/uber-go/guide/blob/master/style.md

### 2.6 HTTP handlers

- Pattern from the Go blog: a handler type that returns an error, with a single `ServeHTTP` adapter that logs and writes the response. Keeps handlers as `return err` and centralizes status mapping. https://go.dev/blog/error-handling-and-go

```go
type appHandler func(http.ResponseWriter, *http.Request) error

func (fn appHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if err := fn(w, r); err != nil {
        // map err to status via errors.Is / errors.AsType; log once here
        http.Error(w, http.StatusText(code), code)
    }
}
```

- Map domain errors to HTTP status in one place with `errors.Is`/`errors.AsType`; never expose internal error text to clients. (The blog's `appError{Error, Message, Code}` struct is the original form; modern code typically uses a typed error plus `errors.AsType`.) https://go.dev/blog/error-handling-and-go
- Log the error where it is mapped to a response (the single place), not at every layer. See "Handle each error once" above.
- Always pass `r.Context()` downstream; do not start `context.Background()` inside a handler. https://go.dev/wiki/CodeReviewComments#contexts
- Go 1.25 added `http.CrossOriginProtection` for CSRF defence via Fetch metadata (no tokens). https://go.dev/doc/go1.25
- Go 1.26: `ServeMux` trailing-slash redirects now use 307 (was 301); `ReverseProxy.Director` is deprecated in favor of `Rewrite`. https://go.dev/doc/go1.26

### 2.7 Language-level error handling proposals (status)

- **Declined.** On 2025-06-03 the Go team (Robert Griesemer) announced: "For the foreseeable future, the Go team will stop pursuing syntactic language changes for error handling," and closed the `?` proposal (issue #71203 / discussion #71460) plus all primarily-syntactic error-handling proposals without further investigation. Earlier `check/handle` (2018) and `try` (2019) were also abandoned. https://go.dev/blog/error-syntax
- I found no evidence of reversal through 2026-10-18; the Go 1.27 release notes list no error-syntax change. https://go.dev/doc/go1.27
- Skill rule: never emit `?`, `try()`, `check`/`handle`, or `if err ...` shorthand; they do not compile. Write explicit `if err != nil { return ..., err }`.
- Rationale given: avoid multiple ways to do the same thing; explicit control flow is easier to debug; verbosity matters less when errors are actually handled (wrapped with context). https://go.dev/blog/error-syntax
- Tooling answer to boilerplate is IDE/LLM generation and helpers like `cmp.Or`, not syntax. Same source.

---

## 3. Language nuances agents commonly get wrong

### 3.1 Loop variables (Go 1.22)

- `for` loop variables (3-clause and range) are per-iteration when the module's `go` line is >= 1.22. Closures and goroutines capturing `v` or `i` see the iteration's own value. `v := v` and `tt := tt` are no longer needed. https://go.dev/blog/loopvar-preview, https://go.dev/doc/faq#closures_and_goroutines
- The semantics are per-module via `go.mod` (or per-file `//go:build go1.22`). In modules with `go < 1.22` the old shared-variable behavior still applies. Check the `go` line before dropping `x := x`. https://go.dev/blog/loopvar-preview
- Tests that "passed by accident" with parallel subtests (all seeing the last value) can start failing after the bump. https://go.dev/blog/loopvar-preview
- `for i := range 10` (range over int, Go 1.22) replaces `for i := 0; i < 10; i++`; `n <= 0` runs zero iterations. https://go.dev/ref/spec (recalled text)
- Go 1.24 `copylock` vet flags `sync.Locker` values in 3-clause for-loop variables because they are copied per iteration. https://go.dev/doc/go1.24

### 3.2 nil interfaces versus nil pointers

- An interface is `nil` only if both its type and value words are unset. A nil `*T` stored in an interface is a non-nil interface. https://go.dev/doc/faq#nil_error
- Pointer-to-interface (`*io.Reader`) satisfies nothing useful; "almost never" correct. https://go.dev/doc/faq#pointer_to_interface
- Comparing two interface values with identical non-comparable dynamic types (for example two slices) panics at run time; map keys of interface type panic on unhashable dynamic values. https://go.dev/ref/spec (recalled text, [UNVERIFIED] verbatim)
- Go 1.25 compiler fix: code in Go 1.21 to 1.24 that used a result before checking `err` could, due to a compiler bug, delay the nil-check panic. It now panics correctly. Always check `err` before using the value. https://go.dev/doc/go1.25

### 3.3 Slices and maps

- A slice is (pointer, len, cap). Reslicing and passing slices share the backing array; writes through one are visible through the other. https://go.dev/blog/slices-intro
- `append` may or may not reallocate. Always assign the result (`s = append(s, x)`). When capacity suffices, the result aliases the input array; two appends from the same base slice can overwrite each other. https://go.dev/blog/slices-intro
- Large backing arrays are retained by tiny sub-slices: copy out (`slices.Clone`, `copy`) before returning a small piece of a big buffer. https://go.dev/blog/slices-intro
- Nil slice is valid for `len`, `cap`, `range`, `append`. https://go.dev/blog/slices-intro
- Maps: reading a missing key yields the zero value; use `v, ok := m[k]`. Reading and ranging concurrently is safe only if nobody writes; concurrent write is a fatal error. Protect with `sync.RWMutex` or own the map in one goroutine. https://go.dev/doc/faq#atomic_maps
- `sync.Map` is for specific patterns (append-only caches, disjoint keys), "not suitable as a general replacement". Same source.
- Map of structs: `m[k].field = x` does not compile (values are not addressable). Read the struct, modify, assign back, or store pointers. https://go.dev/doc/effective_go#maps [UNVERIFIED as a verbatim rule; consequence follows from addressability in the spec]
- Map iteration order is randomized; sort keys (`slices.Sorted(maps.Keys(m))`) when order matters. https://go.dev/blog/range-functions, https://go.dev/wiki/TableDrivenTests
- Arrays are values (copied on assign/pass); prefer slices. https://go.dev/doc/effective_go#arrays

### 3.4 Defer

- Arguments to a deferred call are evaluated at the `defer` statement; deferred calls run LIFO when the surrounding function returns. https://go.dev/doc/effective_go#defer
- A deferred closure may read and modify named results (the legitimate use of named results). https://go.dev/wiki/CodeReviewComments#named-result-parameters
- `defer` in a loop runs at function exit, not iteration end; wrap the loop body in a function when resources must be released per iteration. [UNVERIFIED as quoted text; direct consequence of the spec rule above.]
- Use `defer` right after the successful acquire (`defer f.Close()`), and `defer cancel()` right after `WithCancel/WithTimeout`. https://go.dev/doc/effective_go#defer, https://go.dev/blog/context
- Overhead of `defer` is negligible except in nanosecond-scale hot paths. https://github.com/uber-go/guide/blob/master/style.md

### 3.5 Shadowing with `:=`

- `:=` in an inner block (`if`, `for`, `case`) declares a new variable that shadows the outer one; the outer variable is unchanged after the block. Classic bug: `if x { ctx, cancel := ...}` leaves outer `ctx` untouched. Declare with `var` and assign with `=`. https://google.github.io/styleguide/go/best-practices (Shadowing)
- In the same scope `:=` reuses existing variables only if at least one new non-blank variable is on the left, and the others were declared in the same block. https://go.dev/ref/spec (Short variable declarations)
- Inside goroutines, `_, err = f()` with `=` on a shared outer `err` is a data race; declare with `:=`. https://go.dev/doc/articles/race_detector

### 3.6 Closures and goroutine leaks

- A goroutine blocked forever on a channel or lock is never garbage collected and pins its stack and referenced heap. https://go.dev/blog/pipelines, https://go.dev/wiki/CodeReviewComments#goroutine-lifetimes
- Every `go` statement needs a defined exit: context cancellation, closed channel, or a `done` signal, plus something that waits for it. No fire-and-forget. https://github.com/uber-go/guide/blob/master/style.md (Don't fire-and-forget), https://go.dev/wiki/CodeReviewComments#goroutine-lifetimes
- Go 1.26 added a `goroutineleak` pprof profile (GOEXPERIMENT), and Go 1.27 made it GA at `/debug/pprof/goroutineleak`. It detects goroutines blocked on primitives unreachable from runnable goroutines. https://go.dev/doc/go1.26, https://go.dev/doc/go1.27
- Test for leaks with `go.uber.org/goleak` (UBER) or `testing/synctest` (bubble deadlock fails the test). https://github.com/uber-go/guide/blob/master/style.md, https://pkg.go.dev/testing/synctest
- Unbuffered result channels and the worker that sends on a channel nobody reads is the most common leak; use buffered size-1 channel for single-result goroutines that may be abandoned, or `select` on `ctx.Done()` for the send. https://go.dev/blog/context, https://go.dev/blog/pipelines

### 3.7 Context propagation and cancellation

- `ctx context.Context` is the first parameter, named `ctx`. Do not store it in a struct (exception: interface compatibility). No custom Context types. https://go.dev/wiki/CodeReviewComments#contexts, https://google.github.io/styleguide/go/decisions
- `context.Background()` only in `main`, `init`, top-level of incoming requests, and tests. In tests use `t.Context()` (Go 1.24). https://go.dev/blog/context, https://google.github.io/styleguide/go/decisions
- Always call the `cancel` returned by `WithCancel/WithTimeout/WithDeadline` (`defer cancel()`), including on the success path. `go vet lostcancel` checks this. https://go.dev/blog/context
- Cancellation is advisory: functions must observe `ctx.Done()` in loops and blocking selects, and return `ctx.Err()`. https://go.dev/blog/context
- Context values: only request-scoped data (trace IDs, auth identity), never optional parameters. Use an unexported key type and typed accessor helpers. https://go.dev/blog/context, https://go.dev/wiki/CodeReviewComments#contexts
- `os/signal.NotifyContext` now cancels with a cause indicating the signal (Go 1.26). https://go.dev/doc/go1.26
- Do not document that a function "returns on cancellation"; that is implied. Document only deviations. https://google.github.io/styleguide/go/best-practices

### 3.8 Generics (when to use, when not)

- Use when writing the same code repeatedly differing only by type: slice/map/channel helpers, general containers, shared `Len/Swap`-style implementations. https://go.dev/blog/when-generics
- Do not use when an interface suffices (`io.Reader` over `ReadSome[T io.Reader]`), for speed, or when per-type behavior differs. https://go.dev/blog/when-generics
- Write functions first; add type parameters when duplication appears. Prefer passing a comparison function over a `Compare` method constraint. https://go.dev/blog/when-generics
- GSD: "write code, don't design types"; no generic DSLs or error-handling frameworks. https://google.github.io/styleguide/go/decisions
- Go 1.24: generic type aliases are supported. Go 1.26: self-referential constraints (`type Adder[A Adder[A]] interface{ Add(A) A }`). Go 1.27: methods may declare their own type parameters (`func (r *Rand) N[Int intType](n Int) Int`); interface methods still cannot, and generic methods cannot implement interface methods. https://go.dev/doc/go1.24, https://go.dev/doc/go1.26, https://go.dev/doc/go1.27
- Prefer `slices`, `maps`, `cmp` stdlib generics (`slices.Sort`, `slices.Contains`, `maps.Keys`, `cmp.Compare`, `cmp.Or`) over hand-rolled helpers. https://go.dev/blog/range-functions, https://go.dev/blog/error-syntax (cmp.Or mention)

### 3.9 Range-over-func iterators (Go 1.23)

- Shapes: `func(yield func() bool)`, `func(yield func(V) bool)`, `func(yield func(K, V) bool)`; named `iter.Seq[V]`, `iter.Seq2[K, V]`. Requires `go >= 1.23`. https://go.dev/blog/range-functions
- Iterator contract: if `yield` returns false, return promptly (do clean up); never call `yield` again. https://go.dev/blog/range-functions, https://go.dev/ref/spec
- Containers expose a method returning an iterator (`func (s *Set[E]) All() iter.Seq[E]`), used as `for v := range s.All()`. https://go.dev/blog/range-functions
- Stdlib: `slices.All/Values/Backward/Chunk/Collect/Sorted`, `maps.All/Keys/Values`, `strings.Lines/SplitSeq/FieldsSeq` (1.24), `reflect.Type.Fields/Methods` (1.26). https://go.dev/blog/range-functions, https://go.dev/doc/go1.24, https://go.dev/doc/go1.26
- `iter.Pull(seq)` returns `(next, stop)`; always `defer stop()`. Use only when a push loop does not fit (merging two sequences). https://go.dev/blog/range-functions
- Use sparingly: "in many cases a loop will be clearer". Do not wrap a plain slice loop in an iterator. https://go.dev/blog/range-functions

### 3.10 Builtins: min, max, clear

- `min`/`max` (Go 1.21) work on ordered types, including strings; with floats, any NaN gives NaN, `min(-0.0, 0.0)` is `-0.0`. https://go.dev/ref/spec (recalled text), https://go.dev/blog/gofix (minmax modernizer)
- `clear(m)` deletes all map entries; `clear(s)` zeroes slice elements up to `len` (does not shrink it); nil is a no-op. https://go.dev/ref/spec (recalled text)
- Do not write `if a < b { x = a } else { x = b }` clamps; `go fix` rewrites them to `min`/`max`. https://go.dev/blog/gofix

### 3.11 log/slog

- Use `log/slog` for new code; `slog.Info("msg", "key", val, ...)` alternates key and value. Odd counts and non-string keys are bugs that `go vet` (slog analyzer) catches. https://go.dev/blog/slog
- On hot paths use `logger.LogAttrs(ctx, level, msg, slog.String(...))` to minimize allocations. https://go.dev/blog/slog
- Use `logger.With(...)` for repeated attributes (request ID); handlers format them once. https://go.dev/blog/slog
- Prefer the `...Context` methods (`InfoContext`) so handlers can extract trace IDs; cancellation does not drop the record. https://go.dev/blog/slog
- Implement `LogValuer` to control rendering and redact secrets. https://go.dev/blog/slog
- `slog.NewMultiHandler` (Go 1.26) fans out to several handlers; `slog.DiscardHandler` (Go 1.24) for tests; `slog.GroupAttrs` (1.25). https://go.dev/doc/go1.26, https://go.dev/doc/go1.24, https://go.dev/doc/go1.25
- Do not log and return the same error (section 2.3).
- Test custom handlers with `testing/slogtest`. https://go.dev/blog/slog

### 3.12 Time

- Use `time.Time` for instants and `time.Duration` for periods; never raw ints for time. When a wire format needs ints, put the unit in the field name (`IntervalMillis`) or use RFC 3339 strings. https://github.com/uber-go/guide/blob/master/style.md (Use "time")
- JSON: `omitzero` (Go 1.24) correctly omits zero `time.Time`; `omitempty` does not. https://go.dev/doc/go1.24
- Inject a clock or use `testing/synctest` (fake time) instead of `time.Sleep` in tests. https://pkg.go.dev/testing/synctest
- Since Go 1.23 timer channels are unbuffered and `Stop/Reset` do not leave stale values; the `asynctimerchan` GODEBUG is removed in 1.27. https://go.dev/doc/go1.27
- `math/rand.Seed` is a no-op as of Go 1.24; the global source is auto-seeded. https://go.dev/doc/go1.24

### 3.13 Integers, strings, bytes, runes

- Integer overflow wraps silently: unsigned modulo 2^n, signed two's complement, no panic. Check bounds explicitly before narrowing conversions or arithmetic on untrusted input. https://go.dev/ref/spec (recalled text; [UNVERIFIED] verbatim)
- Strings are read-only byte sequences, not necessarily valid UTF-8. `len(s)` is bytes; `s[i]` is a byte; `for i, r := range s` decodes runes and yields byte offsets. https://go.dev/blog/strings
- `rune` is `int32` (a code point), `byte` is `uint8`. A user-perceived character may span several runes; Go does not normalize. https://go.dev/blog/strings
- Use `%q` to print strings safely; `%x` to inspect invalid UTF-8. https://go.dev/blog/strings, https://google.github.io/styleguide/go/decisions
- Truncate or index text by rune (`[]rune(s)` or `utf8` package), not by byte, when handling user text. https://go.dev/blog/strings
- `[]byte(s)` and `string(b)` copy; avoid repeated conversions in loops and use `strings.Builder` / `bytes.Buffer`. https://github.com/uber-go/guide (string-byte-slice)
- Use `strings.Cut` instead of `Index` plus slicing. https://go.dev/blog/gofix
- Printing: `%v`, `%+v`, `%#v`, `%T`, `%q`. Inside `String()` do not `Sprintf("%s", receiver)` (infinite recursion); convert first. https://go.dev/doc/effective_go#printing
- Use `net.JoinHostPort(host, port)`, not `fmt.Sprintf("%s:%d", ...)` (breaks IPv6); vet `hostport` (1.25) flags it. https://go.dev/doc/go1.25

### 3.14 Other frequently-wrong items

- Struct embedding of `sync.Mutex`/`WaitGroup` by value then copying the outer struct: vet `copylock`. https://go.dev/wiki/CodeReviewComments#copying
- `time.After` in loops allocates timers; prefer `time.NewTimer` with `Reset`/`Stop`. [UNVERIFIED in a fetched page; behavior documented in `time` package docs.]
- `json` struct field tags and exported fields: unexported fields are ignored by `encoding/json`. https://github.com/uber-go/guide/blob/master/style.md (field tags)
- `encoding/json` is now backed by `encoding/json/v2` in Go 1.27: error message text may differ and invalid UTF-8/duplicate names are stricter in v2 APIs. Do not match on JSON error strings. Opt out with `GOEXPERIMENT=nojsonv2`. https://go.dev/doc/go1.27
- Use `slices.Clone`, `slices.Equal`, `slices.Index` etc. rather than loops; run `go fix` modernizers (section 6) to update stale idioms. https://go.dev/blog/gofix

---

## 4. Concurrency patterns

### 4.1 Choosing primitives

- Prefer the simplest design where goroutine lifetimes are obvious. "Do not communicate by sharing memory; instead, share memory by communicating." A mutex remains correct for protecting shared state such as counters or caches. https://go.dev/doc/effective_go#concurrency
- sync primitives other than `Once` and `WaitGroup` are aimed at low-level library code; channels are suggested for higher-level coordination. https://pkg.go.dev/sync
- `TryLock` "is often a sign of a deeper problem". https://pkg.go.dev/sync
- A `sync.Mutex` must not be copied after first use; zero value is unlocked. Keep as a named field and unlock with `defer`. https://pkg.go.dev/sync, https://github.com/uber-go/guide/blob/master/style.md
- Use typed atomics (`atomic.Int64`, `atomic.Bool`, `atomic.Pointer[T]`) rather than raw `sync/atomic` functions on plain ints. (UBER recommends a third-party package for this; stdlib now covers it.) https://github.com/uber-go/guide/blob/master/style.md, https://go.dev/doc/articles/race_detector
- `sync.OnceFunc/OnceValue/OnceValues` (1.21): if `f` panics, every call re-panics with the same value; `Once.Do` instead treats it as done. https://pkg.go.dev/sync
- Channel buffer size: UBER "unbuffered or one; anything else needs strong justification". Size a buffer equal to a known worker count/semaphore capacity when justified. https://github.com/uber-go/guide/blob/master/src/channel-size.md, https://go.dev/doc/effective_go#channels

### 4.2 WaitGroup (and WaitGroup.Go)

- **`wg.Go(f)` exists since Go 1.25.** It runs `f` in a new goroutine and tracks it; the docs say "Callers should prefer WaitGroup.Go" over `Add`/`Done`. `f` must not panic. https://pkg.go.dev/sync#WaitGroup, https://go.dev/doc/go1.25

```go
var wg sync.WaitGroup
for _, item := range items {
    wg.Go(func() { process(item) })   // Go 1.22+ loop var semantics make this safe
}
wg.Wait()
```

- Old pattern: `wg.Add(1)` BEFORE the `go` statement, `defer wg.Done()` first line inside. Calling `Add` inside the goroutine races with `Wait`. Rule: positive-delta `Add` while the counter is zero must happen before `Wait`. https://pkg.go.dev/sync#WaitGroup
- Go 1.25 vet `waitgroup` analyzer flags misplaced `Add`. https://go.dev/doc/go1.25
- Use only when `go.mod` says `go >= 1.25`; otherwise fall back to `Add/Done`.
- `wg.Go` does not collect errors or support cancellation; use errgroup for that.
- WaitGroup bound to a `synctest` bubble on first `Add`/`Go`; do not declare it as a package-level var in synctest tests. https://pkg.go.dev/testing/synctest

### 4.3 errgroup

- `golang.org/x/sync/errgroup` (module `golang.org/x/sync`). `g, ctx := errgroup.WithContext(ctx)`; `g.Go(func() error {...})`; `err := g.Wait()` returns the FIRST non-nil error. https://pkg.go.dev/golang.org/x/sync/errgroup
- The derived context is canceled when the first function returns non-nil OR when `Wait` returns, whichever comes first. Do not reuse that `ctx` after `Wait`. Same source.
- `g.SetLimit(n)` bounds concurrency; `Go` then blocks until a slot frees; `TryGo` is the non-blocking form. Set the limit before launching, never while goroutines are active; a limit of 0 prevents any goroutine. Same source.
- "A Group should not be reused for different tasks." Same source.
- Functions passed to `Go` must honor `ctx.Done()` or cancellation has no effect. https://pkg.go.dev/golang.org/x/sync/errgroup
- Panic semantics are not documented on the v0.24.0 page I read; check the current docs before relying on any behavior. [UNVERIFIED]
- Write per-index results into a pre-sized slice instead of sharing a mutex-protected slice. https://pkg.go.dev/golang.org/x/sync/errgroup (Parallel example)

### 4.4 Worker pools, pipelines, fan-out/fan-in

- Bounded parallelism: fixed pool of N workers reading from one input channel, instead of one goroutine per item. https://go.dev/blog/pipelines, https://go.dev/doc/effective_go#channels
- Alternative bounded pattern: `errgroup` with `SetLimit(n)`; simplest for I/O fan-out.
- Only senders close channels; the stage that owns the output `defer close(out)`. When several goroutines share an output, a coordinator goroutine does `wg.Wait(); close(out)`. https://go.dev/blog/pipelines
- Stages keep receiving until inbound is closed or senders are unblocked; use `select { case out <- v: case <-ctx.Done(): return }` for every send. https://go.dev/blog/pipelines
- Closing a channel broadcasts to any number of receivers; use `ctx.Done()` or a `done <-chan struct{}`. https://go.dev/blog/pipelines
- Sending on a closed channel panics; closing twice panics; never close from the receiver side. https://go.dev/blog/pipelines
- Fan-out count: use `runtime.GOMAXPROCS(0)`, not `NumCPU`, for CPU-bound parallelism. Since Go 1.25 GOMAXPROCS is container-aware (cgroup CPU limit) so `automaxprocs` is no longer needed. https://go.dev/doc/effective_go#parallel, https://go.dev/doc/go1.25
- Read the error channel only after draining results to avoid deadlock; use a buffered size-1 `errc` so the sender never blocks. https://go.dev/blog/pipelines

### 4.5 select and context

- Pair every blocking channel operation in a cancellable goroutine with `ctx.Done()`:

```go
select {
case out <- v:
case <-ctx.Done():
    return ctx.Err()
}
```

- `select` with `default` makes an operation non-blocking; do not busy-loop around it. https://go.dev/doc/effective_go#leaky_buffer
- When waiting on `ctx.Done()` while a worker is still running, make sure the worker can finish (buffered result channel) or you leak it. https://go.dev/blog/context
- A nil channel blocks forever in `select`; use that to disable a case. [UNVERIFIED in fetched text; language semantics.]

### 4.6 Race detector

- Run `go test -race ./...` in CI always; also run a `-race` binary under realistic load, because only executed paths are checked. https://go.dev/doc/articles/race_detector
- Costs: memory 5 to 10x, time 2 to 20x. Requires cgo and a C compiler on most platforms. Exit code 66 on a detected race; `GORACE=halt_on_error=1` stops at first. Same source.
- Common races it finds: loop variable captured (pre-1.22), shared `err` via `=`, unprotected global map, unsynchronized counters (use `atomic`), send racing with `close`. Same source.
- Exclude known-slow tests with `//go:build !race`. Same source.

### 4.7 Testing concurrent code

- Use `testing/synctest` (GA in 1.25): `synctest.Test(t, func(t *testing.T){...})` runs in a bubble with a fake clock (starts 2000-01-01 UTC) that advances only when all goroutines are durably blocked; `synctest.Wait()` waits until others are blocked; `synctest.Sleep(d)` (1.27) is `Sleep` + `Wait`. https://pkg.go.dev/testing/synctest
- Not durably blocking in a bubble: mutex waits, network I/O, syscalls, channels created outside the bubble. Use `net.Pipe` or fakes; `httptest.NewTestServer` (1.27) gives an in-memory network. https://pkg.go.dev/testing/synctest, https://go.dev/doc/go1.27
- The Go 1.24 experimental `synctest.Run` API is removed (1.26); use `synctest.Test`. https://go.dev/doc/go1.25
- Never call `t.Fatal`/`FailNow` from a goroutine other than the test goroutine; use `t.Error` and return. https://google.github.io/styleguide/go/best-practices

---

## 5. Testing

### 5.1 Table-driven tests and subtests

- Convert copy-pasted test code into a table (slice of structs) or a helper. It is a perspective, not a framework. https://go.dev/wiki/TableDrivenTests
- Wrap each case in `t.Run(tt.name, func(t *testing.T){...})` so failures identify the case and `-run 'TestX/name'` works. Subtest names are slash-joined; regexes in `-run` are unanchored per element. https://go.dev/wiki/TableDrivenTests, https://pkg.go.dev/testing
- Map-keyed tables randomize order, which proves cases are independent. Slice tables are ordered and simplest. https://go.dev/wiki/TableDrivenTests
- Parallel subtests: call `t.Parallel()` in the parent and in each `t.Run` closure. In Go >= 1.22 modules, `tt := tt` is unnecessary. https://go.dev/wiki/TableDrivenTests, https://go.dev/blog/loopvar-preview
- `t.Setenv` and `t.Chdir` cannot be used in parallel tests. https://pkg.go.dev/testing
- **[DISAGREE, mild]** naming: UBER uses `tests`/`tt` with `give`/`want` prefixes; the Go wiki and GSD use `got`/`want` in failure messages and `tc`/`tt` freely. Follow the repo.
- UBER: avoid conditional logic, mock-setup funcs, or `shouldCallFoo` fields inside tables; split into separate tests when "test depth" grows. https://github.com/uber-go/guide/blob/master/src/test-table.md
- Failure message format: `t.Errorf("Foo(%q) = %d, want %d", in, got, want)`; include function, inputs, got, then want. Use `t.Error` to keep going, `t.Fatal` only when continuing is meaningless. https://go.dev/wiki/CodeReviewComments#useful-test-failures, https://google.github.io/styleguide/go/decisions
- **[DISAGREE, mild]** order: CRC example "got != want" and GSD both put got before want. Some third-party libs (testify) put expected first. Skill default: got then want in messages.

### 5.2 testing.TB, helpers, cleanup, temp state

- Helper functions that fail the test call `t.Helper()` first and accept `testing.TB` so they serve tests and benchmarks. https://pkg.go.dev/testing, https://google.github.io/styleguide/go/best-practices
- Setup helpers may call `t.Fatal`; validation helpers should return `error` or a `cmp.Option` rather than take `*testing.T`. GBP calls assertion helpers "not idiomatic". https://google.github.io/styleguide/go/best-practices
- `t.Cleanup(f)` runs LIFO after the test and subtests finish; use it instead of `defer` in helpers. https://pkg.go.dev/testing
- `t.Context()` (Go 1.24): canceled just before Cleanup functions run; use it for goroutines/clients the test starts instead of `context.Background()`. https://pkg.go.dev/testing, https://go.dev/doc/go1.24
- `t.TempDir()` (auto-removed, unique per call), `t.Setenv`, `t.Chdir` (1.24). https://pkg.go.dev/testing
- `t.ArtifactDir()` (Go 1.26) for test output files; kept under `-outputdir` with `go test -artifacts`, else a temp dir that is deleted. `t.Attr(k, v)` (1.25) emits key/value metadata; `t.Output()` returns an `io.Writer`. https://pkg.go.dev/testing, https://go.dev/doc/go1.25, https://go.dev/doc/go1.26
- Prefer real transports with test-double servers (`httptest.NewServer`) over hand-mocked clients; keep doubles in a `<pkg>test` package. https://google.github.io/styleguide/go/best-practices
- Do not export interfaces solely to enable mocking; the consumer defines the interface. https://go.dev/wiki/CodeReviewComments#interfaces
- `go test` runs the vet `stdversion` check by default (1.27) and the `tests` analyzer (1.24, malformed test/benchmark/fuzz/example names). https://go.dev/doc/go1.27, https://go.dev/doc/go1.24

### 5.3 Golden files

- Golden files live under `testdata/` (ignored by the go tool) and are compared byte-for-byte; add an `-update` flag (`var update = flag.Bool("update", false, ...)`) to regenerate. [UNVERIFIED: convention from the Go ecosystem; not stated on a page fetched in this run. Verify against https://pkg.go.dev/cmd/go#hdr-Package_lists_and_patterns for the `testdata` rule.]
- Do not compare unstable serialized output (map iteration order, unsorted JSON from structs with maps); compare decoded values or canonicalize first. https://google.github.io/styleguide/go/decisions
- Use `t.ArtifactDir()` for diagnostic outputs, not for goldens. https://pkg.go.dev/testing

### 5.4 testify versus stdlib (contested)

- **[DISAGREE]** Google (GSD/GBP): no assertion libraries; use plain `if got != want`, `cmp.Equal`/`cmp.Diff` from `github.com/google/go-cmp`, labelled diffs (`-want +got`), and replace `reflect.DeepEqual`. https://google.github.io/styleguide/go/decisions
- Industry practice: `testify` (`assert`/`require`/`mock`/`suite`) remains the most widely used toolkit; a 2026 comparison guide still calls it the default for most projects, while a 2026 post argues it makes tests worse and cmp.Diff is the better middle ground. These are secondary sources, not Go team guidance. https://boldlygo.tech/posts/2026-04-20-testify-is-making-your-go-tests-worse/, https://lobste.rs/s/vzdoor/cult_go_test
- Skill default: for a repo without a convention, write stdlib tests plus `cmp.Diff` for structs/slices. If the repo's `go.mod` already requires testify, use it consistently (`require` for preconditions, `assert` for checks) and do not mix styles.
- With testify, remember `require.*` calls `t.FailNow` and must not be used from non-test goroutines (same rule as `t.Fatal`).
- `cmp.Equal`/`cmp.Diff` are test-only (they may panic); use `cmpopts.EquateErrors`, `cmpopts.IgnoreUnexported`, `cmpopts.SortSlices` as needed. https://google.github.io/styleguide/go/decisions

### 5.5 Fuzzing

- `func FuzzXxx(f *testing.F)` in `_test.go`; exactly one `f.Fuzz(func(t *testing.T, args...) {...})`. Allowed arg types: `string`, `[]byte`, `bool`, `int*`, `uint*`, `float*`, `rune`, `byte`. https://go.dev/doc/security/fuzz/
- Seed with `f.Add(...)` (types and order must match) and `testdata/fuzz/FuzzName/` files; seeds run under plain `go test` as regression tests. Same source.
- Run: `go test -fuzz=FuzzName -fuzztime=30s` (regex must match one target). Failing inputs are written to `testdata/fuzz/FuzzName/<hash>`; commit them. Same source.
- Targets must be fast and deterministic, with no global state; 1 second per-execution timeout. Coverage instrumentation only on amd64 and arm64. Same source.
- Good fuzz candidates: parsers, decoders, round-trip invariants (`decode(encode(x)) == x`), differential checks.

### 5.6 Benchmarks

- Use `for b.Loop() { ... }` (Go 1.24) instead of `for i := 0; i < b.N; i++`. It excludes setup/cleanup from timing, runs the function once per `-count`, and keeps args/results alive. As of Go 1.26 it no longer blocks inlining, so all `b.N` benchmarks can migrate. https://pkg.go.dev/testing, https://go.dev/doc/go1.26
- The condition must be written exactly `b.Loop()`; do not mix with `b.N`. https://pkg.go.dev/testing
- `testing.AllocsPerRun` for allocation assertions (panics under parallel tests as of 1.25). https://pkg.go.dev/testing, https://go.dev/doc/go1.25
- Compare runs with `benchstat`. [UNVERIFIED in this run; separate tool at golang.org/x/perf.]
- `go test -bench=. -benchmem -run=^$` to run benchmarks only. [Standard flags; see `go help testflag`.]

### 5.7 Static analysis and lint

- `go vet ./...` is mandatory; it also runs a subset under `go test`. Notable analyzers: `loopclosure`, `copylock`, `lostcancel`, `printf`, `tests`, `waitgroup` (1.25), `hostport` (1.25), `stdversion`. https://pkg.go.dev/cmd/go, https://go.dev/doc/go1.24, https://go.dev/doc/go1.25
- Run `staticcheck ./...` in addition to vet (150+ checks: bugs, performance, simplifications, style). https://staticcheck.dev/docs/
- Go 1.26 plan: staticcheck analyzers to be integrated into the `go fix` framework in 1.27. [Stated as "slated for Go 1.27" in the Go blog; the 1.27 release notes I read do not list it, so treat as UNVERIFIED.] https://go.dev/blog/gofix
- golangci-lint v2 config (`.golangci.yml`): `version: "2"`, `linters.default: standard`, `linters.enable: [...]`, a separate `formatters.enable: [gofmt, goimports, gofumpt]` section, `linters.exclusions.presets`. The v1 `disable-all`/`enable-all` keys are gone. https://golangci-lint.run/docs/configuration/file/
- UBER minimum linter set: `errcheck`, `goimports`, `revive`, `govet`, `staticcheck`; "lint consistently across a codebase" matters more than the exact set. https://github.com/uber-go/guide/blob/master/src/lint.md
- Do not suppress with `//nolint` without a specific linter name and reason. [UNVERIFIED as quoted guidance; golangci-lint convention.]

---

## 6. Tooling

### 6.1 Modules

- Commit `go.mod` and `go.sum`. `go get` authenticates downloads against `go.sum`/sumdb. https://go.dev/doc/modules/managing-dependencies
- `go mod init <module-path>` (use the repo path); `go mod tidy` adds missing and removes unused modules (`-v` to print removals). As of Go 1.27 `tidy` merges duplicate `require` blocks into at most two (direct and indirect). https://go.dev/doc/modules/managing-dependencies, https://go.dev/doc/go1.27
- Add/upgrade: `go get example.com/m@v1.3.4` / `@latest`; list updates with `go list -m -u all`; remove with `go get example.com/m@none` or `go mod tidy`. https://go.dev/doc/modules/managing-dependencies
- `go` directive is a MINIMUM version, enforced since 1.21; it also sets language semantics. The `toolchain` directive is a suggestion and is rewritten by the go command when it bumps `go`. `go mod init` writes `go 1.(N-1).0` since 1.26. https://go.dev/ref/mod, https://go.dev/doc/go1.26
- `GOTOOLCHAIN` controls toolchain selection (see https://go.dev/doc/toolchain; not read in detail this run).
- `replace` is honored only in the main module's `go.mod` (and `go.work`); ignored in dependencies; a `require` must still exist; local path targets need a `go.mod`; replaced modules are not checksum-authenticated. Do not commit local-path replaces. https://go.dev/ref/mod, https://go.dev/doc/modules/managing-dependencies
- `ignore ./dir` directive (1.25) hides directories from `./...` and `all`. https://go.dev/ref/mod, https://go.dev/doc/go1.25
- Vendoring: `go mod vendor`; builds auto-use `vendor/` when `go >= 1.14` and `vendor/modules.txt` is consistent; otherwise default is `-mod=readonly`. https://go.dev/ref/mod
- Private modules: `GOPRIVATE=*.corp.example.com` (also sets no-proxy/no-sumdb); default `GOPROXY=https://proxy.golang.org,direct`. https://go.dev/doc/modules/managing-dependencies
- Major version v2+ requires the `/v2` suffix in the module path. [Standard module rule; not re-verified on a page this run.]

### 6.2 Workspaces (go.work)

- Commands: `go work init`, `go work use [-r] ./mod`, `go work edit`, `go work sync`, `go work vendor`. https://pkg.go.dev/cmd/go
- **Generally do not commit `go.work`.** It can shadow a developer's parent workspace and CI would test wrong dependency versions; it has no effect when your module is consumed as a dependency. Exception: a tightly coupled multi-module repo developed only together. https://go.dev/ref/mod
- `GOWORK=off` disables workspace mode (use in CI to verify each module builds alone); `go env GOWORK` shows the active file. https://go.dev/ref/mod
- `go mod tidy`, `go get`, `go mod edit` always act on a single main module, not the workspace. Same source.
- Prefer `go.work` over `replace => ../x` for local multi-module development. https://go.dev/doc/modules/managing-dependencies

### 6.3 tool directive (Go 1.24+)

- Replaces the old `tools.go` blank-import hack. Add with `go get -tool golang.org/x/tools/cmd/stringer`; run with `go tool stringer` (or full path on name collision); list with bare `go tool`. https://go.dev/doc/modules/managing-dependencies, https://go.dev/ref/mod
- Upgrade all tools: `go get tool`; install all to `$GOBIN`: `go install tool`; remove: `go get -tool pkg@none`. Same sources.
- A `tool` line needs a matching `require` (`go mod tidy` fixes). Tools take part in MVS. Same sources.
- Tool binaries run via `go run`/`go tool` are cached in the build cache (1.24). https://go.dev/doc/go1.24
- Use it for `stringer`, `mockgen`, `sqlc`, linters pinned per repo, so `go generate` is reproducible.

### 6.4 go generate

- Directive: `//go:generate command args...` with no leading space and no space after `//`. Never run automatically by `go build`/`go test`; run `go generate ./...` explicitly and commit generated output. https://pkg.go.dev/cmd/go
- Generated files must contain `// Code generated ... DO NOT EDIT.` before any non-comment text so linters and `go fix` skip them. Same source.
- Env available to the command: `$GOFILE`, `$GOLINE`, `$GOPACKAGE`, `$GOOS`, `$GOARCH`. `-run`/`-skip` select directives. Same source.
- Pair with `go tool <name>` in the directive for pinned tools: `//go:generate go tool stringer -type=Kind`.

### 6.5 Formatting and fixing

- `gofmt -s` simplifies composite literals; `goimports` also manages import lines. Go 1.27 changed gofmt alignment logic (minor whitespace diffs possible). https://go.dev/doc/go1.27, https://google.github.io/styleguide/go/decisions
- **`go fix` (Go 1.26+) is now a modernizer suite**, not the old obsolete fixers. Run `go fix -diff ./...` then `go fix ./...` from a clean git tree; fixes respect the file's effective Go version, skip generated files, and are safe to apply. Examples: `minmax`, `rangeint`, `stringscut`, `forvar` (removes `x := x`), `newexpr`, `any`, `mapsloop`, `stringsbuilder`; 1.27 adds `atomictypes`, `embedlit`, `slicesbackward`, `unsafefuncs`, and renames `waitgroup` to `waitgroupgo`. Run again after to catch cascading fixes. https://go.dev/blog/gofix, https://go.dev/doc/go1.26, https://go.dev/doc/go1.27
- Rationale given by the Go team: LLMs trained on old corpora keep emitting stale idioms; run `go fix` after agent-generated code. https://go.dev/blog/gofix
- `//go:fix inline` directives let API authors declare source-level inline rewrites. https://go.dev/blog/gofix
- `go vet -fix` and `-diff` apply/preview first suggested fixes. https://pkg.go.dev/cmd/go

### 6.6 govulncheck

- Install: `go install golang.org/x/vuln/cmd/govulncheck@latest`; run `govulncheck ./...`. It reports vulnerabilities in dependencies and stdlib, distinguishing reachable ("affected", with call stacks) from imported-but-not-called (informational). https://go.dev/doc/tutorial/govulncheck
- Remediate by upgrading to the highest "Fixed in" version (`go get module@version`) or removing the call. Same source.
- Binary mode, `-format sarif`, and exit codes (non-zero when reachable vulns exist) are documented on https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck; I did not fetch that page, so treat exit-code specifics as [UNVERIFIED].
- Keep the toolchain current: stdlib vulns are fixed only by upgrading Go (e.g. 1.27.2 / 1.26.9 on 2026-10-08). https://go.dev/doc/devel/release
- Go 1.24+: `-buildvcs`-embedded versions and `go version -m -json` (1.25) help audit binaries. https://go.dev/doc/go1.24, https://go.dev/doc/go1.25

### 6.7 Build constraints

- Use `//go:build expr` (with `&&`, `||`, `!`, parentheses) before the package clause, followed by a blank line; at most one per file. `// +build` lines are obsolete. https://pkg.go.dev/cmd/go
- Tags satisfied: GOOS, GOARCH, `unix`, `cgo`, `gc`, `go1.N` release tags, plus `-tags`. Vet `buildtag` rejects point-release constraints like `go1.23.1` (1.24). https://pkg.go.dev/cmd/go, https://go.dev/doc/go1.24
- A `go1.N` build tag in a file also sets that file's language version (used for loopvar/iterators gating). https://go.dev/blog/loopvar-preview, https://go.dev/blog/range-functions
- Filename suffixes `_linux.go`, `_amd64.go`, `_test.go` act as implicit constraints. [Standard go/build rule; not re-fetched.]

### 6.8 GOFLAGS, GOEXPERIMENT, GODEBUG

- `GOFLAGS` is "a space-separated list of -flag=value settings to apply to go commands by default", applied only if the command recognizes the flag; entries must be standalone flags, values cannot contain spaces; command-line flags override it. Persist per-user with `go env -w GOFLAGS=-mod=readonly`. https://pkg.go.dev/cmd/go (Environment variables)
- Typical CI: `GOFLAGS=-mod=readonly` (fail if `go.mod` would change) and `-race` in test steps. Repo default is already readonly unless `vendor/` is present. https://go.dev/ref/mod
- `GOEXPERIMENT` is for toolchain development; "use beyond that purpose is unsupported", and the set changes arbitrarily. Do not set it in project builds. https://pkg.go.dev/cmd/go (Environment variables)
- `godebug` directive in go.mod (or `//go:debug`) tunes compatibility settings for the main module; GODEBUG settings removed in 1.27 fail the build if set to non-final values. https://go.dev/ref/mod, https://go.dev/doc/go1.27
- `-buildvcs=false` to omit VCS stamping; `go build -json` / `go test -json` for tooling. https://go.dev/doc/go1.24

### 6.9 Runtime and platform notes that affect code

- Green Tea GC is default in Go 1.26 (10 to 40 percent less GC overhead); opt-out `GOEXPERIMENT=nogreenteagc` is removed in 1.27. https://go.dev/doc/go1.26
- Container-aware `GOMAXPROCS` since Go 1.25 (cgroup CPU limit, dynamic updates); do not import `automaxprocs`. `runtime.SetDefaultGOMAXPROCS()` restores default. https://go.dev/doc/go1.25
- Flight recorder (`runtime/trace.FlightRecorder`, 1.25) for in-memory trace ring buffer. https://go.dev/doc/go1.25
- `os.Root` / `os.OpenRoot` (1.24) for traversal-safe file access under a directory; extended in 1.25. Use it for user-supplied paths. https://go.dev/doc/go1.24, https://go.dev/doc/go1.25
- `runtime.AddCleanup` (1.24) replaces `SetFinalizer`; `weak` package for weak pointers. https://go.dev/doc/go1.24
- `crypto/rand.Read` never fails (crashes instead); use `rand.Text` (1.24) for tokens. https://go.dev/doc/go1.24
- `net/url.Parse` now rejects malformed host colons (1.26). https://go.dev/doc/go1.26

---

## 7. Anti-patterns checklist for generated code

Concise, checkable list distilled from the above (each item maps to a rule with a source above):

1. No `?`/`try` syntax; explicit `if err != nil` (2.7).
2. Never discard errors with `_` without a comment (2.1).
3. No `return typedNilPtr` from an `error` function (2.4).
4. Wrap with `%w` at the end of the message; no "failed to" stutter; no log-and-return (2.3).
5. Use `errors.Is` / `errors.AsType` (1.26+) / `errors.As`; never `==` on wrapped errors or string matching (2.1).
6. Pass `ctx` first, never store it; `defer cancel()` (3.7).
7. Every goroutine has an owner, an exit path, and a waiter; use `wg.Go` (>= 1.25) or `errgroup` (4.2, 4.3).
8. Do not capture loop variables defensively when `go >= 1.22`; do check the `go` line first (3.1).
9. Do not mutate or return internal slices/maps without copying (1.6).
10. No `panic` for control flow; no blanket `recover` (2.5).
11. Tests: table plus `t.Run`; `got`/`want` order; `t.Helper()`; `t.Cleanup`; `t.Context()`; `b.Loop()` (5.1, 5.2, 5.6).
12. Use `slog`, `slices`, `maps`, `cmp`, `min`/`max`, `for range n`; run `go fix ./...` (3.8 to 3.11, 6.5).
13. Pin tools via `go get -tool` and `go tool`, not `tools.go` (6.3).
14. Run `gofmt`, `go vet`, `staticcheck` or `golangci-lint` (v2 config), `go test -race`, `govulncheck ./...` before finishing (5.7, 4.6, 6.6).
15. Do not commit `go.work` or local-path `replace` directives (6.1, 6.2).

---

## 8. Source index

- Release history: https://go.dev/doc/devel/release
- Release notes: https://go.dev/doc/go1.24 , https://go.dev/doc/go1.25 , https://go.dev/doc/go1.26 , https://go.dev/doc/go1.27
- Effective Go: https://go.dev/doc/effective_go
- Code Review Comments: https://go.dev/wiki/CodeReviewComments
- Google style: https://google.github.io/styleguide/go/decisions , https://google.github.io/styleguide/go/best-practices
- Uber guide: https://github.com/uber-go/guide/blob/master/style.md and per-section files under https://github.com/uber-go/guide/tree/master/src
- Errors: https://go.dev/blog/go1.13-errors , https://go.dev/blog/error-handling-and-go , https://go.dev/blog/error-syntax , https://pkg.go.dev/errors
- FAQ: https://go.dev/doc/faq
- Language/blogs: https://go.dev/blog/loopvar-preview , https://go.dev/blog/when-generics , https://go.dev/blog/range-functions , https://go.dev/blog/slog , https://go.dev/blog/slices-intro , https://go.dev/blog/strings , https://go.dev/blog/context , https://go.dev/blog/pipelines , https://go.dev/blog/gofix , https://go.dev/ref/spec
- Concurrency: https://pkg.go.dev/sync , https://pkg.go.dev/golang.org/x/sync/errgroup , https://pkg.go.dev/testing/synctest , https://go.dev/doc/articles/race_detector
- Testing: https://go.dev/wiki/TableDrivenTests , https://go.dev/doc/security/fuzz/ , https://pkg.go.dev/testing
- Tooling: https://go.dev/ref/mod , https://go.dev/doc/modules/managing-dependencies , https://pkg.go.dev/cmd/go , https://go.dev/doc/tutorial/govulncheck , https://staticcheck.dev/docs/ , https://golangci-lint.run/docs/configuration/file/
- Secondary (testify debate): https://boldlygo.tech/posts/2026-04-20-testify-is-making-your-go-tests-worse/ , https://lobste.rs/s/vzdoor/cult_go_test

## 9. Known gaps in this research

- Spec text for overflow, defer, min/max, clear, and range-over-int was quoted by the fetch tool from the spec's truncated content; those lines are marked "recalled" and should be re-verified before being treated as verbatim.
- Not fetched: `fmt` docs (multiple `%w`), `go.dev/doc/toolchain` (GOTOOLCHAIN), govulncheck command docs (exit codes, binary mode), `benchstat`, golden-file conventions, errgroup panic semantics on the latest version, Uber performance/functional-options section bodies.
- The Go FAQ on generic methods predates Go 1.27 generic methods.
