# datastar-lint

A linter for [Datastar](https://data-star.dev) HTML attributes. Language-agnostic: works on any HTML output (Templ/Go, Jinja/Python, ERB/Ruby, JSX, plain HTML).

Datastar is an HTML/SSE protocol — the contract lives in `data-*` attributes. This tool validates that your HTML actually follows the spec, so you catch typos and misuse at build time instead of in the browser console.

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

# Strict mode: also flag unknown Pro-only attributes
datastar-lint -r -s ./web/
```

Exit code is `0` on clean, `1` on issues.

## What it catches

- **Unknown attributes.** Flags `data-foobar`, `data-on-clik` (typo), or attributes outside the Datastar spec.
- **Pro-only attributes** in non-strict mode (warn-only). Strict mode (`-s`) makes them errors so CI fails if someone uses a paid feature without the license.
- **JSON validation in `data-signals`.** Single-quoted attributes (`data-signals='{...}'`) must use `SafeJSON` to escape inner single quotes; the linter catches raw JSON with unescaped `'` that would break the Datastar parser client-side.
- **Modal/DaisyUI conflict.** `<dialog class="modal">` with `data-show` instead of `data-class='{"modal-open": ...}'` is flagged, since DaisyUI modals need the `modal-open` class toggled.
- **Forms missing submit.** A `<form>` that uses Datastar `contentType: 'form'` without a `type="submit"` button triggers `FetchFormNotFound` at runtime; caught here.
- **Modifier misuse.** Wrong key spellings in `data-on-click__debounce.leading` (note the `__` separator), invalid `data-computed-*` prefixes, etc.
- **Alpine.js / Vue.js leftovers.** Catches `x-data`, `x-on:click`, `v-if`, `@click` attributes that look like leftover non-Datastar frameworks.

## Where to run it

Three natural integration points — pick what fits your stack:

| When | How | Why |
|------|-----|-----|
| **After `templ generate`** | `templ generate && datastar-lint -r ./features/` | Catches attributes you wrote in `.templ` files. Fastest feedback. |
| **Pre-commit (changed files only)** | `datastar-lint -r ./features/` | Blocking gate before the commit lands. |
| **In CI** | `datastar-lint -r ./features/ web/` | Full repo scan on every PR. |

The `Makefile` and pre-commit hook in [cali-go-stack](https://github.com/calionauta/cali-go-stack) wire all three.

## Why a separate tool

Datastar's attribute grammar is rich (signals, plugins, modifiers, expressions, JSON-typed values). `golangci-lint` and HTML linters don't know it. A dedicated linter gives you:

- **Zero false positives on Datastar-specific syntax.** Generic HTML validators choke on `data-on-click__debounce.leading` or the JSON-inside-single-quotes pattern.
- **Structured errors** that point at the attribute, not the line.
- **Strict mode** for teams that want to forbid Pro attributes before they accidentally ship a license-violating demo.

## License

MIT.