# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.14.1] - 2026-10-17

### Fixed

- **The bound-signal check missed two of the three write forms.** v0.14.0
  compared the Go field name (`SearchQuery`) against the wire name a template
  binds (`searchQuery`), so a struct-literal field and a field assignment were
  never reported — only map keys were. Against the project that surfaced the
  class, that reported 4 findings where the corrected check reports 9.

  The Go side now parses with `go/ast` and resolves every write through the
  field → JSON tag index, so all three forms are covered:

  - a map key: `map[string]any{"searchQuery": v}`
  - a struct-literal field: `Signals{SearchQuery: v}`
  - a field assignment: `s.SearchQuery = v`

  A struct declared outside the scanned files (its tag therefore unknown) falls
  back to a case-insensitive match against the bound signals themselves, and
  only when that match is unique — a guess must never become a false positive.

### Added

- **Dotted bindings arm their root.** `data-bind="user.name"` now also marks
  `user` as client-owned, because a server write of the whole `user` object
  replaces the nested value the input reads.
- **`onlyIfMissing` is honoured as the documented escape hatch.**
  `MarshalAndPatchSignalsIfMissing` and an explicit `WithOnlyIfMissing(true)`
  make the client apply a signal only when one does not already exist, so such
  a write cannot overwrite a bound input and is not reported.
  `WithOnlyIfMissing(false)` is an explicit opt-out and is still reported. The
  hatch covers a write, not the tag: a struct field without `omitempty` remains
  an error, because the same struct is merged on other paths.

### Changed

