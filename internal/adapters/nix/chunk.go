package nix

// chunk splits xs into batches of at most size. A size of zero or less means
// one batch: the caller has said it does not want batching, and silently
// choosing a bound for it would be worse than doing what it asked.
//
// Batching exists because peak memory is a property of ONE nix process, not of
// the work. A whole ring in a single invocation loads every host's toplevel
// into one evaluator, and on 2026-08-01 that took the gate down three times -
// which stops every path that commits configuration, because they all run
// through it.
func chunk(xs []string, size int) [][]string {
	if size <= 0 || len(xs) <= size {
		return [][]string{xs}
	}
	out := make([][]string, 0, (len(xs)+size-1)/size)
	for start := 0; start < len(xs); start += size {
		end := start + size
		if end > len(xs) {
			end = len(xs)
		}
		out = append(out, xs[start:end])
	}
	return out
}
