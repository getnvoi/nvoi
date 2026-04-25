package reconcile

import (
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// hasVarRef is a thin alias over utils.HasVarRef — kept for readability
// in the reconcile call sites. Parsing rules live in pkg/utils so
// cmd/cli/ci.go (which also needs to enumerate $VAR references) shares
// the same definition of what counts as a reference.
func hasVarRef(s string) bool { return utils.HasVarRef(s) }

// extractVarRefs is a thin alias over utils.ExtractVarRefs. Same
// rationale as hasVarRef — reconcile-side callers keep the short name,
// the parsing itself lives in the one place.
func extractVarRefs(s string) []string { return utils.ExtractVarRefs(s) }

// resolveRef replaces $VAR and ${VAR} references in val with values from sources.
// No $ → returns val unchanged. Unknown $VAR → error.
func resolveRef(val string, sources map[string]string) (string, error) {
	if !utils.HasVarRef(val) {
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
		if utils.IsVarStart(next) {
			j := i + 2
			for j < len(val) && utils.IsVarChar(val[j]) {
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
