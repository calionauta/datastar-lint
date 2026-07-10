# Contributing to datastar-lint

## Project structure

```
.
├── main.go              # CLI entry point, flag parsing, runner
├── walk.go              # HTML DOM walker + attribute validation rules
├── types.go             # lintResult struct + severity types
├── helpers.go           # Shared helper functions
├── attrs.go             # Known Datastar attribute definitions + parser
├── actions.go           # @get/@post/@put/etc. action validation
├── forms.go             # Form-specific rules
├── signals.go           # data-signals JSON validation
├── crossref.go          # Cross-reference Go ↔ HTML id checks
├── patch.go             # PatchElements target ID checks
├── analyzer.go          # Analyzer interface + registration
├── lint-html.go         # HTML analyzer
├── lint-go.go           # Go analyzer
├── lint-python.go       # Python analyzer (build tag: analyzer_python)
├── lint-typescript.go   # TypeScript analyzer (build tag: analyzer_ts)
├── source.go            # Raw source scanning for broken attributes
└── testdata/            # E2E test fixtures
```

## How to add a new lint rule

### 1. Add the check logic in `walk.go`

For HTML attribute rules, find the appropriate `case` in `validateDatastarAttr()`:

```go
case "data-on":
    // Add your check here following the existing pattern:
    if attrKey == "myevent" && !hasModifier(modifiers, "window") {
        *results = append(*results, lintResult{
            Severity:   sevError,
            File:       path,
            Line:       line,
            Col:        col,
            Element:    tag,
            Attribute:  name,
            Code:       "ON_MYEVENT_NO_EVENT",  // SCREAMING_SNAKE_CASE
            Message:    fmt.Sprintf("data-on:myevent on <%s> — description why", tag),
            Suggestion: "How to fix it",
        })
    }
```

**Conventions:**
- Code: `SCREAMING_SNAKE_CASE`
- Severity: `sevError` for real bugs, `sevWarning` for likely mistakes, `sevHint` for suggestions
- Message: `"data-on:X on <%s> — explanation"` (lowercase, uses `——` dash)
- Suggestion: concrete fix instruction in `\backtick quotes\`

### 2. Write tests in `main_test.go`

Add a test function:
```go
func TestOnMyeventNoEvent(t *testing.T) {
    // Bad cases — trigger the rule
    results := lintString(t, config{}, `<div data-on:myevent="$x = 1">x</div>`, "html")
    r := hasCode(t, results, "ON_MYEVENT_NO_EVENT")
    if r == nil { t.Errorf("expected ON_MYEVENT_NO_EVENT; got %v", codes(results)) }

    // Good cases — should NOT trigger
    results = lintString(t, config{}, `<div data-on:myevent__window="$x = 1">x</div>`, "html")
    if r := hasCode(t, results, "ON_MYEVENT_NO_EVENT"); r != nil {
        t.Errorf("__window should not flag; got %v", codes(results))
    }
}
```

Also add an entry in `TestAllDocumentedRules` (regression net).

### 3. Update testdata

- `testdata/bad.html` — add a bad case
- `testdata/good.html` — add a correct case

### 4. Document in `README.md`

Add the rule under the appropriate section following the existing descriptions.

### 5. Run tests

```bash
go test -count=1 ./...
```

### 6. Verify no false positives

Run the linter on real-world Datastar templates to ensure the rule doesn't fire incorrectly:

```bash
go run . --verbose -r ./path/to/project/
```

## How to build

```bash
make build          # binary with ldflags version injection
make test           # run tests
make test-verbose   # run tests with verbose output
make lint           # run golangci-lint (if installed)
```

## How to release

```bash
# 1. Tag and push
make release V=v0.X.Y

# 2. Or manually:
git tag -a v0.X.Y -m "Release v0.X.Y"
git push origin v0.X.Y

# 3. GitHub Actions will build and (optionally) goreleaser will publish
```

## Rule design principles

1. **KISS** — One if-check per rule. No abstractions unless shared by 3+ rules.
2. **DRY** — Reuse `hasModifier()`, `parseDatastarAttr()`, `isLoadingElement()`.
3. **No false positives** — Better to miss a bug than to annoy users with noise.
   - Use `sevWarning`/`sevHint` when uncertain.
   - Always provide a concrete `Suggestion`.
4. **DOM fundamentals** — Prefer rules based on stable DOM/HTML spec behaviors over Datastar version-specific behaviors.

## When NOT to add a rule

- The event is so obviously window-only that no developer would misuse it (e.g., `popstate`, `beforeunload`)
- The rule would catch <1% of real-world bugs
- The fix would be discovered naturally in <2 minutes of browser testing
