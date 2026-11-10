package measure

import (
	"testing"
	"time"
)

func obsRecord(shape Record, observed, estimate uint64) Record {
	shape.ObservedBytes = observed
	shape.EstimateBytes = estimate
	shape.BaselineKnown = true
	return shape
}

func TestCalibrationNeedsThreeSamples(t *testing.T) {
	s := testStore(t)
	base := Record{ModelID: "m", Ctx: 4096, CacheTypeK: "f16", CacheTypeV: "f16", GPULayers: 999}
	for i := 0; i < 2; i++ {
		if err := s.Append(obsRecord(base, 6<<30, 5<<30)); err != nil {
			t.Fatal(err)
		}
	}
	cal, ok := s.Calibration(base.Shape(), Identity{})
	if !ok {
		t.Fatal("calibration should be reported below the threshold")
	}
	if cal.Samples != 2 {
		t.Fatalf("samples = %d, want 2", cal.Samples)
	}
	if cal.OK() {
		t.Fatal("two samples must not correct an estimate")
	}
	if got := cal.Corrected(5 << 30); got != 5<<30 {
		t.Fatalf("corrected below threshold = %d, want raw %d", got, uint64(5<<30))
	}
}

func TestCalibrationConvergesOnMedian(t *testing.T) {
	s := testStore(t)
	base := Record{ModelID: "m", Ctx: 4096, CacheTypeK: "f16", CacheTypeV: "f16", GPULayers: 999}
	// Five samples with one high outlier; the median stays at 1.05.
	observations := []uint64{
		5<<30 + 256<<20, // 1.05
		5 << 30,         // 1.00
		5<<30 + 512<<20, // 1.10
		8 << 30,         // 1.60 outlier
		5<<30 + 256<<20, // 1.05
	}
	for _, observed := range observations {
		if err := s.Append(obsRecord(base, observed, 5<<30)); err != nil {
			t.Fatal(err)
		}
	}
	cal, ok := s.Calibration(base.Shape(), Identity{})
	if !ok {
		t.Fatal("no calibration")
	}
	if cal.Samples != 5 {
		t.Fatalf("samples = %d, want 5", cal.Samples)
	}
	if !cal.OK() {
		t.Fatal("five samples should correct an estimate")
	}
	if cal.MedianRatio < 1.04 || cal.MedianRatio > 1.06 {
		t.Fatalf("median ratio = %.3f, want ~1.05", cal.MedianRatio)
	}
	if cal.Ratio != cal.MedianRatio || cal.Clamped {
		t.Fatalf("ratio = %.3f clamped=%v, want unclamped median", cal.Ratio, cal.Clamped)
	}
	if got := cal.Corrected(5 << 30); got < 5<<30+200<<20 || got > 5<<30+400<<20 {
		t.Fatalf("corrected = %d, want ~5.25 GiB", got)
	}
	if cal.Spread <= 0 || cal.Spread > 0.6 {
		t.Fatalf("spread = %.3f, want a small positive range", cal.Spread)
	}
}

func TestCalibrationOutlierDoesNotDominate(t *testing.T) {
	s := testStore(t)
	base := Record{ModelID: "m", Ctx: 4096}
	for _, observed := range []uint64{4 << 30, 4 << 30, 4 << 30, 100 << 30} {
		if err := s.Append(obsRecord(base, observed, 4<<30)); err != nil {
			t.Fatal(err)
		}
	}
	cal, ok := s.Calibration(base.Shape(), Identity{})
	if !ok {
		t.Fatal("no calibration")
	}
	if cal.MedianRatio != 1 || cal.Ratio != 1 {
		t.Fatalf("outlier skewed the ratio: median=%.3f ratio=%.3f", cal.MedianRatio, cal.Ratio)
	}
}

func TestCalibrationClampsBothDirections(t *testing.T) {
	low := testStore(t)
	lowBase := Record{ModelID: "low", Ctx: 4096}
	for _, observed := range []uint64{1 << 30, 2 << 30, 2 << 30} {
		if err := low.Append(obsRecord(lowBase, observed, 10<<30)); err != nil {
			t.Fatal(err)
		}
	}
	cal, ok := low.Calibration(lowBase.Shape(), Identity{})
	if !ok {
		t.Fatal("no low calibration")
	}
	if cal.Ratio != MinCalibrationRatio || !cal.Clamped {
		t.Fatalf("low ratio = %.2f clamped=%v, want %.2f clamped", cal.Ratio, cal.Clamped, MinCalibrationRatio)
	}
	if got := cal.Corrected(10 << 30); got != 5<<30 {
		t.Fatalf("low corrected = %d, want clamped 5 GiB", got)
	}

	high := testStore(t)
	highBase := Record{ModelID: "high", Ctx: 4096}
	for _, observed := range []uint64{30 << 30, 40 << 30, 50 << 30} {
		if err := high.Append(obsRecord(highBase, observed, 10<<30)); err != nil {
			t.Fatal(err)
		}
	}
	cal, ok = high.Calibration(highBase.Shape(), Identity{})
	if !ok {
		t.Fatal("no high calibration")
	}
	if cal.Ratio != MaxCalibrationRatio || !cal.Clamped {
		t.Fatalf("high ratio = %.2f clamped=%v, want %.2f clamped", cal.Ratio, cal.Clamped, MaxCalibrationRatio)
	}
	if got := cal.Corrected(10 << 30); got != 20<<30 {
		t.Fatalf("high corrected = %d, want clamped 20 GiB", got)
	}
}

