# datastar-lint

A linter for [Datastar](https://data-star.dev) HTML attributes. Language-agnostic: works on any HTML output (Templ/Go, Jinja/Python, ERB/Ruby, JSX, plain HTML).

Datastar's contract lives in `data-*` attributes on HTML. The frontend is a single **11.76 KiB** file (per data-star.dev); the backend drives everything via HTML and SSE. This tool validates your HTML against the Datastar attribute spec so you catch typos and misuse at build time, not in the browser console.

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

# Strict mode: unknown data-* attributes are errors instead of warnings
datastar-lint -r -s ./web/
```

Exit code is `0` on clean, `1` on issues.

## What it catches

Every rule below was verified against the linter source. Run with `-v` to see codes and suggestions per finding.

- **`UNKNOWN_ATTR` / `UNKNOWN_ATTR_TYPO`** — Flags `data-foobar`, `data-on-clik` (typo), or any `data-*` attribute not in the Datastar spec. Includes a "did you mean" suggestion via a hand-curated typo map. Severity: **error** when a close-match typo is found, **warning** otherwise.
- **`SIGNALS_UNESCAPED_QUOTES`** — `data-signals='{"key":"value"}'` rendered via Templ (or any templating engine that produces single-quoted attributes) will break at the attribute boundary if the JSON contains a literal `'`. Catches unescaped single quotes inside the value and suggests `SafeJSON` (Go) or `&#39;` escaping. Severity: warning.
- **`KEY_NOT_ALLOWED`** — Datastar attributes that do not accept sub-keys (the `:signalName` syntax in `data-on-click:mySignal`) reject keys with `KEY_NOT_ALLOWED` (error). E.g. `data-show:foo` is invalid.
- **`INVALID_MODIFIER`** — Each Datastar attribute accepts only specific modifiers (`debounce`, `throttle`, `case`, etc., per-attribute). E.g. `data-on-raf__debounce` is invalid because `data-on-raf` only allows `throttle`. Severity: warning.
- **`MODAL_DAISYUI`** — `<dialog class="modal">` with `data-show` instead of `data-class='{"modal-open": $open}'`. DaisyUI modals require the `modal-open` class toggled; `data-show` won't add it, so the modal never opens. Severity: error.
- **`DATA_SHOW_HIDDEN_CONFLICT`** — An element with both `data-show` and `class="...hidden..."` — they conflict (DaisyUI's `hidden` hides regardless). Severity: warning.
- **`DATA_SHOW_EMPTY`** — `data-show=""` with no expression makes the element permanently invisible. Severity: error.
- **`FORM_SUBMIT_MISSING`** — A `<form>` with `data-bind` inputs but no `data-on:submit` handler. The bound data will not be sent on submit. Severity: hint.
- **`NON_FORM_DATA_BIND`** — `data-bind` on a non-form element without `__prop` modifier (which makes it a global signal setter). Severity: hint.
- **Alpine.js / Vue.js leftovers** — `x-data`, `x-on:click`, `v-if`, `v-show`, `v-bind`, `v-on:click`, `@click` are flagged as non-Datastar. Severity: warning.

**Strict mode (`-s`)** upgrades `UNKNOWN_ATTR` warnings to errors. It does **not** currently block Pro-only attributes (`data-animate`, `data-on-raf`, `data-custom-validity`, `data-match-media`); those are silently allowed in non-strict mode and pass through in strict mode. If you need a Pro gate, file an issue.

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

A dedicated linter gives you structured errors that point at the attribute, not the line, and rule codes you can grep and silence per-project.

## License

MIT.