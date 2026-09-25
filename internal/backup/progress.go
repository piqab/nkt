package backup

import (
	"regexp"
	"strconv"
	"strings"
)

// Прогресс долгих шагов — из вывода самих команд, без отдельного канала:
//   - qemu-img -p печатает «    (12.34/100%)»;
//   - сценарий перед tar печатает «@@total <байт> <что>» (du -sb), а tar с
//     --checkpoint=100 --checkpoint-action='echo=@@tarcp %u' — номер
//     записи (по 10 КиБ); процент — записи × 10240 / объём;
//   - «@@phase <что>» — шаг без процентов (docker save, virsh define).
// Служебные строки в журнал не попадают, прогресс уходит в шаг задания
// (step/steps = N/100), и окно журнала рисует по нему полосу.

var (
	// lxc export/import: «Exporting the backup: 45% (12.3MB/s)».
	lxcProgressRe  = regexp.MustCompile(`^(Exporting|Importing|Backing up|Uploading|Unpacking)[^:]*:\s*(\d+)%`)
	qemuProgressRe = regexp.MustCompile(`^\s*\((\d+(?:\.\d+)?)/100%\)\s*$`)
	tarCheckRe     = regexp.MustCompile(`@@tarcp (\d+)`)
)

const tarRecordBytes = 10240

// Progress разбирает вывод и сообщает шаг: step — процент 0…100 (-1 —
// без процентов), name — что сейчас идёт.
type Progress struct {
	report func(step int, name string)
	label  string
	total  int64
	last   int
}

// NewProgress — report вызывается только при смене процента или шага.
func NewProgress(report func(step int, name string)) *Progress {
	return &Progress{report: report, last: -2}
}

// Line разбирает строку; true — строка служебная и в журнал не идёт.
func (p *Progress) Line(line string) bool {
	switch {
	case strings.HasPrefix(line, "@@total "):
		f := strings.SplitN(strings.TrimPrefix(line, "@@total "), " ", 2)
		p.total, _ = strconv.ParseInt(f[0], 10, 64)
		if len(f) > 1 {
			p.label = f[1]
		}
		p.set(0)
		return true
	case strings.HasPrefix(line, "@@phase "):
		p.label = strings.TrimPrefix(line, "@@phase ")
		p.total = 0
		p.set(-1)
		return true
	case strings.HasPrefix(line, "--- lxc "):
		p.label = strings.TrimPrefix(line, "--- ")
		p.total = 0
		p.set(0)
		return false
	case strings.HasPrefix(line, "--- copy "):
		// qemu-img -p следом — шаг копирования диска.
		p.label = strings.TrimPrefix(line, "--- ")
		p.total = 0
		p.set(0)
		return false
	}
	if m := tarCheckRe.FindStringSubmatch(line); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		if p.total > 0 {
			pct := int(n * tarRecordBytes * 100 / p.total)
			if pct > 99 {
				pct = 99
			}
			p.set(pct)
		}
		return true
	}
	if m := lxcProgressRe.FindStringSubmatch(line); m != nil {
		n, _ := strconv.Atoi(m[2])
		if p.label == "" {
			p.label = m[1]
		}
		p.set(n)
		return true
	}
	if m := qemuProgressRe.FindStringSubmatch(line); m != nil {
		f, _ := strconv.ParseFloat(m[1], 64)
		p.set(int(f))
		return true
	}
	return false
}

func (p *Progress) set(pct int) {
	if pct == p.last {
		return
	}
	p.last = pct
	p.report(pct, p.label)
}
