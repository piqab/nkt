package fail2ban

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
)

// Журнал банов: события Ban/Unban/Found из /var/log/fail2ban.log и его
// ротаций (.1, .2.gz …), а если файла нет (logtarget = SYSTEMD-JOURNAL) —
// из journald. Отдельно ничего не хранится: смотрим то, что пишет сам
// fail2ban, за последние дни.

// LogPath — журнал fail2ban по умолчанию.
const LogPath = "/var/log/fail2ban.log"

// maxLogBytes — сколько читать из одного файла журнала: неделя на
// атакуемом хосте — десятки мегабайт, дальше читать незачем.
const maxLogBytes = 64 << 20

// Event — одно событие журнала.
type Event struct {
	TS     string `json:"ts"`
	Jail   string `json:"jail"`
	Action string `json:"action"`
	IP     string `json:"ip"`
	Line   string `json:"line"`
}

// LogQuery — что искать.
type LogQuery struct {
	Days int
	// Text — подстрока в строке (адрес, джейл, что угодно), без учёта
	// регистра.
	Text   string
	Jail   string
	Action string
	Limit  int
}

// LogResult — найденное (новые сверху) и сколько всего совпало.
type LogResult struct {
	Events []Event `json:"events"`
	Total  int     `json:"total"`
	Source string  `json:"source"`
	// Truncated — совпавших больше, чем отдано.
	Truncated bool `json:"truncated"`
}

// eventRe — «… [sshd] Ban 1.2.3.4» (файл и journald пишут одинаково).
var eventRe = regexp.MustCompile(`\[([^\]]+)\]\s+(Restore Ban|Increase Ban|Ban|Unban|Found|Ignore)\s+(\S+)`)

var (
	fileTSRe    = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})`)
	journalTSRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{4})`)
)

// Actions — виды событий для фильтра.
var Actions = []string{"Ban", "Unban", "Found", "Restore Ban", "Increase Ban", "Ignore"}

// parseEvent — событие из строки; ok=false — строка не о бане.
func parseEvent(line string) (Event, time.Time, bool) {
	m := eventRe.FindStringSubmatch(line)
	if m == nil {
		return Event{}, time.Time{}, false
	}
	var ts time.Time
	if t := fileTSRe.FindString(line); t != "" {
		ts, _ = time.ParseInLocation("2006-01-02 15:04:05", t, time.Local)
	} else if t := journalTSRe.FindString(line); t != "" {
		ts, _ = time.Parse("2006-01-02T15:04:05-0700", t)
	}
	if ts.IsZero() {
		return Event{}, time.Time{}, false
	}
	return Event{TS: ts.Format(time.RFC3339), Jail: m[1], Action: m[2], IP: m[3], Line: strings.TrimSpace(line)}, ts, true
}

// logFiles — файл журнала и его ротации по возрасту: сначала свежие.
func logFiles(c collect.Collector) []string {
	var files []string
	if c.Exists(LogPath) {
		files = append(files, LogPath)
	}
	rotated, _ := c.Glob(LogPath + ".*")
	sort.Slice(rotated, func(i, j int) bool { return rotationIndex(rotated[i]) < rotationIndex(rotated[j]) })
	return append(files, rotated...)
}

// rotationIndex — номер ротации из «fail2ban.log.3.gz».
func rotationIndex(p string) int {
	rest := strings.TrimPrefix(p, LogPath+".")
	rest = strings.TrimSuffix(rest, ".gz")
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 1 << 20
	}
	return n
}

func readLog(c collect.Collector, p string) string {
	raw, err := c.ReadFile(p)
	if err != nil {
		return ""
	}
	if strings.HasSuffix(p, ".gz") {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return ""
		}
		defer zr.Close()
		raw, _ = io.ReadAll(io.LimitReader(zr, maxLogBytes))
	}
	if len(raw) > maxLogBytes {
		raw = raw[len(raw)-maxLogBytes:]
	}
	return string(raw)
}

// ReadLog — события за Days дней по запросу.
func ReadLog(ctx context.Context, c collect.Collector, q LogQuery) LogResult {
	if q.Days <= 0 || q.Days > 31 {
		q.Days = 7
	}
	if q.Limit <= 0 || q.Limit > 5000 {
		q.Limit = 1000
	}
	since := time.Now().AddDate(0, 0, -q.Days)
	text := strings.ToLower(strings.TrimSpace(q.Text))

	var texts []string
	res := LogResult{Events: []Event{}}
	if files := logFiles(c); len(files) > 0 {
		res.Source = LogPath
		for _, f := range files {
			body := readLog(c, f)
			texts = append(texts, body)
			// Ротации старше срока дальше не читаем: первая строка файла
			// — самое раннее событие в нём.
			if _, ts, ok := firstEvent(body); ok && ts.Before(since) {
				break
			}
		}
	} else {
		res.Source = "journald"
		out, err := c.Run(ctx, "journalctl", "-u", "fail2ban", "--since", strconv.Itoa(q.Days*24)+" hours ago",
			"--no-pager", "-o", "short-iso")
		if err == nil {
			texts = append(texts, out.Stdout)
		}
	}

	// Снимок (режим fixtures) записан однажды: его журнал сдвигается так,
	// чтобы последнее событие было «сейчас», иначе через неделю демо
	// показывало бы пустой журнал.
	var shift time.Duration
	if c.Mode() == "fixtures" {
		var newest time.Time
		for _, body := range texts {
			for _, line := range strings.Split(body, "\n") {
				if _, ts, ok := parseEvent(line); ok && ts.After(newest) {
					newest = ts
				}
			}
		}
		if !newest.IsZero() {
			shift = time.Until(newest) * -1
		}
	}

	var all []Event
	var stamps []time.Time
	for _, body := range texts {
		for _, line := range strings.Split(body, "\n") {
			ev, ts, ok := parseEvent(line)
			if ok && shift != 0 {
				ts = ts.Add(shift)
				ev.TS = ts.Format(time.RFC3339)
				if fileTSRe.MatchString(ev.Line) {
					ev.Line = ts.In(time.Local).Format("2006-01-02 15:04:05") + ev.Line[19:]
				}
			}
			if !ok || ts.Before(since) {
				continue
			}
			if q.Jail != "" && ev.Jail != q.Jail {
				continue
			}
			if q.Action != "" && ev.Action != q.Action {
				continue
			}
			if text != "" && !strings.Contains(strings.ToLower(ev.Line), text) {
				continue
			}
			all = append(all, ev)
			stamps = append(stamps, ts)
		}
	}
	idx := make([]int, len(all))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return stamps[idx[a]].After(stamps[idx[b]]) })
	res.Total = len(all)
	for _, i := range idx {
		if len(res.Events) >= q.Limit {
			res.Truncated = true
			break
		}
		res.Events = append(res.Events, all[i])
	}
	return res
}

// firstEvent — самое раннее событие файла (первое по порядку строк).
func firstEvent(body string) (Event, time.Time, bool) {
	for _, line := range strings.Split(body, "\n") {
		if ev, ts, ok := parseEvent(line); ok {
			return ev, ts, true
		}
	}
	return Event{}, time.Time{}, false
}
