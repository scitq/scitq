package server

import "github.com/lib/pq"

// curveToPG marshals a TaskRequest curve ([]float32 over the wire) into a
// pq.Array of float64 so the postgres driver writes it as a
// `double precision[]`. nil/empty curves return nil so the SQL layer
// stores NULL — distinguished from `[]` in PG and meaning "no curve
// declared on this task" to the assignment & retry paths.
//
// Spec: addition_from_nextflow.md A (per-attempt resource curves).
func curveToPG(curve []float32) interface{} {
	if len(curve) == 0 {
		return nil
	}
	out := make([]float64, len(curve))
	for i, v := range curve {
		out[i] = float64(v)
	}
	return pq.Array(out)
}

// curveAtAttempt returns the curve element at the given attempt index,
// clamped to the last value when `attempt` overshoots the curve length.
// An empty / nil curve returns 0 and false so callers can distinguish
// "no curve" from "curve value is zero".
//
// Used by the retry-decision logic to compute the next attempt's
// min_cpu / min_mem / min_disk: a failed task's clone reads the parent's
// curve and shifts to curve[attempt+1] (unless failure_class blocks the
// advance — eviction in particular).
func curveAtAttempt(curve []float64, attempt int) (float64, bool) {
	if len(curve) == 0 {
		return 0, false
	}
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(curve) {
		attempt = len(curve) - 1
	}
	return curve[attempt], true
}