- The `CLIENT_BOUND_SIGNAL_WRITTEN` suggestion now names `onlyIfMissing` and
  cites the upstream issues whose write-back this class matches
  (starfederation/datastar#1228, #783) — the docs state the general model
  ("the backend is the source of truth") but not this collision, so the rule
  was arriving at the right answer without saying why.

## [0.14.0] - 2026-10-17

### Added

- **`CLIENT_BOUND_SIGNAL_NO_OMITEMPTY` (error) and
  `CLIENT_BOUND_SIGNAL_WRITTEN` (warning)** — a new cross-reference check for
  the bound-signal class: a `data-bind="x"` input owns its signal, so a server
  merge that writes `x` can overwrite what the user is typing, and the value
  the server holds always lags (it only advances after a debounced
  round-trip). The debounced request then reads the clobbered value and the
  feature silently does nothing.

  Two halves, because the bug has two:

  - `..._NO_OMITEMPTY` (error) catches the invisible half: a struct field whose
    JSON name is a bound signal, with a tag that lacks `omitempty`. The zero
    value still marshals (`"x":""`), so **removing the field from the merge
    call does not stop the write** — which is exactly how this survived a first,
    wrong fix in the project that surfaced it (its browser smoke went from 3/3
    passing to 0/3).
  - `..._WRITTEN` (warning) catches the visible half: an explicit write of a
    bound signal at the merge site. Sometimes intentional (clearing an input
    after a successful submit), so it informs rather than blocks.

  Runs when both `go` and `html` analyzers are active — the template knows the
  binding, the Go file knows the write. Against the pre-fix sources of the
  project that surfaced it, the check reports 4 findings; after the fix, only
  the one intentional clear remains, as a warning.

## [0.13.3] - 2026-10-09

### Fixed

- **Fallback version lagged the release again.** v0.13.2 shipped the tag
  without bumping the `version` constant, so the installed binary kept
  reporting v0.13.1. Bumped, and added
  `TestFallbackVersionTracksChangelog` so the suite — not a human
  comparing strings — catches the next miss.

## [0.13.2] - 2026-10-09

### Fixed

- **`<script` inside a `//` comment opened a phantom script region.**
  The `templ` analyzer treated the comment text as a tag, swallowing the
  real markup up to the next `</script>` — findings were misattributed to
  the swallowed lines (or hidden entirely). Tag scanning now skips
  candidates inside line comments, shared by both region loops. Found via
  a shared realtime component whose doc comment mentions script bodies.

## [0.13.1] - 2026-10-08

### Fixed

- **Fallback version string lagged the release.** `go install @v0.13.0`
  reported v0.12.0 and the update check nagged forever. The fallback now
  tracks the latest tag; `go install @v0.13.1` identifies correctly.

## [0.13.0] - 2026-10-08

### Added

- **Opt-in `templ` analyzer** (`--analyzers templ`): flags Go template
  expressions that render literally — `TEMPL_EXPR_IN_SCRIPT` (`{ name }`
  inside markup-embedded `<script>`) and `TEMPL_EXPR_IN_ATTR` (inside
  quoted attribute strings). Bare identifiers without braces, JS object
  literals/calls, `${x}` template literals, and `templ script`
  declarations stay quiet by design. Found wiring a real page whose
  roster silently stayed empty.

## [0.12.0] - 2026-10-05

### Fixed

- **The Go analyzer matched an API the SDK has never had.** It only recognised
  `datastar.PatchElements(sse, ...)`, a package-level function that does not
  exist in any 1.x release — writing that form does not compile
  (`undefined: datastar.PatchElements`). The SDK is **method-only**
  (`sse.PatchElements(...)`), so the analyzer was inert on every real call site,
  including the fixtures it shipped with. Detection now keys on the file
  importing the SDK plus a method name the SDK owns.
- **A missing selector could never fail a build.** The rule was
  `sevWarning`, and `countErrors` — which decides the exit code — counts only
  `sevError`. Combined with the usual `-only-errors` invocation the check was
  invisible twice over: filtered from the output *and* unable to set a non-zero
  exit. A missing selector means the client throws
  `PatchElementsNoTargetsFound` and the update silently never lands, so
  `PATCH_ELEMENTS_NO_SELECTOR` and `PATCH_SELECTOR_EMPTY` are now errors.
- **`RemoveElementByID` and `RemoveElementf` were false positives.** Neither
  takes options — the former prefixes `#` to the bare id itself, the latter
  carries its selector in the format string — so neither can be "missing a
  selector". Both are now skipped.
- **Line numbers collapsed for a repeated tag.** `source.locate` anchored its
  cursor at the element it had just found but never consumed past it, so the
  next lookup for the *same* tag found the first match again: three
  `<div data-x>` on consecutive lines were all reported at line 1. Anchors are
  now memoized per node and the cursor advances past each opening tag.
- **Options forwarded via `opts ...` read as a missing selector.** A helper
  taking `opts ...PatchElementOption` and forwarding them
  (`sse.PatchElements(html, opts...)`) is the documented way to wrap the SDK,
  and the selector comes from the caller. Now recognised via the call's
  ellipsis. Without this, the severity change above would have made every such
  wrapper un-buildable.
- **`MarshalAndPatchSignalsIfMissing(nil)` was unchecked.** It delegates to
  `MarshalAndPatchSignals` with `WithOnlyIfMissing(true)`, so `nil` marshals to
  `"null"` on the wire identically — the defect shipped under a method name that
  looked covered. The message now names the method actually called.
- **Multiple paths on the command line: only the first was linted.**
  `cfg.root` was assigned from `args[0]`, so `datastar-lint ./features ./internal`
  silently skipped `./internal` and printed "No issues found" — a gate that
  looked like it covered both trees and covered one.

### Added

- **`@query()` is recognised** (Datastar core v1.0.4). It previously fell
  through to the "no action matched" branch, silently skipping both the
  URL-format and HTTP-method checks.

### Changed

- **`datastarTested` moves from v1.0.2 to v1.0.4**, matching the core release
  the rule set was verified against.
- **`testdata/` is loaded by tests.** It is what `CONTRIBUTING.md` tells a
  contributor to extend, but nothing referenced it by path in any commit, so the
  fixtures were decorative — and had drifted onto the phantom API above, passing
  only because their receiver happened to be named `datastar` (matching the
  import alias, not the method form real code uses). `testdata_test.go` now
  asserts the codes each fixture's inline comments claim.
- **README documents the method-only API shape**, so the next rule is written
  against the real API.

### Breaking

`PATCH_ELEMENTS_NO_SELECTOR` and `PATCH_SELECTOR_EMPTY` are now `ERROR`
severity. A project that previously saw these as non-blocking warnings will now
exit non-zero. That is the intent — an unmatched selector is a runtime defect,
not a style opinion — but it is a behaviour change for CI, so pin the version
and bump deliberately.

## [0.11.2] - 2026-07-13

### Fixed

- `--update` now extracts the correct binary from release archives.

## [0.11.1] - 2026-07-13

### Added

- Report the tested Datastar version in `--version` and the stderr banner.