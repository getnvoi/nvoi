package utils

import "sort"

// SortedKeys returns the keys of a map sorted alphabetically.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// RemovedKeys returns keys present in prev but absent in current.
func RemovedKeys[V any](prev, current map[string]V) []string {
	var removed []string
	for k := range prev {
		if _, ok := current[k]; !ok {
			removed = append(removed, k)
		}
	}
	return removed
}

// ReverseSorted sorts strings and returns them in reverse order.
func ReverseSorted(s []string) []string {
	sort.Sort(sort.Reverse(sort.StringSlice(s)))
	return s
}
