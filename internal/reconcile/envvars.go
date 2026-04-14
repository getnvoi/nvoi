package reconcile

import (
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/kube"
)

// envRefPattern matches $VAR or ${VAR} where VAR is a valid env var name.
// Returns false for things like $100 (digit after $).
func hasVarRef(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) {
			next := s[i+1]
			if next == '{' || isVarStart(next) {
				return true
			}
		}
	}
	return false
}

func isVarStart(b byte) bool { return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '_' }
func isVarChar(b byte) bool  { return isVarStart(b) || (b >= '0' && b <= '9') }

// resolveRef replaces $VAR and ${VAR} references in val with values from sources.
// No $ → returns val unchanged. Unknown $VAR → error.
func resolveRef(val string, sources map[string]string) (string, error) {
	if !hasVarRef(val) {
		return val, nil
	}

	var b strings.Builder
	i := 0
	for i < len(val) {
		if val[i] != '$' || i+1 >= len(val) {
			b.WriteByte(val[i])
			i++
			continue
		}
		next := val[i+1]

		// ${VAR} form
		if next == '{' {
			end := strings.IndexByte(val[i+2:], '}')
			if end < 0 {
				return "", fmt.Errorf("unclosed ${")
			}
			varName := val[i+2 : i+2+end]
			v, ok := sources[varName]
			if !ok {
				return "", fmt.Errorf("$%s is not a known env var", varName)
			}
			b.WriteString(v)
			i = i + 2 + end + 1
			continue
		}

		// $VAR form — VAR must start with letter or underscore
		if isVarStart(next) {
			j := i + 2
			for j < len(val) && isVarChar(val[j]) {
				j++
			}
			varName := val[i+1 : j]
			v, ok := sources[varName]
			if !ok {
				return "", fmt.Errorf("$%s is not a known env var", varName)
			}
			b.WriteString(v)
			i = j
			continue
		}

		// $ followed by non-var char (e.g., $100) — literal
		b.WriteByte(val[i])
		i++
	}
	return b.String(), nil
}

// resolveEntry parses a KEY=VALUE or KEY entry and resolves $VAR references.
// Same function for env: and secrets: fields.
func resolveEntry(entry string, sources map[string]string) (key, value string, err error) {
	key, raw := kube.ParseSecretRef(entry)
	value, err = resolveRef(raw, sources)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", key, err)
	}
	return key, value, nil
}
