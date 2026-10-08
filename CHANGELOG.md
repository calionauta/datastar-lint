# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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