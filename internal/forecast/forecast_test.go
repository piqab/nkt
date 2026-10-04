package forecast

import (
	"math"
	"testing"
)

func TestFitLinearWithOutliers(t *testing.T) {
	var ps []Point
	for i := 0; i < 30; i++ {
		v := 50 + 2*float64(i)
		if i == 10 || i == 20 {
			v += 40 // выбросы
		}
		ps = append(ps, Point{T: float64(i), V: v})
	}
	tr, ok := Fit(ps)
	if !ok || math.Abs(tr.Slope-2) > 0.01 || math.Abs(tr.Now-108) > 0.5 || tr.Span != 29 {
		t.Fatalf("%+v", tr)
	}
	if tr.Monotonic < 0.85 {
		t.Fatalf("monotonic %v", tr.Monotonic)
	}
	d, ok := tr.DaysUntil(128)
	if !ok || math.Abs(d-10) > 0.3 {
		t.Fatalf("days %v %v", d, ok)
	}
	if _, ok := tr.DaysUntil(100); ok {
		t.Fatal("already past the limit")
	}
}

func TestFitFlatAndShort(t *testing.T) {
	if _, ok := Fit([]Point{{0, 1}, {1, 2}}); ok {
		t.Fatal("two points")
	}
	var ps []Point
	for i := 0; i < 50; i++ {
		ps = append(ps, Point{T: float64(i) / 24, V: 10 + math.Sin(float64(i))})
	}
	tr, ok := Fit(ps)
	if !ok || math.Abs(tr.Slope) > 1 {
		t.Fatalf("flat: %+v", tr)
	}
	if _, ok := (Trend{Slope: -1, Now: 5}).DaysUntil(10); ok {
		t.Fatal("falling trend reaches the limit")
	}
}

func TestThinKeepsEnds(t *testing.T) {
	var ps []Point
	for i := 0; i < 1000; i++ {
		ps = append(ps, Point{T: float64(i), V: float64(i)})
	}
	got := thin(ps, 400)
	if len(got) != 400 || got[0].T != 0 || got[399].T != 999 {
		t.Fatalf("%d %v %v", len(got), got[0], got[len(got)-1])
	}
}
