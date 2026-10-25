# datastar-lint

A multi-language linter for [Datastar](https://data-star.dev). Validates HTML attributes AND backend SDK calls across Go, Python, and TypeScript projects.

Datastar's contract lives in `data-*` attributes on HTML and `PatchElements`/`PatchSignals` calls on the backend. This tool catches typos, missing selectors, and misconfigurations at build time — not in the browser console.

> **Version compatibility**: Verified against Datastar **v1.0.4** (run `datastar-lint --version` for the exact value — it is the authoritative source). The rules check stable DOM-level and Datastar-API-level patterns, and the full rule set is covered by automated tests that run in CI on every push. Minor/patch releases of Datastar (Y.Z) should not affect correctness.

## Contents

- [Why datastar-lint](#why-datastar-lint)
- [Install](#install)
- [Usage](#usage)
- [Available analyzers](#available-analyzers)
- [Update flags](#update-flags)
- [Suppressing a finding](#suppressing-a-finding)
- [What it catches](#what-it-catches)
- [Enabling analyzers](#enabling-analyzers)
- [Architecture](#architecture)
- [Go SDK API shape (read before adding a Go rule)](#go-sdk-api-shape)
- [Where to run it](#where-to-run-it)
- [License](#license)

## Why datastar-lint

These mistakes compile and build cleanly — they only fail at runtime, in the
browser or over the SSE stream:

- **HTML attribute typos** (`data-on-clik`) — the browser silently ignores
  unknown `data-*` attributes; no build error.
- **Missing SDK selectors** (`SSE.patch_elements()` without `selector=`) — the
  Datastar client throws `PatchElementsNoTargetsFound` at runtime; the SDK does
  not validate this.
- **Go `PatchElementf` format mismatch** — `go build` does not check `fmt` verbs
  against arguments.
- **`MarshalAndPatchSignals(nil)`** — compiles fine, but sends `"null"` on the
  wire, wiping all signals.

datastar-lint shifts these from "caught in production" to "caught in CI".

## Install

```bash
go install github.com/calionauta/datastar-lint@latest
```

Requires Go 1.26+.

All analyzers (HTML, Go, Python, TypeScript) are included in the binary; only
HTML runs by default — enable the others with `--analyzers` (see
[Enabling analyzers](#enabling-analyzers)).

## Usage

```bash
# HTML/Templ linting (default analyzer)
datastar-lint -r ./web/

# Go backend linting (opt-in)
datastar-lint -r --analyzers go ./api/

# HTML + Go + cross-reference checks
datastar-lint -r --analyzers html,go ./project/

# Python linting (opt-in)
datastar-lint -r --analyzers python ./src/

# TypeScript linting (opt-in)
datastar-lint -r --analyzers typescript ./src/

# Strict mode: Pro-only attributes become errors
datastar-lint -r -s ./web/

# With custom attribute config
datastar-lint -r --config .datastar-lint.yaml ./src/
```

Run `datastar-lint --version` to print the linter version and the Datastar release it was verified against. Exit code is `0` on clean, `1` on issues.

`--format` selects the output: `text` (default), `json`, or `sarif` for GitHub code
scanning — with SARIF the findings become PR annotations and Security-tab alerts
instead of lines in a CI log.

### Available analyzers

| Analyzer | Flag name | Extensions | Default? | Language |
|----------|-----------|------------|----------|----------|
| HTML | `html` | `.html`, `.htm`, `.templ`, `.tsx`, `.jsx`, `.ts`, `.js` | yes | All — Templ (Go), JSX/TSX (TS/JS), plain HTML |
| Go | `go` | `.go` | opt-in | Go (stdlib `go/parser`) |
| Python | `python` | `.py` | opt-in | Python |
| TypeScript | `typescript` | `.ts`, `.tsx` | opt-in | TypeScript/JavaScript |
| Templ | `templ` | `.templ` | opt-in | Go template expressions leaking into markup JS/attrs |

> **Two layers in a Go project:** `.templ` is Go's templating format. The
> **HTML** analyzer lints the `data-*` attributes `.templ` emits (the markup
> layer), while the separate **Go** analyzer lints backend SDK calls
> (`PatchElements` selectors, etc.) in `.go` files. Likewise `.ts`/`.tsx` are
> linted by **both** the HTML analyzer (markup) and the TypeScript analyzer (SDK
> selectors) when enabled. All analyzers ship in the binary — only HTML runs by
> default, so `data-*` mistakes in TSX/JSX are caught out of the box; enable
> `go`/`typescript` with `--analyzers` for the SDK-layer checks.

### Update flags

- **`--check-update`** — Check if a newer version is available on GitHub without running lint.
- **`--update`** — Download and atomically replace the current binary with the latest release. Requires write access to the executable directory.

On every run, `datastar-lint` silently checks for a newer version (with a 2 second timeout). If found, a notice is printed to stderr before the lint output. Use `--update` to apply it.

### Configuration file

To suppress warnings for intentional custom `data-*` attributes (e.g., `data-tool`, `data-doc-id`), create a `.datastar-lint.yaml` in your project root:

```yaml
# .datastar-lint.yaml
attributes:
  allowed:
    - data-tool
    - data-doc-id
    - data-app-state

# Optional: codes this project never allows to be silenced.
# The decision lives in this reviewed file, not in a comment next to the code.
suppressions:
  forbidden:
    - CLIENT_BOUND_SIGNAL_WRITTEN
```

The linter auto-discovers the file by walking up from the target directory. Pass `--config <path>` to use an explicit path. The built-in `knownAttrs` remain the default — the config only *adds* to them.

> The UNKNOWN_ATTR warning says "If this is an intentional custom attribute (not Datastar), add it to .datastar-lint.yaml". This gives LLMs and humans a clear action, but the conditional ("if intentional") makes it explicit that typos and mistakes should be corrected, not silenced. UNKNOWN_ATTR_TYPO (detected misspelling) never mentions the config file — it only suggests the correction, and it cannot be suppressed at all.

## Suppressing a finding

One line, one code, one reason:

```go
// datastar-lint:ignore CLIENT_BOUND_SIGNAL_WRITTEN — page-render seed: ?q= initialises the box once
SearchQuery: query,
```

The directive silences at most the finding on its own line, the line it annotates,
and the line directly above. No ranges, no globs, no file-wide form. Every applied
suppression is printed in the run summary with its reason.

It is deliberately harder to write than the check it silences, because the
dangerous failure is not that suppression fails to work — it is that it works too
easily, and the next agent silences the check instead of fixing the write. Six
rules, each reported as its own finding:

| What you wrote | What you get |
|---|---|
| `// datastar-lint:ignore` | `SUPPRESS_UNSPECIFIED` — a code is required |
| `... CLIENT_BOUND_SIGNAL_WRITTEN` (no reason) | `SUPPRESS_NO_REASON` |
| `... CLIENT_BOUND_SIGNAL_WRITEN` (typo) | `SUPPRESS_UNKNOWN_CODE` |
| a code in the blocklist (below) | `SUPPRESS_BLOCKED` |
| a directive that matched no finding | `SUPPRESS_UNUSED` — a stale suppression is a lie about the tree |
| a finding 2+ lines below the directive | not silenced, and reported `SUPPRESS_UNUSED` |

A directive inside a Go string literal is text, not a directive.

### What cannot be suppressed

Parse failures and unreadable files (`PARSE_ERROR`, `GO_PARSE_ERROR`, `FILE_OPEN` — the lint did not actually run on that file), and the checks whose false-negative is silent: `UNKNOWN_ATTR_TYPO`, `INVALID_MODIFIER`, `PATCH_ELEMENTS_NO_SELECTOR`, `PATCH_ELEMENTS_NO_ID`, `PATCH_SELECTOR_EMPTY`, `SDK_FUNC_IN_JS`. Add your own with `suppressions.forbidden` in the config.

Note what is *not* on that list: `CLIENT_BOUND_SIGNAL_WRITTEN`. It has a real,
correct exception (the page-render seed), and a check with no escape hatch gets
disabled wholesale the first time it is right and inconvenient — the worst
outcome. It is ERROR-severity with a required, reported reason instead.

### Configuration for the project, not the agent

`suppressions.forbidden` exists because a source comment and a config file are not
the same act. An agent editing a file it was already asked to change can slip a
suppression into that diff; editing `.datastar-lint.yaml` is a separate, visible
change that a reviewer sees on its own. When a code may never be silenced at your
project, say so once in the config rather than trusting every future edit.

## What it catches

### HTML attributes & typos (all languages)

- **`UNKNOWN_ATTR` / `UNKNOWN_ATTR_TYPO`** — Flags `data-foobar`, `data-on-clik` (typo), or any `data-*` attribute not in the Datastar spec.
- **`KEY_NOT_ALLOWED`** — Attributes that don't accept sub-keys (`:signalName` syntax) reject them.
- **`INVALID_MODIFIER`** — Unknown modifiers on Datastar attributes.
- **`PRO_ATTR`** — Datastar Pro-only attributes. Warning by default, error in strict mode.
- **`FOREIGN_ATTR`** — Alpine.js/Vue.js leftovers (`x-data`, `v-if`, `@click`).
- **`PATCH_ELEMENTS_NO_ID`** — Element with `data-on:load` (SSE) or `data-on-signal-patch` but no `id`. The JS client needs an `#id` anchor to morph the fragment.
- **`ON_LOAD_NO_EVENT`** — `data-on:load` on an element that never fires the native `load` event (`<div>`, `<span>`, etc.). The native `load` event only fires on `<body>`, `<img>`, `<script>`, `<link>`, `<video>`, and other elements with external resource loading. On all other elements the callback silently never executes. Use `data-init` instead, or add the `__window` modifier. Severity: error.
- **`ON_INIT_NO_EVENT`** — `data-on:init` used anywhere — there is no `init` event in the browser DOM spec. Use `data-init` (without colon) instead, which runs immediately when the element is processed by Datastar. Severity: error.
- **`ON_DOM_CONTENT_LOADED_NO_EVENT`** — `data-on:DOMContentLoaded` on any element — the `DOMContentLoaded` event fires on `document`, not on individual elements, so the callback silently never runs. Use `data-init` instead, or add the `__document` modifier. Severity: error.
- **`ON_RESIZE_NO_EVENT`** — `data-on:resize` on any element — the native `resize` event only fires on `window`, not on individual elements. For element-level resize observation, use `ResizeObserver` instead. Or add the `__window` modifier. Severity: error.
- **`ON_HASHCHANGE_NO_EVENT`** — `data-on:hashchange` on any element — the `hashchange` event only fires on `window`, not on individual elements. Use `data-on:hashchange__window` instead. Severity: error.

### Go SDK checks

- **`PATCH_ELEMENTS_NO_SELECTOR`** — `PatchElements()` / `PatchElementTempl()` / `PatchElementGostar()` / `RemoveElement()` called without `WithSelector`/`WithSelectorID`, or with an omitted selector argument. Without a CSS selector the JS client throws `PatchElementsNoTargetsFound` and the update silently never lands. Severity: **error** (it fails a build). `RemoveElementByID()` and `RemoveElementf()` are exempt: the former takes the bare id and prefixes `#` itself, the latter carries its selector in the format string. Both take no options, so neither can be missing one.
- **`PATCH_SELECTOR_EMPTY`** — `WithSelector("")` or `WithSelectorID("")` — empty string is silently dropped by the SDK. Severity: **error**.
- **`MERGE_SIGNALS_NIL`** — `MarshalAndPatchSignals(nil)` or `MarshalAndPatchSignalsIfMissing(nil)` produces `"null"` on the wire, overwriting all client signals. (The `IfMissing` variant delegates to the base method, so it inherits the defect.) Severity: hint.
- **`PATCH_ELEMENTF_FORMAT`** — `PatchElementf()` format string has `%` verbs that may not match the number of value arguments. Severity: hint.
- **`GO_PARSE_ERROR`** — The Go file could not be parsed. Severity: error.

### Python SDK checks

- **`PY_PATCH_NO_SELECTOR`** — `SSE.patch_elements(...)` called without `selector=` keyword. Severity: warning.
- **`PY_PATCH_EMPTY_SELECTOR`** — `SSE.patch_elements(...)` with `selector=""` or `selector=''`. Severity: warning.
- **`PY_REMOVE_NO_SELECTOR`** — `SSE.remove_elements(...)` called with empty or missing selector argument. Severity: warning.

### TypeScript SDK checks

- **`TS_PATCH_NO_SELECTOR`** — `stream.patchElements(...)` or `sse.patchElements(...)` called without `selector:` in options. Severity: warning.
- **`TS_PATCH_EMPTY_SELECTOR`** — `selector: ""` or `selector: ''`. Severity: warning.
- **`TS_REMOVE_NO_SELECTOR`** — `stream.removeElements(...)` / `sse.removeElements(...)` called with empty or missing selector argument. Severity: warning.

### Cross-reference checks (when both `go` and `html` analyzers are active)

- **`CROSSREF_ORPHAN_SELECTOR`** — A Go `WithSelector("#id")` references an element id that doesn't exist in any scanned `.templ`/`.html`/`.tsx`/`.jsx`/`.ts`/`.js` file. Severity: warning.

  A selector passed as a **string constant** is resolved too, so
  `const selChat = "#chat-messages"` + `WithSelector(selChat)` is checked
  against the templates like a literal would be. The finding names the
  constant (`sdk.WithSelector(selChat)`), which is what you need to find the
  line. A constant whose value is computed rather than literal is out of
  scope and stays unreported.
- **`CLIENT_BOUND_SIGNAL_NO_OMITEMPTY`** — A struct field whose JSON name matches a `data-bind` signal has a `json:"x"` tag **without** `omitempty`. The zero value still marshals (`"x":""`), so **every** merge that carries the struct overwrites the bound input — even after someone removes the field from the merge call, which is why this one is an error. Severity: error.
- **`CLIENT_BOUND_SIGNAL_WRITTEN`** — the server writes a signal that some input binds two-way with `data-bind`, on **any** of three doors: a Go merge, a `data-signals:key` attribute, or a `data-signals={obj}` attribute. Datastar writes an incoming signal value straight back into the element (`bind.ts` wraps `syncElement` in an effect), and the server's copy always lags what the user is typing, so a background write (stream handshake, record-event re-list, any patch-signals) can overwrite live input; the debounced request then reads the clobbered value and the feature silently does nothing. Severity: error. The fix is usually to stop writing the field at all — see the `json:"-"` + `value=` shape below, which needs no suppression. A single deliberately correct write (a page-render seed) can be silenced with a reason (`// datastar-lint:ignore CLIENT_BOUND_SIGNAL_WRITTEN — <why>`), though `suppressions.forbidden` lets a project reject even that.

```go
// .templ:  <input data-bind="searchQuery" />   <- the input owns this

// flagged (error): the tag always marshals, so removing the field from the
// merge call below would NOT fix the bug
type Signals struct {
	SearchQuery string `json:"searchQuery"`
}

// flagged (error): the server writes a signal the input owns
merge(map[string]any{"searchQuery": query})

// clean: absent now means "leave the client's value alone"
type Signals struct {
	SearchQuery string `json:"searchQuery,omitempty"`
}

// clean: the field never reaches the wire at all — the strongest statement
// that it cannot clobber the input, and the idiomatic way to keep a
// server-computed seed OUT of data-signals while still rendering it into
// the input's value=
type Signals struct {
	SearchQuery string `json:"-"`
}
```

`json:"-"` is an explicit exclusion, not a missing tag: the field is resolved as
"no wire name" and never falls back to a name match. `json:",omitempty"` and
`json:""` (empty name) mean "use the Go field name", exactly as `encoding/json`
reads them.

Four write forms are recognized, because a project uses all four: a map key
(`map[string]any{"x": v}`), a map key written as a **string constant**
(`signalX: v` — the dominant form in a real tree), a struct-literal field
(`Signals{X: v}`, resolved through the field->JSON tag), and a field assignment
(`s.X = v`). A dotted
binding (`data-bind="user.name"`) also arms its root, since writing the whole
`user` object replaces the value the input reads.

**The mark-up side is checked too.** A server-rendered signals attribute
re-applies on every morph, so `data-signals:key` (upstream `mergePaths`) and
`data-signals={obj}` (`mergePatch`) clobber live input exactly like a Go merge.
Both are reported when the key — or a path root — is bound. Both honour the
`ifmissing` modifier (`data-signals:key__ifmissing`, `data-signals__ifmissing`),
which upstream turns into `{ifMissing: true}`. `data-signals={ expr }` is
deliberately left alone: a dynamically-built object's keys are not statically
knowable, and the Go-side rule catches the struct that feeds the expression.

**The documented escape hatch is honoured.** `onlyIfMissing` makes the client
apply a signal only when one of that name does not already exist, so it cannot
overwrite a bound input — that is what the SDK's
`MarshalAndPatchSignalsIfMissing` and `WithOnlyIfMissing(true)` are for, and what
the HTML-side `ifmissing` modifier maps to. A write routed through any of them is
not reported:

```go
// clean: the client refuses to overwrite an existing signal
sse.MarshalAndPatchSignalsIfMissing(map[string]any{"searchQuery": q})
sse.MarshalAndPatchSignals(map[string]any{"searchQuery": q}, WithOnlyIfMissing(true))

// still flagged: an explicit opt-out
sse.MarshalAndPatchSignals(map[string]any{"searchQuery": q}, WithOnlyIfMissing(false))
```

The escape hatch covers a **write**, not the tag: a struct field without
`omitempty` stays an error, because the same struct is merged on other paths
that are not `onlyIfMissing`.

Upstream context: Datastar documents the general model — *"the backend is the
source of truth"* ([the tao of Datastar](https://data-star.dev/guide/the_tao_of_datastar)) —
but not this collision. The behavior is upstream-recognized as a bug class:
[#1228](https://github.com/starfederation/datastar/issues/1228) (*"corrupts the
date when the user types over a date that is already in the box"*, fixed in
v1.0.4) and [#783](https://github.com/starfederation/datastar/issues/783)
(*"Input value gets lost"*) are the same write-back. A bound input is the
exception to the source-of-truth rule, which is exactly why it is worth
linting.

A write that is genuinely intended (clearing an input once its submit has
succeeded) is the case for a line suppression with a reason, not for a lower
severity: it is an error, so a run with `-only-errors` fails until the write is
gone, routed through `onlyIfMissing`, or named as an exception. The idiom above
(`json:"-"` + `value=`) removes the need for all three.

### Templ-expression leakage (opt-in `templ` analyzer)

The templ compiler never interpolates `{...}` inside `<script>` bodies or
quoted attribute strings — both render literally, with no error. Datastar
attributes are unaffected (bare identifiers without braces are valid
expressions and never flagged), and `templ script` declarations (real
Go+JS) are skipped. Call-shaped `{ f(x) }` is ambiguous with real JS
blocks and left to human eyes; only bare identifier/path forms flag.

- **`TEMPL_EXPR_IN_SCRIPT`** — `{ name }` inside a markup-embedded
  `<script>` (external `src` files excluded). Severity: error.
- **`TEMPL_EXPR_IN_ATTR`** — `{ name }` inside a quoted attribute value.
  Drop the quotes (`attr={ expr }`) or move the value to a `data-*`
  attribute. Severity: error.

### Forms

- **`FORM_SUBMIT_MISSING`** — `<form>` with `data-bind` but no `data-on:submit`.
- **`FORM_SUBMIT_NO_PREVENT`** — Submit action without `__prevent` modifier.
- **`FORM_MISSING_ENCTYPE`** — File input + `contentType: 'form'` without `enctype`.
- **`BIND_MISSING_NAME`** — Form element with `data-bind` but no `name`.
- **`BIND_NO_NAME`** — `data-bind` without signal name.
- **`BIND_NON_FORM`** — `data-bind` on non-form element without `__prop`.
- **`INDICATOR_AFTER_INIT`** — `data-indicator` after `data-init` on same element.

### Infrastructure

- **`PARSE_ERROR`** / **`FILE_OPEN`** — File could not be opened or parsed.

## Enabling analyzers

All four analyzers are compiled into every binary. Only **HTML** runs by
default; enable the others with `--analyzers` (comma-separated):

```bash
datastar-lint -r ./                                       # HTML only (default)
datastar-lint -r --analyzers html,go ./                   # + Go SDK checks
datastar-lint -r --analyzers html,typescript ./src/       # + TS SDK checks
datastar-lint -r --analyzers html,go,python,typescript ./ # everything
```

## Go SDK API shape

**The Datastar Go SDK is method-only.** Every patch call is a method on
`*datastar.ServerSentEventGenerator`, obtained from `datastar.NewSSE(w, r)`:

```go
sse := datastar.NewSSE(w, r)
sse.PatchElements(`<div id="x">x</div>`, datastar.WithSelector("#x"))
sse.RemoveElement("#temporary")
sse.MarshalAndPatchSignals(map[string]any{"k": "v"})
```

There is **no** package-level `datastar.PatchElements(sse, ...)` — and there has
not been one in any 1.x release. Writing the package form does not compile:

```
./x.go:11:15: undefined: datastar.PatchElements
```

Only the *option constructors* are package-level (`datastar.WithSelector`,
`datastar.WithSelectorID`, `datastar.WithModeAppend`, …). The Go analyzer
therefore matches the method form, gated on the file importing the SDK. Three
of these methods take no options at all, so they cannot be missing a selector:
`PatchElementf`, `RemoveElementf`, `RemoveElementByID`.

## Architecture

datastar-lint uses a plugin-style `Analyzer` interface:

```go
type Analyzer interface {
    Name() string
    FileExtensions() []string
    Lint(path string, cfg config) []lintResult
}
```

Each analyzer registers itself via `init()` and `RegisterAnalyzer()`. The `run()` function collects files per-analyzer and dispatches linting. When both `go` and `html` analyzers run, a cross-reference step automatically checks for orphan selectors.

To add a new language, create a file implementing `Analyzer`, call `RegisterAnalyzer()` in `init()`. (Analyzers currently ship in every build; see [Enabling analyzers](#enabling-analyzers).)

## Where to run it

datastar-lint catches mistakes that language compilers and browsers ignore
(see [Why datastar-lint](#why-datastar-lint)). Run it wherever you produce or
change Datastar output.

### Analyzer enablement

Only the **HTML** analyzer runs by default. All analyzers ship in the binary,
so enable the others explicitly with `--analyzers` (comma-separated):

| Analyzer | Runs by default? | To enable |
|----------|------------------|-----------|
| `html` | Yes | — (always on) |
| `go` | No | `--analyzers ...,go` |
| `python` | No | `--analyzers ...,python` |
| `typescript` | No | `--analyzers ...,typescript` |

### When to run

| When | Command | Why |
|------|---------|-----|
| **After `templ generate`** | `templ generate && datastar-lint -r --analyzers html,go ./features/` | Lint generated `.templ`/`.html` attributes **and** Go SDK calls |
| **Before commit** | `datastar-lint -r --analyzers html,go ./` | Gate local changes across HTML attributes + Go SDK |
| **In CI** | `datastar-lint -r --analyzers html,go ./` | PR gate; cross-reference runs automatically when `go` + `html` are both active |

> **TypeScript / JavaScript:** these have no template code-generation step like
> `templ generate` (Go), so there is no "after generate" trigger. Their `data-*`
> markup (in `.tsx`/`.jsx`/`.ts`/`.js`) is linted by the default `html` analyzer,
> and their backend SDK calls by the `typescript` analyzer. Lint both with:
> `datastar-lint -r --analyzers html,typescript ./src`
> (Python SDK calls: add `python` to `--analyzers`.)
> TypeScript's own `tsc`/`vite` compile step is unrelated to Datastar codegen.

## License

MIT.

## Changelog

See [CHANGELOG.md](CHANGELOG.md). Note that `PATCH_ELEMENTS_NO_SELECTOR` and
`PATCH_SELECTOR_EMPTY` became **errors** in v0.12.0 — they now fail a build.
