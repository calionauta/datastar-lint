package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// collectStringConsts returns package-level string constants by name.
//
// String constants are how signal names and selectors are actually written in
// a real tree (`const signalChatPrompt = "chatPrompt"`), so a scanner that
// only understands literals sees nothing. Both the bound-signal check and the
// selector cross-reference resolve identifiers through this table; before it
// existed, 39 writes in gogogo (two of them clobbering a bound input) and any
// constant selector were invisible.
//
// Only package-level `const` declarations are considered. A const inside a
// function is legal and unhandled on purpose: it would need scope tracking,
// and the package-level form is the convention this indexes.
func collectStringConsts(files []string) map[string]string {
	consts := make(map[string]string)
	fset := token.NewFileSet()
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range af.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					consts[name.Name] = unquote(lit.Value)
				}
			}
		}
	}
	return consts
}

// resolveName returns the string value an expression denotes: a literal
// directly, or a package-level string constant by name. Anything else (a call,
// a variable, a concatenation) resolves to "" — the checks report what they
// can prove and stay quiet otherwise.
func resolveName(expr ast.Expr, consts map[string]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		return unquote(e.Value), true
	case *ast.Ident:
		if v, ok := consts[e.Name]; ok {
			return v, true
		}
	case *ast.ParenExpr:
		return resolveName(e.X, consts)
	}
	return "", false
}
