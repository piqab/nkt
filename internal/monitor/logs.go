package monitor

import (
	"context"
	"fmt"
	gopath "path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/store"
)

// Metric source names for log-derived series.
const (
	SourceNginxLog   = "nginx_log"
	SourceHAProxyLog = "haproxy_log"
)

// maxLogLines bounds how much of a log file is read per pass. Rotated files can
// be enormous, and anything older than the previous pass is skipped anyway.
const maxLogLines = 40000

// LogSource is one access log to read, plus the name its numbers belong to.
type LogSource struct {
	Path    string
	Service string
	Subject string
}

// LogCollector turns access logs into request-rate time series.
type LogCollector struct {
	db *store.DB
	c  collect.Collector
}

// NewLogCollector builds the access-log collector.
func NewLogCollector(db *store.DB, c collect.Collector) *LogCollector {
	return &LogCollector{db: db, c: c}
}

// DiscoverSources derives the log list from the parsed configuration: every
// access_log path an nginx server declares, plus the haproxy log.
func DiscoverSources(snap *model.Snapshot, extraNginx, extraHAProxy []string) []LogSource {
	seen := map[string]bool{}
	var out []LogSource

	add := func(path, service, subject string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, LogSource{Path: path, Service: service, Subject: subject})
	}

	if snap != nil {
		for _, e := range snap.Endpoints {
			if e.Service != model.ServiceNginx {
				continue
			}
			subject := e.Label
			if len(e.Names) > 0 {
				subject = e.Names[0]
			}
			for _, logPath := range e.AccessLog {
				add(logPath, model.ServiceNginx, subject)
			}
		}
	}
	for _, p := range extraNginx {
		add(p, model.ServiceNginx, logSubjectFromPath(p))
	}
	for _, p := range extraHAProxy {
		add(p, model.ServiceHAProxy, "haproxy")
	}
	return out
}

func logSubjectFromPath(path string) string {
	base := gopath.Base(path)
	base = strings.TrimSuffix(base, ".log")
	base = strings.TrimSuffix(base, ".access")
	if base == "" || base == "access" {
		return "default"
	}
	return base
}

// RunOnce reads every source and stores the request rates it finds. Samples
// carry the timestamp of the log entry, not of the scan, so the usage schedule
// reflects when traffic actually happened.
func (l *LogCollector) RunOnce(ctx context.Context, sources []LogSource) (int, error) {
	var samples []store.MetricSample

	for _, src := range sources {
		if !l.c.Exists(src.Path) {
			continue
		}
		lines, err := collect.ReadLines(l.c, src.Path, maxLogLines)
		if err != nil {
			continue
		}

		posKey := "logpos:" + src.Path
		lastSeen, _, err := l.db.KVGet(ctx, posKey)
		if err != nil {
			return 0, err
		}

		source := SourceNginxLog
		parseLine := parseNginxLine
		if src.Service == model.ServiceHAProxy {
			source, parseLine = SourceHAProxyLog, parseHAProxyLine
		}

		// bucket -> metric -> value
		agg := map[string]map[string]float64{}
		newest := lastSeen

		for _, line := range lines {
			entry, ok := parseLine(line)
			if !ok {
				continue
			}
			ts := store.FormatTime(entry.When)
			if lastSeen != "" && ts <= lastSeen {
				continue
			}
			if ts > newest {
				newest = ts
			}
			bucket := entry.When.UTC().Truncate(time.Hour).Format(time.RFC3339)
			if agg[bucket] == nil {
				agg[bucket] = map[string]float64{}
			}
			agg[bucket]["requests"]++
			agg[bucket]["bytes"] += float64(entry.Bytes)
			switch {
			case entry.Status >= 500:
				agg[bucket]["errors_5xx"]++
			case entry.Status >= 400:
				agg[bucket]["errors_4xx"]++
			}
		}

		for bucket, metrics := range agg {
			for metric, value := range metrics {
				samples = append(samples, store.MetricSample{
					TS: bucket, Source: source, Subject: src.Subject, Metric: metric, Value: value,
				})
			}
		}
		if newest != lastSeen {
			if err := l.db.KVSet(ctx, posKey, newest); err != nil {
				return 0, err
			}
		}
	}

	if err := l.db.InsertMetrics(ctx, samples); err != nil {
		return 0, fmt.Errorf("сохранение метрик логов: %w", err)
	}
	return len(samples), nil
}

// logEntry is the part of an access-log line the collector aggregates.
type logEntry struct {
	When   time.Time
	Status int
	Bytes  int64
}

const accessLogTimeLayout = "02/Jan/2006:15:04:05 -0700"

var nginxLineRe = regexp.MustCompile(`\[([^\]]+)\]\s+"([^"]*)"\s+(\d{3})\s+(\d+|-)`)

// parseNginxLine reads the combined log format, which is what nginx ships with
// and what the default log_format in the fixtures extends.
func parseNginxLine(line string) (logEntry, bool) {
	m := nginxLineRe.FindStringSubmatch(line)
	if m == nil {
		return logEntry{}, false
	}
	when, err := time.Parse(accessLogTimeLayout, m[1])
	if err != nil {
		return logEntry{}, false
	}
	status, _ := strconv.Atoi(m[3])
	var bytes int64
	if m[4] != "-" {
		bytes, _ = strconv.ParseInt(m[4], 10, 64)
	}
	return logEntry{When: when, Status: status, Bytes: bytes}, true
}

var haproxyTimeRe = regexp.MustCompile(`\[(\d{2}/\w{3}/\d{4}:\d{2}:\d{2}:\d{2})\.\d+\]`)

// parseHAProxyLine handles both httplog and tcplog: they differ in whether the
// timing field has five components (HTTP) or three (TCP), and TCP lines carry
// no status code.
func parseHAProxyLine(line string) (logEntry, bool) {
	m := haproxyTimeRe.FindStringSubmatchIndex(line)
	if m == nil {
		return logEntry{}, false
	}
	when, err := time.Parse("02/Jan/2006:15:04:05 -0700", line[m[2]:m[3]]+" +0000")
	if err != nil {
		return logEntry{}, false
	}

	fields := strings.Fields(line[m[1]:])
	// fields: frontend backend/server timings [status] bytes ...
	if len(fields) < 4 {
		return logEntry{}, false
	}
	timings := fields[2]
	entry := logEntry{When: when}
	if strings.Count(timings, "/") >= 4 {
		if status, err := strconv.Atoi(fields[3]); err == nil {
			entry.Status = status
		}
		if len(fields) > 4 {
			entry.Bytes, _ = strconv.ParseInt(fields[4], 10, 64)
		}
		return entry, true
	}
	entry.Bytes, _ = strconv.ParseInt(fields[3], 10, 64)
	return entry, true
}
