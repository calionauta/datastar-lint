package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// datastarImportPath is the Go SDK's import path. The analyzer keys its
// method-form detection on the file importing this, since the SDK exposes no
// package-level patch functions to match on (see isSSEPkg).
const datastarImportPath = "github.com/starfederation/datastar-go/datastar"

func init() {
	RegisterAnalyzer(GoAnalyzer{})
}

// GoAnalyzer lints .go files for Datastar SDK misuse.
// Uses go/parser + go/ast (stdlib, no extra dependencies).
type GoAnalyzer struct{}

func (GoAnalyzer) Name() string             { return "go" }
func (GoAnalyzer) FileExtensions() []string { return []string{"go"} }

func (GoAnalyzer) Lint(path string, cfg config) []lintResult {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return []lintResult{{
			Severity: sevError,
			File:     path,
			Code:     "GO_PARSE_ERROR",
			Message:  fmt.Sprintf("Go parse error: %v", err),
		}}
	}

	imports := buildImportMap(f.Imports)
	sseAliases := sseImportAliases(imports)
	// The SDK is method-only, so the receiver-name heuristic in isSSEPkg is
	// only sound when the file actually imports the SDK.
	hasSDKImport := importsDatastar(imports)

	var results []lintResult

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		funcName, recvName := resolveCall(call)
		isSSE := isSSEMethodCall(funcName, recvName, sseAliases, hasSDKImport)

		// Check: MarshalAndPatchSignals(nil) / MarshalAndPatchSignalsIfMissing(nil)
		// — run for ANY qualified call, not just patch functions. The IfMissing
		// variant delegates to MarshalAndPatchSignals (signals-sugar.go), so nil
		// marshals to "null" on the wire identically; checking only the base name
		// left the sibling uncovered.
		if isMarshalSignalsFunc(funcName) && isSSE {
			if isNilArg(call) {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevHint,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "MERGE_SIGNALS_NIL",
					Message:    funcName + "(nil) produces null on the wire, wiping client signals",
					Suggestion: "Pass an empty signals struct, a map, or a typed struct with fields.",
				})
			}
		}

		// Check: PatchElementf format-arg mismatch.
		if funcName == "PatchElementf" && isSSE {
			if hasFormatMismatch(call) {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevHint,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "PATCH_ELEMENTF_FORMAT",
					Message:    "PatchElementf() format string has % verbs that may not match the number of value arguments",
					Suggestion: "Verify that the format string has the correct number of % verbs for the value arguments.",
				})
			}
		}

		// Patch functions require a selector.
		if !isPatchFunc(funcName) || !isSSE {
			return true
		}

		// RemoveElementByID takes the bare id and does not need a WithSelector
		// option — the SDK prefixes "#" itself. Flagging it was a false positive.
		// RemoveElementf takes a selector/format string (and no options, per the
		// SDK signature), so its selector is present by construction.
		if funcName == "RemoveElementByID" || funcName == "RemoveElementf" {
			return true
		}

		if funcName == "RemoveElement" {
			if len(call.Args) == 0 {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevError,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "PATCH_ELEMENTS_NO_SELECTOR",
					Message:    "RemoveElement() called with no arguments — remove target is unknown",
					Suggestion: "Pass a CSS selector: RemoveElement(\"#element-id\"). The first argument is the selector string.",
				})
			} else if isEmptyRemoveElementArg(call) {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevError,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "PATCH_SELECTOR_EMPTY",
					Message:    "RemoveElement(\"\") called with empty selector — silently dropped by SDK",
					Suggestion: "Pass a non-empty CSS selector: RemoveElement(\"#element-id\").",
				})
			} else if !hasRemoveElementSelector(call) {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevWarning,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "PATCH_ELEMENTS_NO_SELECTOR",
					Message:    "RemoveElement() called with non-string first argument — cannot verify selector",
					Suggestion: "Pass a string CSS selector: RemoveElement(\"#element-id\").",
				})
			}
		} else {
			if !hasSelectorArg(call, sseAliases) {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevError,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "PATCH_ELEMENTS_NO_SELECTOR",
					Message:    fmt.Sprintf("%s() called without WithSelector/WithSelectorID — client has no merge anchor", qualifiedCall(recvName, funcName)),
					Suggestion: "Add sdk.WithSelector(\"#id\") or sdk.WithSelectorID(\"id\") among the arguments.",
				})
			}
			if isEmptySelectorArg(call) {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevError,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "PATCH_SELECTOR_EMPTY",
					Message:    fmt.Sprintf("%s() called with empty selector string — silently dropped by SDK", qualifiedCall(recvName, funcName)),
					Suggestion: "Pass a non-empty selector: WithSelector(\"#actual-id\").",
				})
			}
		}

		return true
	})

	return results
}

