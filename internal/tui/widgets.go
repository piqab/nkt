package tui

import (
	"fmt"
	"strings"

	"github.com/althq/netknownsthat/internal/store"
)

// Terminal chart primitives. The rules are the same as in the web interface:
// one sequential hue for magnitude, status colours reserved for state, a legend
// whenever more than one series is on screen, and never colour alone.

// sparkChars are the eight block levels used by the sparkline.
var sparkChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparkline renders a series as one line of block characters.
func sparkline(values []float64, width int) string {
	if len(values) == 0 || width <= 0 {
		return dim("нет данных")
	}
	// Take the tail that fits, so the most recent points are always visible.
	if len(values) > width {
		values = values[len(values)-width:]
	}

	min, max := values[0], values[0]
	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	span := max - min
	if span == 0 {
		span = 1
	}

	var sb strings.Builder
	for _, v := range values {
		idx := int((v - min) / span * float64(len(sparkChars)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparkChars) {
			idx = len(sparkChars) - 1
		}
		sb.WriteRune(sparkChars[idx])
	}
	return sb.String()
}

// hbar renders a horizontal bar of the given fraction (0..1).
func hbar(fraction float64, width int, hex string) string {
	if width <= 0 {
		return ""
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction*float64(width) + 0.5)
	return tag(hex, strings.Repeat("█", filled)) + dim(strings.Repeat("░", width-filled))
}

// dowLabels indexes SQLite's %w, which starts the week on Sunday.
var dowLabels = [7]string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}

// heatmap renders a week of hourly cells: seven rows of twenty-four columns,
// magnitude on the sequential ramp. Cells with no measurement stay blank rather
// than pretending to be zero.
func heatmap(cells []store.HeatCell, valueOf func(store.HeatCell) float64, scaleLabel string,
	format func(float64) string) string {

	if len(cells) == 0 {
		return "\n " + dim("Измерений за период нет.") +
			"\n " + dim("Историю собирает фоновый планировщик — запустите сервис netknownsthat.")
	}

	type key struct{ dow, hour int }
	grid := map[key]float64{}
	present := map[key]bool{}
	max := 0.0
	for _, c := range cells {
		k := key{c.DOW, c.Hour}
		v := valueOf(c)
		grid[k] = v
		present[k] = true
		if v > max {
			max = v
		}
	}
	if max == 0 {
		max = 1
	}

	var sb strings.Builder
	sb.WriteString("     ")
	for h := 0; h < 24; h++ {
		if h%3 == 0 {
			sb.WriteString(dim(fmt.Sprintf("%-6d", h)))
		}
	}
	sb.WriteString("\n")

	for d := 0; d < 7; d++ {
		sb.WriteString(dim(fmt.Sprintf(" %s  ", dowLabels[d])))
		for h := 0; h < 24; h++ {
			k := key{d, h}
			if !present[k] {
				sb.WriteString(dim("··"))
				continue
			}
			ratio := grid[k] / max
			step := int(ratio * float64(len(seqRamp)-1))
			if step < 0 {
				step = 0
			}
			if step >= len(seqRamp) {
				step = len(seqRamp) - 1
			}
			sb.WriteString(fmt.Sprintf("[:%s]  [:-]", seqRamp[step]))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n " + dim(scaleLabel+": меньше "))
	for _, hex := range seqRamp {
		sb.WriteString(fmt.Sprintf("[:%s]  [:-]", hex))
	}
	sb.WriteString(dim(fmt.Sprintf(" больше   (максимум %s, ·· — измерений не было)", format(max))))
	return sb.String()
}

// statTile renders one labelled figure with an optional note.
func statTile(label, value, note, hex string) string {
	out := dim(label) + "\n " + bold(tag(hex, value))
	if note != "" {
		out += "\n " + dim(note)
	}
	return out
}

// --------------------------------------------------------------------- format

func formatBytes(n float64) string {
	if n == 0 {
		return "0 Б"
	}
	units := []string{"Б", "КБ", "МБ", "ГБ", "ТБ", "ПБ"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 || n >= 100 {
		return fmt.Sprintf("%.0f %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

func formatMS(n float64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.2f с", n/1000)
	}
	if n < 10 {
		return fmt.Sprintf("%.2f мс", n)
	}
	return fmt.Sprintf("%.0f мс", n)
}

func formatCount(n float64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", n/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", n/1000)
	default:
		return fmt.Sprintf("%.0f", n)
	}
}

// truncate shortens a string to width runes, marking the cut.
func truncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}
