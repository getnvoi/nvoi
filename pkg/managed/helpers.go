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

// requireEnv looks up a required credential key from the env.
// Returns a hard error with a clear message if missing.
func requireEnv(env map[string]string, key, kind, name string) (string, error) {
	val, ok := env[key]
	if !ok || val == "" {
		return "", fmt.Errorf("managed %s %q: missing required credential %s (env: %s)", kind, name, key, key)
	}
	return val, nil
}
