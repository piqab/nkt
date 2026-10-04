// Package forecast — простые и прозрачные прогнозы для «Мониторинга»
// хаба: устойчивый тренд (оценка Тейла — Сена: медиана наклонов всех
// пар точек, единичные выбросы её не сдвигают), доля шагов в сторону
// тренда (монотонность) и время до заполнения. Без внешних библиотек:
// у каждого прогноза видно, на чём он основан.
package forecast

import (
	"math"
	"sort"
)

// Point — значение V в момент T (дни от любого начала).
type Point struct {
	T float64
	V float64
}

// Trend — наклон (единиц в день), значение на последней точке по
// тренду, монотонность и охват.
type Trend struct {
	Slope float64
	// Now — значение тренда в последней точке.
	Now float64
	// Monotonic — доля шагов между соседними точками в сторону наклона
	// (0…1): 1 — растёт без откатов.
	Monotonic float64
	// Span — сколько дней охватывают точки.
	Span float64
	N    int
}

// maxPairs — сколько точек брать для O(n²) оценки (остальные
// прореживаются равномерно).
const maxPairs = 400

// Fit — тренд по точкам (по времени). ok=false — точек меньше трёх или
// все в один момент.
func Fit(ps []Point) (Trend, bool) {
	pts := thin(sortByT(ps), maxPairs)
	n := len(pts)
	if n < 3 || pts[n-1].T-pts[0].T <= 0 {
		return Trend{}, false
	}
	slopes := make([]float64, 0, n*(n-1)/2)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if dt := pts[j].T - pts[i].T; dt > 0 {
				slopes = append(slopes, (pts[j].V-pts[i].V)/dt)
			}
		}
	}
	if len(slopes) == 0 {
		return Trend{}, false
	}
	slope := median(slopes)
	icepts := make([]float64, n)
	for i, p := range pts {
		icepts[i] = p.V - slope*p.T
	}
	b := median(icepts)
	up, steps := 0, 0
	for i := 1; i < n; i++ {
		d := pts[i].V - pts[i-1].V
		if d == 0 {
			continue
		}
		steps++
		if (d > 0) == (slope > 0) {
			up++
		}
	}
	mono := 0.0
	if steps > 0 {
		mono = float64(up) / float64(steps)
	}
	last := pts[n-1].T
	return Trend{Slope: slope, Now: slope*last + b, Monotonic: mono, Span: last - pts[0].T, N: n}, true
}

// DaysUntil — через сколько дней тренд дойдёт до limit (ok=false — не
// растёт к нему или уже там).
func (t Trend) DaysUntil(limit float64) (float64, bool) {
	if t.Slope <= 0 || t.Now >= limit {
		return 0, false
	}
	return (limit - t.Now) / t.Slope, true
}

// Mean — среднее значений.
func Mean(ps []Point) float64 {
	if len(ps) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, p := range ps {
		s += p.V
	}
	return s / float64(len(ps))
}

func median(v []float64) float64 {
	sort.Float64s(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}

func sortByT(ps []Point) []Point {
	out := append([]Point(nil), ps...)
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}

// thin — не больше max точек, равномерно, первая и последняя остаются.
func thin(ps []Point, max int) []Point {
	if len(ps) <= max {
		return ps
	}
	out := make([]Point, 0, max)
	step := float64(len(ps)-1) / float64(max-1)
	for i := 0; i < max; i++ {
		out = append(out, ps[int(math.Round(float64(i)*step))])
	}
	return out
}
