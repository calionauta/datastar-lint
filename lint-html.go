package main

func init() {
	RegisterAnalyzer(HTMLAnalyzer{})
}

// HTMLAnalyzer lints .html, .htm, .templ, and JS/TS source (.tsx, .jsx, .ts, .js)
// for Datastar attribute correctness. JSX/TSX render to HTML and pass data-*
// attributes through verbatim, so their markup is linted here too. It wraps the
// existing lintFile function from walk.go with zero behavioral change.
type HTMLAnalyzer struct{}

func (HTMLAnalyzer) Name() string { return "html" }
func (HTMLAnalyzer) FileExtensions() []string {
	return []string{"html", "htm", "templ", "tsx", "jsx", "ts", "js"}
}
func (HTMLAnalyzer) Lint(path string, cfg config) []lintResult {
	return lintFile(path, cfg)
}
