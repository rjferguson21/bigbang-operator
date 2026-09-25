package generator

import "sort"

// Generic one-liners only. Anything with domain meaning (names, metadata,
// netpol shapes) belongs in its concern's file, not here.

// sortedKeys lets the generator emit resources in deterministic order so
// goldens compare cleanly across runs.
func sortedKeys[M ~map[string]V, V any](m M) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func ptr[T any](v T) *T { return &v }
