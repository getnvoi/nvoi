package managed

import (
	"fmt"
	"sort"
	"strings"
)

func namespaced(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ErrMissingCredential is returned when a required credential is not in the env.
// Typed so callers can distinguish from compilation errors (e.g. API returns 400 not 500).
type ErrMissingCredential struct {
	Kind string
	Name string
	Key  string
}

func (e *ErrMissingCredential) Error() string {
	return fmt.Sprintf("managed %s %q: missing required credential %s", e.Kind, e.Name, e.Key)
}

// requireEnv looks up a required credential key from the env.
// Returns ErrMissingCredential if missing.
func requireEnv(env map[string]string, key, kind, name string) (string, error) {
	val, ok := env[key]
	if !ok || val == "" {
		return "", &ErrMissingCredential{Kind: kind, Name: name, Key: key}
	}
	return val, nil
}
