# datastar-lint

A linter for [Datastar](https://data-star.dev) HTML attributes. Language-agnostic: works on any HTML output (Templ/Go, Jinja/Python, ERB/Ruby, JSX, plain HTML).

Datastar's contract lives in `data-*` attributes on HTML. The frontend is a single tiny file (per data-star.dev); the backend drives everything via HTML and SSE. This tool validates your HTML against the Datastar attribute spec so you catch typos and misuse at build time, not in the browser console.

## Install

```bash
go install github.com/calionauta/datastar-lint@latest
```

Requires Go 1.26+.

## Usage

```bash
# Lint a directory recursively (default extensions: html, htm, templ)
datastar-lint -r ./web/

# Lint specific files
datastar-lint ./web/index.html ./components/todo.templ

# Custom extensions
datastar-lint -r -e "html,htm,templ,go.html" ./templates/

# Strict mode: Pro-only attributes become errors (otherwise warnings)
datastar-lint -r -s ./web/
```

Exit code is `0` on clean, `1` on issues.

## What it catches

Every rule below was verified against the linter source. Each finding prints its `Code` and a `Suggestion`.

### Attributes & typos

- **`UNKNOWN_ATTR` / `UNKNOWN_ATTR_TYPO`** — Flags `data-foobar`, `data-on-clik` (typo), or any `data-*` attribute not in the Datastar spec. A hand-curated typo map produces a "did you mean" suggestion. Severity: **error** when a close-match typo is found (`UNKNOWN_ATTR_TYPO`), **warning** otherwise (`UNKNOWN_ATTR`).
- **`KEY_NOT_ALLOWED`** — Datastar attributes that do not accept sub-keys (the `:signalName` syntax) reject them. E.g. `data-show:foo` is invalid. Severity: error.
- **`INVALID_MODIFIER`** — Each Datastar attribute accepts only specific modifiers. Unknown modifiers are flagged, e.g. `data-on-click__frobnicate`. Known time modifiers (`debounce`, `throttle`, `delay`) accept a `.duration` suffix; `case` requires a valid style (`kebab`, `camel`, `pascal`, `snake`, `title`, `upper`, `lower`); `prop`/`event` are `data-bind`-only; `duration` is `data-on-interval`-only. Severity: warning.
- **`PRO_ATTR`** — Datastar Pro-only attributes (`data-persist`, `data-replace-url`, `data-animate`, `data-on-raf`, `data-custom-validity`, `data-match-media`, `data-query-string`, `data-scroll-into-view`, `data-view-transition`). Severity: **warning** in normal mode, **error** in strict mode (`-s`), so CI fails if someone ships a paid feature without a license.
- **`FOREIGN_ATTR`** — Alpine.js / Vue.js leftovers: `x-data`, `x-on:click`, `v-if`, `v-show`, `v-bind`, `v-on:click`, `@click` are flagged as non-Datastar. Severity: warning (only on non-`.templ` files, to avoid false positives from Go template expressions).

### Signals

- **`SIGNALS_UNESCAPED_QUOTES`** — `data-signals` whose parsed value contains a literal `'` (e.g. double-quoted `data-signals="{'name': "o'brien"}"`) breaks the Datastar JSON parse client-side. Suggests `SafeJSON` (Go) or `&#39;` escaping. (Single-quoted attributes are mangled by the HTML parser before we see them, so that truncation surfaces via `SIGNALS_JS_OBJECT`/parse checks instead.) Severity: warning.
- **`SIGNALS_JS_OBJECT`** — `data-signals` uses JS object notation (not strict JSON); valid in Datastar but templ may need escaping. Severity: hint.
- **`EXPR_MISSING_DOLLAR`** — A value that looks like a signal name but is missing the `$` prefix (e.g. `data-text="name"` instead of `data-text="$name"`); the expression won't react to signal changes. Severity: warning.
- **`JSON_SIGNALS_NO_TERSE`** — `data-json-signals` without the `__terse` modifier displays the full JSON structure. Severity: hint.

### Expressions & empty values

- **`TEXT_EMPTY`** — `data-text` with empty expression; the element will have no content. Severity: warning.
- **`EFFECT_EMPTY`** — `data-effect` with no expression; nothing happens on signal change. Severity: warning.
- **`COMPUTED_EMPTY`** — `data-computed` with no expression; the computed signal does nothing. Severity: warning.
- **`REF_EMPTY`** — `data-ref` with no name; the element reference will not be accessible. Severity: warning.
- **`SCROLL_NO_TARGET`** — `data-scroll-into-view` with no selector; scrolls the element itself into view. Severity: warning.

### Actions

- **`GET_WITH_MUTATION`** — `@get()` used with a mutation-like endpoint; GET should be idempotent. Use `@post()`/`@put()`/`@patch()`/`@delete()`. Severity: warning.
- **`SDK_FUNC_IN_JS`** — `datastar.PostSSE()` etc. are Go SDK functions and don't exist in the browser. Use `@post('/api/endpoint')`. Severity: warning.
- **`USE_REDIRECT`** — `window.location` assignment; use `@get()` or SSE redirect instead. Severity: warning.
- **`ACTION_URL_FORMAT`** — `@get()/@post()` URL should start with `/` (absolute path) or be a full URL. Severity: warning.
- **`INTERSECT_NO_ACTION`** — `data-on-intersect` has no `@get()`/`@post()` action. Severity: warning.
- **`PERSIST_NO_KEY`** — `data-persist` without a key persists all signals (may persist unwanted state). Use `data-persist:myKey`. Severity: hint.

### Forms

- **`FORM_SUBMIT_MISSING`** — A `<form>` with `data-bind` inputs but no `data-on:submit` handler. The bound data will not be sent on submit. Severity: hint.
- **`FORM_SUBMIT_NO_PREVENT`** — Form submit action without the `__prevent` modifier; the browser may reload before Datastar processes it. Severity: warning.
- **`FORM_MISSING_ENCTYPE`** — A form has a file input and `contentType: 'form'` but no `enctype="multipart/form-data"`. Severity: error.
- **`BIND_MISSING_NAME`** — A form element (`input`/`select`/`textarea`) with `data-bind` but no `name` attribute — form data will not be sent. Severity: error.
- **`BIND_NO_NAME`** — `data-bind` has no signal name; use `data-bind:signalName` or `data-bind="signalName"`. Severity: warning.
- **`BIND_NON_FORM`** — `data-bind` on a non-form element without the `__prop` modifier (which makes it a global signal setter). Severity: warning.
- **`INDICATOR_AFTER_INIT`** — `data-indicator` appears after `data-init` on the same element; the indicator signal doesn't exist when init runs. Reorder. Severity: warning.

### Modals, visibility & scripts

- **`MODAL_DATA_SHOW`** — `<dialog class="modal">` with `data-show` instead of `data-class='{"modal-open": $open}'`. DaisyUI modals require the `modal-open` class toggled; `data-show` won't add it, so the modal never opens. Only `class="modal"` dialogs are checked; `data-show` elsewhere is fine. Severity: warning.
- **`SHOW_WITH_HIDDEN`** — An element with both `data-show` and `class="...hidden..."` — they conflict (DaisyUI's `hidden` hides regardless). Severity: warning.
- **`SHOW_EMPTY`** — `data-show=""` with no expression makes the element permanently invisible. Severity: warning.
- **`SCRIPT_DEFER_MISSING`** — A Datastar `<script>` loaded without `defer` may process the DOM before it's ready. Severity: warning.

### Infrastructure (non-attribute)

- **`PARSE_ERROR`** — The HTML parser failed on the file. Severity: error.
- **`FILE_OPEN`** — The linter could not open the file. Severity: error.

**Strict mode (`-s`)** upgrades `PRO_ATTR` from warning to **error**. It does not change the severity of other rules.

## Where to run it

| When | How | Why |
|------|-----|-----|
| **After `templ generate`** | `templ generate && datastar-lint -r ./features/` | Catches attributes you wrote in `.templ` files. Fastest feedback. |
| **Pre-commit (full scan)** | `datastar-lint -r ./features/` | Blocking gate before the commit lands. |
| **In CI** | `datastar-lint -r ./features/ web/` | Full repo scan on every PR. |

The `Makefile` and pre-commit hook in [cali-go-stack](https://github.com/calionauta/cali-go-stack) wire all three.

## Why a separate tool

Datastar's attribute grammar is rich (signals, plugins, modifiers, expressions, JSON-typed values). Generic HTML validators don't know it:

- `data-on-click__debounce.leading` looks like a parse error to a generic linter; it's a real Datastar pattern.
- The JSON-inside-single-quotes pattern (`data-signals='{...}'`) is valid HTML but only safe with `SafeJSON` escaping — a Datastar-specific concern.

A dedicated linter gives you structured errors that point at the attribute (with a code you can grep and triage per-project), not just a line number.

## License

MIT.
