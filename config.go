package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// lintConfigFile represents the .datastar-lint.yaml configuration file.
type lintConfigFile struct {
	Attributes struct {
		Allowed []string `yaml:"allowed"`
	} `yaml:"attributes"`
	// Suppressions lets the PROJECT — not the agent editing a source file —
	// decide which codes may not be silenced here. An entry lands in the same
	// never-suppressible set as the built-in blocklist, so the decision lives in
	// a reviewed, version-controlled file instead of in a comment next to the
	// code it excuses. Typical use: forbid the one code the team has decided is
	// always a bug at this project (e.g. CLIENT_BOUND_SIGNAL_WRITTEN), while a
	// neighbouring project with a different UI contract allows it.
	Suppressions struct {
		Forbidden []string `yaml:"forbidden"`
	} `yaml:"suppressions"`
}

// configFileNames lists the recognised config file names in order of preference.
var configFileNames = []string{
	".datastar-lint.yaml",
	".datastar-lint.yml",
}

// findConfig walks up from dir looking for the first existing config file.
// Returns the full path if found, or "" if none exists.
func findConfig(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		for _, name := range configFileNames {
			p := filepath.Join(abs, name)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p
			}
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	return ""
}

// loadConfigFile reads and parses a YAML config file at path.
func loadConfigFile(path string) (*lintConfigFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg lintConfigFile
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

// isAllowedCustomAttr checks whether a parsed attribute name (baseAttr) matches
// an entry in the user-configured allowed list. It handles both exact matches
// (data-tool) and prefix matches for key/modifier syntax (data-tool:plugin,
// data-tool__mod).
func isAllowedCustomAttr(allowed map[string]bool, baseAttr string) bool {
	if allowed[baseAttr] {
		return true
	}
	for prefix := range allowed {
		if strings.HasPrefix(baseAttr, prefix+":") || strings.HasPrefix(baseAttr, prefix+"__") {
			return true
		}
	}
	return false
}

// loadAllowedAttrs discovers the config file (either explicit path or auto-
// discovered from root) and returns the set of allowed custom attribute names.
// Returns nil when no config file is found or usable (caller treats nil as
// empty set).
func loadAllowedAttrs(root, configPath string) map[string]bool {
	cfg, _ := loadConfig(root, configPath)
	if cfg == nil {
		return nil
	}
	allowed := make(map[string]bool, len(cfg.Attributes.Allowed))
	for _, a := range cfg.Attributes.Allowed {
		allowed[strings.ToLower(a)] = true
	}
	return allowed
}

// loadConfig resolves and parses the project config once, returning nil when
// there is none or it is unreadable (a warning is printed, matching the
// attribute-list behaviour: a broken config must not stop a lint run).
func loadConfig(root, configPath string) (*lintConfigFile, error) {
	path := configPath
	if path == "" {
		if stat, err := os.Stat(root); err == nil && !stat.IsDir() {
			root = filepath.Dir(root)
		}
		path = findConfig(root)
	}
	if path == "" {
		return nil, nil
	}
	cfg, err := loadConfigFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load config %s: %v\n", path, err)
		return nil, err
	}
	return cfg, nil
}