// --------------- Import helpers ---------------

func buildImportMap(imports []*ast.ImportSpec) map[string]string {
	m := make(map[string]string)
	for _, imp := range imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if imp.Name != nil {
			m[imp.Name.Name] = path
		} else {
			parts := strings.Split(path, "/")
			m[parts[len(parts)-1]] = path
		}
	}
	return m
}

func sseImportAliases(imports map[string]string) map[string]bool {
	aliases := make(map[string]bool)
	for alias, path := range imports {
		base := path
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			base = path[idx+1:]
		}
		if base == "sse" || base == "datastar" || strings.Contains(path, "/sse") {
			aliases[alias] = true
		}
	}
	return aliases
}

// --------------- Call resolution ---------------

// resolveCall returns the called function's name and, for a qualified call, the
// receiver expression's identifier.
//
// `pkgName` is a misnomer kept for call-site compatibility: for
// `datastar.PatchElements(...)` it is the package name, but for the method form
// `sse.PatchElements(...)` — the ONLY form the v1.x SDK exposes — it is the
// RECEIVER VARIABLE's name. Callers must therefore not assume it names a
// package; use isSSEPkg to decide whether the call is a Datastar SDK call.
func resolveCall(call *ast.CallExpr) (funcName, recvName string) {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if ident, ok := fun.X.(*ast.Ident); ok {
			return fun.Sel.Name, ident.Name
		}
		return fun.Sel.Name, ""
	case *ast.Ident:
		return fun.Name, ""
	}
	return "", ""
}

func qualifiedCall(pkgName, funcName string) string {
	if pkgName != "" {
		return pkgName + "." + funcName
	}
	return funcName
}

// --------------- Function classification ---------------

func isPatchFunc(name string) bool {
	switch name {
	case "PatchElements", "PatchElementTempl", "PatchElementf",
		"PatchElementGostar", "RemoveElement",
		"RemoveElementf", "RemoveElementByID":
		return true
	}
	return false
}

// isSSEPkg reports whether a call is a Datastar SDK call.
//
// The SDK is method-only: `sse.PatchElements(...)` where the receiver is a
// `*datastar.ServerSentEventGenerator` from `datastar.NewSSE(w, r)`. There has
// never been a package-level `datastar.PatchElements` in any 1.x release, so the
// previous alias-only matching was dead code that found nothing in practice.
//
// Without go/types we cannot prove the receiver's type, so the decision rests on
// two things that are checkable from the AST alone:
//
//  1. The file imports the SDK (hasSDKImport). Without this, nothing is flagged
//     — a project that does not use Datastar cannot get findings from this.
//  2. The method name is one the SDK is the sole provider of (isPatchFunc,
//     plus the signals methods handled separately).
//
// Given a file that imports the SDK, a method with one of these names is a
// Datastar call in every realistic program; the converse (another type in the
// same file exposing `PatchElements`) is possible but pathological, and the
// alternative is the false-negative this function replaced.
func isSSEPkg(name string, aliases map[string]bool) bool {
	return name != "" && aliases[name]
}

// isSSEMethodCall reports whether a call to `funcName` on receiver `recvName`
// should be validated as a Datastar SDK call.
func isSSEMethodCall(funcName, recvName string, aliases map[string]bool, hasSDKImport bool) bool {
	if recvName == "" {
		return false
	}
	if isSSEPkg(recvName, aliases) {
		return true
	}
	if !hasSDKImport {
		return false
	}
	return isPatchFunc(funcName) || isSignalsFunc(funcName)
}