func TestCalibrationExpiresStaleSamples(t *testing.T) {
	base := Record{
		ModelID: "m", Ctx: 4096, GPULayers: 999,
		ModelSize: 8 << 30, ModelModTime: time.Unix(1_700_000_000, 0),
		GPUPCIID: "0000:01:00.0", GPUDriver: "550.54", LlamaServerVersion: "b4000",
	}
	cases := []struct {
		name string
		id   Identity
	}{
		{"model size", Identity{ModelSize: 9 << 30}},
		{"model modtime", Identity{ModelModTime: time.Unix(1_800_000_000, 0)}},
		{"gpu", Identity{GPUPCIID: "0000:02:00.0"}},
		{"driver", Identity{GPUDriver: "560.28"}},
		{"llama-server", Identity{LlamaServerVersion: "b4100"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			for i := 0; i < 4; i++ {
				if err := s.Append(obsRecord(base, 9<<30, 8<<30)); err != nil {
					t.Fatal(err)
				}
			}
			cal, ok := s.Calibration(base.Shape(), tc.id)
			if !ok {
				t.Fatal("expired samples should still be reported")
			}
			if cal.Samples != 0 || cal.Expired != 4 {
				t.Fatalf("calibration = %+v, want 4 expired", cal)
			}
			if cal.OK() {
				t.Fatal("expired samples must not correct an estimate")
			}
			if got := cal.Corrected(8 << 30); got != 8<<30 {
				t.Fatalf("expired corrected = %d, want raw", got)
			}
		})
	}
}

func TestCalibrationUnknownIdentityDoesNotExpire(t *testing.T) {
	s := testStore(t)
	base := Record{
		ModelID: "m", Ctx: 4096,
		ModelSize: 8 << 30, GPUDriver: "550.54", LlamaServerVersion: "b4000",
	}
	for i := 0; i < 3; i++ {
		if err := s.Append(obsRecord(base, 9<<30, 8<<30)); err != nil {
			t.Fatal(err)
		}
	}
	cal, ok := s.Calibration(base.Shape(), Identity{})
	if !ok || cal.Expired != 0 || cal.Samples != 3 {
		t.Fatalf("unknown identity expired samples: %+v ok=%v", cal, ok)
	}
}

func TestCalibrationUnknownBaseline(t *testing.T) {
	s := testStore(t)
	base := Record{ModelID: "m", Ctx: 4096}
	for i := 0; i < 3; i++ {
		r := base
		r.BaselineKnown = false
		r.ObservedBytes = 9 << 30
		r.EstimateBytes = 8 << 30
		if err := s.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	cal, ok := s.Calibration(base.Shape(), Identity{})
	if !ok {
		t.Fatal("unknown baseline should be reported")
	}
	if cal.Samples != 0 || cal.Unknown != 3 || cal.OK() {
		t.Fatalf("calibration = %+v, want 3 unknown", cal)
	}
}

func TestCalibrationIgnoresOtherShapesAndBenchmarks(t *testing.T) {
	s := testStore(t)
	base := Record{ModelID: "m", Ctx: 4096, CacheTypeK: "q4_0", CacheTypeV: "q4_0", GPULayers: 999}
	for i := 0; i < 3; i++ {
		if err := s.Append(obsRecord(base, 5<<30, 4<<30)); err != nil {
			t.Fatal(err)
		}
	}
	other := base
	other.Ctx = 8192
	if err := s.Append(obsRecord(other, 99<<30, 4<<30)); err != nil {
		t.Fatal(err)
	}
	bench := obsRecord(base, 100<<30, 4<<30)
	bench.Kind = KindBench
	if err := s.Append(bench); err != nil {
		t.Fatal(err)
	}

	cal, ok := s.Calibration(base.Shape(), Identity{})
	if !ok {
		t.Fatal("no calibration")
	}
	if cal.Samples != 3 || cal.MedianRatio != 1.25 {
		t.Fatalf("calibration = %+v, want only the three matching observations", cal)
	}
}

func TestCalibrationEmptyStore(t *testing.T) {
	s := testStore(t)
	if cal, ok := s.Calibration(Shape{ModelID: "m"}, Identity{}); ok {
		t.Fatalf("empty store returned %+v", cal)
	}
}
