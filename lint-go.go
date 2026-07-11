package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

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

	var results []lintResult

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		funcName, pkgName := resolveCall(call)
		isSSE := isSSEPkg(pkgName, sseAliases)

		// Check: MarshalAndPatchSignals(nil) — run for ANY qualified call,
		// not just patch functions.
		if funcName == "MarshalAndPatchSignals" && isSSE {
			if isNilArg(call) {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevHint,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "MERGE_SIGNALS_NIL",
					Message:    "MarshalAndPatchSignals(nil) produces null on the wire",
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

		if funcName == "RemoveElement" {
			if len(call.Args) == 0 {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevWarning,
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
					Severity:   sevWarning,
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
					Severity:   sevWarning,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "PATCH_ELEMENTS_NO_SELECTOR",
					Message:    fmt.Sprintf("%s() called without WithSelector/WithSelectorID — client has no merge anchor", qualifiedCall(pkgName, funcName)),
					Suggestion: "Add sdk.WithSelector(\"#id\") or sdk.WithSelectorID(\"id\") among the arguments.",
				})
			}
			if isEmptySelectorArg(call) {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevWarning,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "PATCH_SELECTOR_EMPTY",
					Message:    fmt.Sprintf("%s() called with empty selector string — silently dropped by SDK", qualifiedCall(pkgName, funcName)),
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

func resolveCall(call *ast.CallExpr) (funcName, pkgName string) {
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

func isSSEPkg(pkgName string, aliases map[string]bool) bool {
	if pkgName == "" {
		return true
	}
	return aliases[pkgName]
}

func isSelectorFunc(name string) bool {
	switch name {
	case "WithSelector", "WithSelectorID", "WithSelectorf":
		return true
	}
	return false
}

// --------------- Selector detection (non-RemoveElement) ---------------

func hasSelectorArg(call *ast.CallExpr, sseAliases map[string]bool) bool {
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
