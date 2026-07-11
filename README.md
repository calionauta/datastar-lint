# datastar-lint

A multi-language linter for [Datastar](https://data-star.dev). Validates HTML attributes AND backend SDK calls across Go, Python, and TypeScript projects.

Datastar's contract lives in `data-*` attributes on HTML and `PatchElements`/`PatchSignals` calls on the backend. This tool catches typos, missing selectors, and misconfigurations at build time — not in the browser console.

> **Version compatibility**: Tested against Datastar **v1.x**. The rules check stable DOM-level and Datastar-API-level patterns. Minor/patch releases of Datastar (Y.Z) should not affect correctness. For major releases, an audit will be published alongside the linter update. Run `datastar-lint --version` to see the linter version.

## Install

```bash
go install github.com/calionauta/datastar-lint@latest
```

Requires Go 1.26+.

With optional analyzers:

```bash
go install -tags "analyzer_python analyzer_ts" github.com/calionauta/datastar-lint@latest
```

## Usage

```bash
# HTML/Templ linting (default analyzer)
datastar-lint -r ./web/

# Go backend linting (stdlib, always available)
datastar-lint -r --analyzers go ./api/

# HTML + Go + cross-reference checks
datastar-lint -r --analyzers html,go ./project/

# Python linting (requires build tag: analyzer_python)
datastar-lint -r --analyzers python ./src/

# TypeScript linting (requires build tag: analyzer_ts)
datastar-lint -r --analyzers typescript ./src/

# Strict mode: Pro-only attributes become errors
datastar-lint -r -s ./web/
```

Exit code is `0` on clean, `1` on issues.

### Available analyzers

| Analyzer | Flag name | Extensions | Build tag | Language |
|----------|-----------|------------|-----------|----------|
| HTML | `html` | `.html`, `.htm`, `.templ` | _(default)_ | All (Templ, Jinja, ERB, JSX, plain HTML) |
| Go | `go` | `.go` | _(default)_ | Go (stdlib `go/parser`) |
| Python | `python` | `.py` | `analyzer_python` | Python (regex) |
| TypeScript | `typescript` | `.ts`, `.tsx` | `analyzer_ts` | TypeScript/JavaScript (regex) |

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

### Go SDK checks (always built)

- **`PATCH_ELEMENTS_NO_SELECTOR`** — `PatchElements()` / `PatchElementTempl()` / `PatchElementf()` / `PatchElementGostar()` / `RemoveElement()` / `RemoveElementf()` / `RemoveElementByID()` called without `WithSelector`/`WithSelectorID` or with empty/omitted selector argument. Without a CSS selector the JS client throws `PatchElementsNoTargetsFound`. Severity: warning.
- **`PATCH_SELECTOR_EMPTY`** — `WithSelector("")` or `WithSelectorID("")` — empty string is silently dropped by the SDK. Severity: warning.
- **`MERGE_SIGNALS_NIL`** — `MarshalAndPatchSignals(nil)` produces `"null"` on the wire, overwriting all signals. Severity: hint.
- **`PATCH_ELEMENTF_FORMAT`** — `PatchElementf()` format string has `%` verbs that may not match the number of value arguments. Severity: hint.
- **`GO_PARSE_ERROR`** — The Go file could not be parsed. Severity: error.

### Python SDK checks (build tag: `analyzer_python`)

- **`PY_PATCH_NO_SELECTOR`** — `SSE.patch_elements(...)` called without `selector=` keyword. Severity: warning.
- **`PY_PATCH_EMPTY_SELECTOR`** — `SSE.patch_elements(...)` with `selector=""` or `selector=''`. Severity: warning.
- **`PY_REMOVE_NO_SELECTOR`** — `SSE.remove_elements(...)` called with empty or missing selector argument. Severity: warning.

### TypeScript SDK checks (build tag: `analyzer_ts`)

- **`TS_PATCH_NO_SELECTOR`** — `stream.patchElements(...)` or `sse.patchElements(...)` called without `selector:` in options. Severity: warning.
- **`TS_PATCH_EMPTY_SELECTOR`** — `selector: ""` or `selector: ''`. Severity: warning.
- **`TS_REMOVE_NO_SELECTOR`** — `stream.removeElements(...)` / `sse.removeElements(...)` called with empty or missing selector argument. Severity: warning.

### Cross-reference checks (when both `go` and `html` analyzers are active)

- **`CROSSREF_ORPHAN_SELECTOR`** — A Go `WithSelector("#id")` references an element id that doesn't exist in any scanned `.templ`/`.html` file. Severity: warning.

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

## Build tags

Optional analyzers are gated behind build tags to keep the default binary lean:

```bash
go install .                                   # HTML + Go analyzers
go install -tags analyzer_python .             # + Python analyzer
go install -tags analyzer_ts .                 # + TypeScript analyzer
go install -tags "analyzer_python analyzer_ts" .  # All
```

When a build-tagged analyzer is not compiled, running `--analyzers python` exits with an error listing available analyzers.

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

To add a new language, create a file implementing `Analyzer`, call `RegisterAnalyzer()` in `init()`, and (optionally) gate it behind a build tag.

## Where to run it

datastar-lint catches mistakes that language compilers and browsers ignore
(see [Why datastar-lint](#why-datastar-lint)). Run it wherever you produce or
change Datastar output:

| When | Command | Why |
|------|---------|-----|
| **After `templ generate`** | `templ generate && datastar-lint -r ./features/` | Templ emits `.templ`/`.html` — lint the generated attributes |
| **Before commit** | `datastar-lint -r ./` | Gate on every change across all enabled analyzers |
| **In CI** | `datastar-lint -r ./` | PR gate; cross-reference needs both `go` + `html` |

> Python and TypeScript have no code-generation step, so there is no "after
> generate" trigger for them — lint their source directly with the matching
> analyzer flag (see [Available analyzers](#available-analyzers)).

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

## License

MIT.
