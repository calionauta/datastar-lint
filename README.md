# datastar-lint

A multi-language linter for [Datastar](https://data-star.dev). Validates HTML attributes AND backend SDK calls across Go, Python, and TypeScript projects.

Datastar's contract lives in `data-*` attributes on HTML and `PatchElements`/`PatchSignals` calls on the backend. This tool catches typos, missing selectors, and misconfigurations at build time — not in the browser console.

> **Version compatibility**: Tested against Datastar **v1.x**. The rules check stable DOM-level and Datastar-API-level patterns, and the full rule set is covered by automated tests that run in CI on every push. Minor/patch releases of Datastar (Y.Z) should not affect correctness.

## Contents

- [Why datastar-lint](#why-datastar-lint)
- [Install](#install)
- [Usage](#usage)
- [Available analyzers](#available-analyzers)
- [Update flags](#update-flags)
- [What it catches](#what-it-catches)
- [Enabling analyzers](#enabling-analyzers)
- [Architecture](#architecture)
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
```

Run `datastar-lint --version` to print the linter version. Exit code is `0` on clean, `1` on issues.

### Available analyzers

| Analyzer | Flag name | Extensions | Default? | Language |
|----------|-----------|------------|----------|----------|
| HTML | `html` | `.html`, `.htm`, `.templ`, `.tsx`, `.jsx`, `.ts`, `.js` | yes | All — Templ (Go), JSX/TSX (TS/JS), plain HTML |
| Go | `go` | `.go` | opt-in | Go (stdlib `go/parser`) |
| Python | `python` | `.py` | opt-in | Python |
| TypeScript | `typescript` | `.ts`, `.tsx` | opt-in | TypeScript/JavaScript |

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

- **`PATCH_ELEMENTS_NO_SELECTOR`** — `PatchElements()` / `PatchElementTempl()` / `PatchElementf()` / `PatchElementGostar()` / `RemoveElement()` / `RemoveElementf()` / `RemoveElementByID()` called without `WithSelector`/`WithSelectorID` or with empty/omitted selector argument. Without a CSS selector the JS client throws `PatchElementsNoTargetsFound`. Severity: warning.
- **`PATCH_SELECTOR_EMPTY`** — `WithSelector("")` or `WithSelectorID("")` — empty string is silently dropped by the SDK. Severity: warning.
- **`MERGE_SIGNALS_NIL`** — `MarshalAndPatchSignals(nil)` produces `"null"` on the wire, overwriting all signals. Severity: hint.
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
