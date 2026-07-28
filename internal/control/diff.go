package control

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// diffLineLimit caps the quadratic LCS below. Config files are far smaller than
// this; anything bigger falls back to a plain summary instead of hanging.
const diffLineLimit = 4000

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// UnifiedDiff renders a standard unified diff with three lines of context.
func UnifiedDiff(fromName, toName, from, to string) string {
	a := splitLines(from)
	b := splitLines(to)

	if len(a) > diffLineLimit || len(b) > diffLineLimit {
		return fmt.Sprintf("--- %s (%d строк)\n+++ %s (%d строк)\n"+
			"Файлы слишком велики для построчного сравнения.\n", fromName, len(a), toName, len(b))
	}

	ops := diffOps(a, b)
	hunks := groupHunks(ops, 3)
	if len(hunks) == 0 {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", fromName, toName)
	for _, h := range hunks {
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", h.fromStart, h.fromCount, h.toStart, h.toCount)
		for _, op := range h.ops {
			switch op.kind {
			case opEqual:
				sb.WriteString(" " + op.text + "\n")
			case opDelete:
				sb.WriteString("-" + op.text + "\n")
			case opInsert:
				sb.WriteString("+" + op.text + "\n")
			}
		}
	}
	return sb.String()
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

type opKind int

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

type diffOp struct {
	kind opKind
	text string
	aIdx int // 1-based line number in the "from" side, 0 when inserted
	bIdx int // 1-based line number in the "to" side, 0 when deleted
}

// diffOps computes a line diff via the classic LCS table. Config files are
// small enough that the quadratic cost is irrelevant and the result is optimal.
func diffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{opEqual, a[i], i + 1, j + 1})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{opDelete, a[i], i + 1, 0})
			i++
		default:
			ops = append(ops, diffOp{opInsert, b[j], 0, j + 1})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{opDelete, a[i], i + 1, 0})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{opInsert, b[j], 0, j + 1})
	}
	return ops
}

type hunk struct {
	fromStart, fromCount int
	toStart, toCount     int
	ops                  []diffOp
}

// groupHunks keeps `context` unchanged lines around each run of changes.
func groupHunks(ops []diffOp, context int) []hunk {
	changed := make([]bool, len(ops))
	any := false
	for i, op := range ops {
		if op.kind != opEqual {
			changed[i] = true
			any = true
		}
	}
	if !any {
		return nil
	}

	keep := make([]bool, len(ops))
	for i, c := range changed {
		if !c {
			continue
		}
		lo, hi := i-context, i+context
		if lo < 0 {
			lo = 0
		}
		if hi >= len(ops) {
			hi = len(ops) - 1
		}
		for k := lo; k <= hi; k++ {
			keep[k] = true
		}
	}

	var hunks []hunk
	i := 0
	for i < len(ops) {
		if !keep[i] {
			i++
			continue
		}
		start := i
		for i < len(ops) && keep[i] {
			i++
		}
		block := ops[start:i]

		h := hunk{ops: block}
		for _, op := range block {
			if op.aIdx > 0 {
				if h.fromStart == 0 {
					h.fromStart = op.aIdx
				}
				h.fromCount++
			}
			if op.bIdx > 0 {
				if h.toStart == 0 {
					h.toStart = op.bIdx
				}
				h.toCount++
			}
		}
		hunks = append(hunks, h)
	}
	return hunks
}
