package registry

import (
	"os"

	"asare_poc/pkg/ledger"
	"gopkg.in/yaml.v3"
)

// Rule maps a forward action to its compensating (inverse) action template.
type Rule struct {
	Action  ActionPattern  `yaml:"action"`
	Inverse ActionTemplate `yaml:"inverse"`
}

// ActionPattern identifies a forward call by HTTP method + URL path suffix.
type ActionPattern struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
}

// ActionTemplate is the compensation to run; Body may contain $response.* placeholders.
type ActionTemplate struct {
	Method string         `yaml:"method"`
	Path   string         `yaml:"path"`
	Body   map[string]any `yaml:"body"`
}

type Registry struct {
	Rules []Rule `yaml:"rules"`
}

// Load reads a YAML inverse-action registry file.
func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	reg := &Registry{}
	if err := yaml.Unmarshal(data, reg); err != nil {
		return nil, err
	}
	return reg, nil
}

// Lookup finds the compensating action template for a forward call.
// Path matching is suffix-based (full URL like http://x/mock/stripe/charge
// matches pattern /mock/stripe/charge).
func (r *Registry) Lookup(method, fullURL string) (ledger.Action, bool) {
	for _, rule := range r.Rules {
		if rule.Action.Method != method {
			continue
		}
		if len(fullURL) >= len(rule.Action.Path) &&
			fullURL[len(fullURL)-len(rule.Action.Path):] == rule.Action.Path {
			return ledger.Action{
				Method: rule.Inverse.Method,
				URL:    rule.Inverse.Path,
				Body:   rule.Inverse.Body,
			}, true
		}
	}
	return ledger.Action{}, false
}
