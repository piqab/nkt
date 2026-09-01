// Package tui is the terminal interface: the same inventory, analysis and
// control plane the web dashboard uses, driven from an SSH session without a
// browser.
package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

// The palette mirrors the web interface so both surfaces describe severity and
// state with the same colours. Values are the dark-surface steps: a terminal is
// a dark surface far more often than not.
const (
	hexSeries1 = "#3987e5" // blue
	hexSeries2 = "#d95926" // orange
	hexSeries3 = "#199e70" // aqua
	hexSeries4 = "#c98500" // yellow
	hexSeries5 = "#d55181" // magenta

	hexGood     = "#0ca30c"
	hexWarning  = "#fab219"
	hexSerious  = "#ec835a"
	hexCritical = "#d03b3b"

	hexText      = "#ffffff"
	hexSecondary = "#c3c2b7"
	hexMuted     = "#898781"
	hexBorder    = "#383835"
)

// sequential ramp, light to dark, for magnitude in heatmaps.
var seqRamp = []string{
	"#cde2fb", "#9ec5f4", "#6da7ec", "#3987e5", "#256abf", "#184f95", "#0d366b",
}

var (
	colorText      = tcell.GetColor(hexText)
	colorSecondary = tcell.GetColor(hexSecondary)
	colorMuted     = tcell.GetColor(hexMuted)
	colorBorder    = tcell.GetColor(hexBorder)
	colorAccent    = tcell.GetColor(hexSeries1)
	colorGood      = tcell.GetColor(hexGood)
	colorWarning   = tcell.GetColor(hexWarning)
	colorCritical  = tcell.GetColor(hexCritical)
)

// severityColor maps a finding severity onto the reserved status palette.
func severityColor(severity string) string {
	switch severity {
	case model.SeverityCritical:
		return hexCritical
	case model.SeverityHigh:
		return hexSerious
	case model.SeverityMedium:
		return hexWarning
	case model.SeverityLow:
		return hexSeries1
	default:
		return hexMuted
	}
}

// severityLabel is the word shown beside the colour, so severity is never
// carried by colour alone.
func severityLabel(lang msgs.Lang, severity string) string {
	switch severity {
	case model.SeverityCritical:
		return msgs.T(lang, "tui.severity.critical")
	case model.SeverityHigh:
		return msgs.T(lang, "tui.severity.high")
	case model.SeverityMedium:
		return msgs.T(lang, "tui.severity.medium")
	case model.SeverityLow:
		return msgs.T(lang, "tui.severity.low")
	default:
		return msgs.T(lang, "tui.severity.info")
	}
}

// stateColor maps a service or container state onto the status palette.
func stateColor(state string) string {
	switch state {
	case "active", "running", "ok":
		return hexGood
	case "failed", "restarting", "dead", "error":
		return hexCritical
	case "inactive", "exited", "declared", "warn":
		return hexWarning
	default:
		return hexMuted
	}
}

// tag wraps text in a tview colour tag.
func tag(hex, text string) string { return fmt.Sprintf("[%s]%s[-]", hex, text) }

// dim renders secondary text.
func dim(text string) string { return tag(hexMuted, text) }

// bold renders emphasised text.
func bold(text string) string { return fmt.Sprintf("[::b]%s[::-]", text) }
