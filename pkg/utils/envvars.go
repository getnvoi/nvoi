package utils

// ExtractVarRefs returns every `$VAR` / `${VAR}` name referenced in s,
// in order of appearance. A `$` followed by a digit (`$100`) or by any
// non-identifier character is ignored — it's a literal dollar.
//
// Single source of truth for `$VAR` parsing across the codebase:
// internal/reconcile uses it to resolve env entries at deploy time,
// cmd/cli/ci.go uses it to enumerate which env vars to port into the CI
// provider's secret store. Keeping the parser here means both paths
// agree on the shapes we accept — diverging rules (cmd/ thinks
// `${FOO_BAR}` is a var while reconcile doesn't) would be a silent
// source of wrong behavior between the laptop and the runner.
func ExtractVarRefs(s string) []string {
	var refs []string
	i := 0
	for i < len(s) {
		if s[i] != '$' || i+1 >= len(s) {
			i++
			continue
		}
		next := s[i+1]
		if next == '{' {
			end := indexByte(s[i+2:], '}')
			if end < 0 {
				return refs
			}
			refs = append(refs, s[i+2:i+2+end])
			i = i + 2 + end + 1
			continue
		}
		if IsVarStart(next) {
			j := i + 2
			for j < len(s) && IsVarChar(s[j]) {
				j++
			}
			refs = append(refs, s[i+1:j])
			i = j
			continue
		}
		i++
	}
	return refs
}

// HasVarRef reports whether s contains any `$VAR` / `${VAR}` reference.
// Cheaper than ExtractVarRefs when the caller only needs yes/no.
func HasVarRef(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) {
			next := s[i+1]
			if next == '{' || IsVarStart(next) {
				return true
			}
		}
	}
	return false
}

// IsVarStart reports whether b is a valid first char for a POSIX env
// var name (letter or underscore). Digits are rejected to avoid false
// positives on `$100`, `$9`, etc.
func IsVarStart(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '_'
}

// IsVarChar reports whether b is a valid continuation char for an env
// var name (letter, digit, or underscore).
func IsVarChar(b byte) bool {
	return IsVarStart(b) || (b >= '0' && b <= '9')
}

// indexByte inlines the tiny bytes.IndexByte we need. Avoids the
// import + function-call overhead in a hot parsing loop.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
