package main

func init() {
	RegisterAnalyzer(HTMLAnalyzer{})
}

// HTMLAnalyzer lints .html, .htm, and .templ files for Datastar attribute correctness.
// It wraps the existing lintFile function from walk.go with zero behavioral change.
type HTMLAnalyzer struct{}

func (HTMLAnalyzer) Name() string             { return "html" }
func (HTMLAnalyzer) FileExtensions() []string { return []string{"html", "htm", "templ"} }
func (HTMLAnalyzer) Lint(path string, cfg config) []lintResult {
	return lintFile(path, cfg)
}