// isSignalsFunc reports whether name is one of the SDK's signals methods (no
// selector concept, but still method-form and still worth validating).
func isSignalsFunc(name string) bool {
	switch name {
	case "MarshalAndPatchSignals", "MarshalAndPatchSignalsIfMissing",
		"PatchSignals", "PatchSignalsIfMissingRaw":
		return true
	}
	return false
}

// isMarshalSignalsFunc reports whether name takes `any` signals and marshals
// them to JSON, so `nil` becomes the literal `"null"` on the wire.
// MarshalAndPatchSignalsIfMissing delegates to MarshalAndPatchSignals with
// WithOnlyIfMissing(true) and inherits the defect.
func isMarshalSignalsFunc(name string) bool {
	return name == "MarshalAndPatchSignals" || name == "MarshalAndPatchSignalsIfMissing"
}

// importsDatastar reports whether the parsed file imports the Datastar SDK at
// all, under any alias.
func importsDatastar(imports map[string]string) bool {
	for _, path := range imports {
		if path == datastarImportPath {
			return true
		}
	}
	return false
}

func isSelectorFunc(name string) bool {
	switch name {
	case "WithSelector", "WithSelectorID", "WithSelectorf":
		return true
	}
	return false
}

// --------------- Selector detection (non-RemoveElement) ---------------

// forwardsVariadicOptions reports whether the call forwards an options slice
// with `...` (e.g. `sse.PatchElements(html, opts...)`).
//
// A helper that takes `opts ...PatchElementOption` and forwards them is the
// documented way to wrap the SDK: the selector is supplied by the CALLER and is
// invisible here, so the callee must not be reported as missing one. The
// ellipsis is the syntactic signal that the argument list is not complete at
// this site.
func forwardsVariadicOptions(call *ast.CallExpr) bool {
	return call.Ellipsis.IsValid()
}

func hasSelectorArg(call *ast.CallExpr, sseAliases map[string]bool) bool {
	if forwardsVariadicOptions(call) {
		return true
	}
	for _, arg := range call.Args {
		if isSelectorCall(arg, sseAliases) {
			return true
		}
	}
	return false
}

func isSelectorCall(expr ast.Expr, sseAliases map[string]bool) bool {
	inner, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	var name, pkg string
	switch fun := inner.Fun.(type) {
	case *ast.SelectorExpr:
		name = fun.Sel.Name
		if ident, ok := fun.X.(*ast.Ident); ok {
			pkg = ident.Name
		}
	case *ast.Ident:
		name = fun.Name
	}
	if !isSelectorFunc(name) {
		return false
	}
	if pkg != "" && !sseAliases[pkg] {
		return false
	}
	return true
}

func isEmptySelectorArg(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		inner, ok := arg.(*ast.CallExpr)
		if !ok {
			continue
		}
		var name string
		switch fun := inner.Fun.(type) {
		case *ast.SelectorExpr:
			name = fun.Sel.Name
		case *ast.Ident:
			name = fun.Name
		}
		if !isSelectorFunc(name) {
			continue
		}
		if len(inner.Args) > 0 {
			if lit, ok := inner.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				val := strings.Trim(lit.Value, `"`)
				if val == "" {
					return true
				}
			}
		}
	}
	return false
}

// --------------- Selector detection (RemoveElement) ---------------

func hasRemoveElementSelector(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	return strings.Trim(lit.Value, `"`) != ""
}

func isEmptyRemoveElementArg(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	return strings.Trim(lit.Value, `"`) == ""
}

// --------------- Nil detection ---------------

func isNilArg(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	ident, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "nil"
}

// --------------- Format validation ---------------

func hasFormatMismatch(call *ast.CallExpr) bool {
	if len(call.Args) < 2 {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	fmtStr := strings.Trim(lit.Value, "`\"")
	verbCount := strings.Count(fmtStr, "%")
	// Skip %% (escaped percent) and %! (error format).
	verbCount -= strings.Count(fmtStr, "%%")
	if verbCount <= 0 {
		return false
	}
	// Count non-selector, non-option args after the format string.
	dataArgs := 0
	for _, arg := range call.Args[1:] {
		if isSelectorCall(arg, nil) {
			continue
		}
		dataArgs++
	}
	return verbCount != dataArgs
}
