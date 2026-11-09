package measure

import "sort"

// BenchHistory returns benchmark records for target, oldest first. An empty
// target matches every benchmark; a non-empty target matches either the
// recorded target name or the model ID. A positive limit keeps the newest
// records. The returned records always have a non-nil Bench payload.
func (s *Store) BenchHistory(target string, limit int) ([]Record, error) {
	records, err := s.All()
	if err != nil {
		return nil, err
	}
	out := FilterBench(records, target)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// FilterBench keeps benchmark records, optionally for one target. The target
// may be a preset name, a model ID, or the explicit model ID used with --url.
func FilterBench(records []Record, target string) []Record {
	out := make([]Record, 0, len(records))
	for _, r := range records {
		if r.Kind != KindBench || r.Bench == nil {
			continue
		}
		if target != "" && r.Target != target && r.ModelID != target {
			continue
		}
		out = append(out, r)
	}
	return out
}

// BenchTargets returns the sorted, unique target names in a set of records.
// The model ID is used when a record has no target (older or hand-written
// records).
func BenchTargets(records []Record) []string {
	seen := make(map[string]bool)
	var out []string
	for _, r := range records {
		if r.Kind != KindBench || r.Bench == nil {
			continue
		}
		name := BenchTargetName(r)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// BenchTargetName is the label shown for a benchmark record.
func BenchTargetName(r Record) string {
	if r.Target != "" {
		return r.Target
	}
	return r.ModelID
}
