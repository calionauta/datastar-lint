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
// It uses go/parser + go/ast (stdlib, no extra dependencies).
//
// Checks:
//   - PATCH_ELEMENTS_NO_SELECTOR: PatchElements/PatchElementTempl/RenderAndPatch
//     called without WithSelector/WithSelectorID/WithSelectorf
//   - PATCH_SELECTOR_EMPTY: WithSelector("") or WithSelectorID("")
//   - MERGE_SIGNALS_NIL: MarshalAndPatchSignals(nil)
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

		// Check 1: Patch functions require a selector.
		if isPatchFunc(funcName) && isSSEPkg(pkgName, sseAliases) {
			if !hasSelectorArg(call, sseAliases) {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevWarning,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "PATCH_ELEMENTS_NO_SELECTOR",
					Message:    fmt.Sprintf("%s() called without WithSelector/WithSelectorID — client has no merge anchor", qualifiedCall(pkgName, funcName)),
					Suggestion: "Add sdk.WithSelector(\"#id\") or sdk.WithSelectorID(\"id\") among the arguments. Without a CSS selector the Datastar JS client can't find the DOM target and throws PatchElementsNoTargetsFound.",
				})
			}
		}

		// Check 2: Empty selector.
		if isPatchFunc(funcName) && isSSEPkg(pkgName, sseAliases) {
			if isEmptySelectorArg(call) {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevWarning,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "PATCH_SELECTOR_EMPTY",
					Message:    fmt.Sprintf("%s() called with empty selector string — silently dropped by SDK", qualifiedCall(pkgName, funcName)),
					Suggestion: "Pass a non-empty selector: WithSelector(\"#actual-id\"). An empty string is ignored at the SDK's selector check (if options.Selector != \"\").",
				})
			}
		}

		// Check 3: MarshalAndPatchSignals(nil).
		if funcName == "MarshalAndPatchSignals" && isSSEPkg(pkgName, sseAliases) {
			if isNilArg(call) {
				pos := fset.Position(call.Pos())
				results = append(results, lintResult{
					Severity:   sevHint,
					File:       path,
					Line:       pos.Line,
					Col:        pos.Column,
					Code:       "MERGE_SIGNALS_NIL",
					Message:    "MarshalAndPatchSignals(nil) produces null on the wire",
					Suggestion: "Pass an empty signals struct, a map, or a typed struct with fields. json.Marshal(nil) returns \"null\", overwriting existing signals with null.",
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
		"PatchElementGostar", "RenderAndPatch", "RemoveElement":
		return true
	}
	return false
}

func isSSEPkg(pkgName string, aliases map[string]bool) bool {
	if pkgName == "" {
		return true // unqualified call — assume SSE-related
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

// --------------- Selector detection ---------------

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
