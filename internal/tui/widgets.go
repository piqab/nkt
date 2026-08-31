package tui

import (
	"fmt"
	"strings"

	"github.com/althq/netknownsthat/internal/msgs"
	"github.com/althq/netknownsthat/internal/store"
)

// Terminal chart primitives. The rules are the same as in the web interface:
// one sequential hue for magnitude, status colours reserved for state, a legend
// whenever more than one series is on screen, and never colour alone.

// sparkChars are the eight block levels used by the sparkline.
var sparkChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparkline renders a series as one line of block characters.
func sparkline(lang msgs.Lang, values []float64, width int) string {
	if len(values) == 0 || width <= 0 {
		return dim(msgs.T(lang, "tui.noData"))
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

// dowLabelsRU indexes SQLite's %w, which starts the week on Sunday.
var dowLabelsRU = [7]string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}
var dowLabelsEN = [7]string{"Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"}

func dowLabels(lang msgs.Lang) [7]string {
	if lang == msgs.EN {
		return dowLabelsEN
	}
	return dowLabelsRU
}

// heatmap renders a week of hourly cells: seven rows of twenty-four columns,
// magnitude on the sequential ramp. Cells with no measurement stay blank rather
// than pretending to be zero.
func heatmap(lang msgs.Lang, cells []store.HeatCell, valueOf func(store.HeatCell) float64, scaleLabel string,
	format func(float64) string) string {

	if len(cells) == 0 {
		return "\n " + dim(msgs.T(lang, "tui.noMeasurementsInPeriod")) +
			"\n " + dim(msgs.T(lang, "tui.historyCollectedByScheduler"))
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

	labels := dowLabels(lang)
	for d := 0; d < 7; d++ {
		sb.WriteString(dim(fmt.Sprintf(" %s  ", labels[d])))
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

	sb.WriteString("\n " + dim(scaleLabel+": "+msgs.T(lang, "tui.heatmapLess")+" "))
	for _, hex := range seqRamp {
		sb.WriteString(fmt.Sprintf("[:%s]  [:-]", hex))
	}
	sb.WriteString(dim(" " + msgs.T(lang, "tui.heatmapMore", format(max))))
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

var byteUnitsRU = []string{"Б", "КБ", "МБ", "ГБ", "ТБ", "ПБ"}
var byteUnitsEN = []string{"B", "KB", "MB", "GB", "TB", "PB"}

func formatBytes(lang msgs.Lang, n float64) string {
	units := byteUnitsRU
	if lang == msgs.EN {
		units = byteUnitsEN
	}
	if n == 0 {
		return "0 " + units[0]
	}
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

func formatMS(lang msgs.Lang, n float64) string {
	sUnit, msUnit := "с", "мс"
	if lang == msgs.EN {
		sUnit, msUnit = "s", "ms"
	}
	if n >= 1000 {
		return fmt.Sprintf("%.2f %s", n/1000, sUnit)
	}
	if n < 10 {
		return fmt.Sprintf("%.2f %s", n, msUnit)
	}
	return fmt.Sprintf("%.0f %s", n, msUnit)
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
