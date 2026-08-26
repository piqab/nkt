package model

import "github.com/althq/netknownsthat/internal/msgs"

// resolve returns text unchanged when key is empty — either this field
// hasn't been converted to the msgs catalog yet, or there's nothing to
// report — otherwise the catalog's rendering of key/args for lang.
func resolve(lang msgs.Lang, text, key string, args []any) string {
	if key == "" {
		return text
	}
	return msgs.T(lang, key, args...)
}

// LocalizeSnapshot returns a shallow copy of snap with every translatable
// field (SourceStatus/Certificate/RenewalInfo/Finding text) resolved
// against lang. Every API handler that serves snapshot-derived JSON (see
// internal/api/handlers_inventory.go) calls this once, right before
// writeJSON — snap itself, and whatever Scanner.Latest() cached, is never
// mutated, so two requests for different languages against the same cached
// scan don't interfere with each other, and a scan doesn't need to know or
// care which language(s) will eventually read it.
func LocalizeSnapshot(lang msgs.Lang, snap *Snapshot) *Snapshot {
	if snap == nil {
		return nil
	}
	out := *snap
	out.Sources = localizeSources(lang, snap.Sources)
	out.Certs = localizeCerts(lang, snap.Certs)
	out.Findings = localizeFindings(lang, snap.Findings)
	return &out
}

func localizeSources(lang msgs.Lang, sources []SourceStatus) []SourceStatus {
	out := make([]SourceStatus, len(sources))
	for i, s := range sources {
		s.Error = resolve(lang, s.Error, s.ErrorKey, s.ErrorArgs)
		// Only trust WarningRefs when it lines up 1:1 with Warnings — a
		// source that appended some warnings through the old (unconverted)
		// path and some through the new one would otherwise translate the
		// wrong entries.
		if len(s.WarningRefs) == len(s.Warnings) {
			warnings := make([]string, len(s.Warnings))
			for j, w := range s.Warnings {
				ref := s.WarningRefs[j]
				warnings[j] = resolve(lang, w, ref.Key, ref.Args)
			}
			s.Warnings = warnings
		}
		out[i] = s
	}
	return out
}

func localizeCerts(lang msgs.Lang, certs []Certificate) []Certificate {
	out := make([]Certificate, len(certs))
	for i, c := range certs {
		c.Error = resolve(lang, c.Error, c.ErrorKey, c.ErrorArgs)
		c.Renewal.Detail = resolve(lang, c.Renewal.Detail, c.Renewal.DetailKey, c.Renewal.DetailArgs)
		out[i] = c
	}
	return out
}

func localizeFindings(lang msgs.Lang, findings []Finding) []Finding {
	out := make([]Finding, len(findings))
	for i, f := range findings {
		f.Title = resolve(lang, f.Title, f.TitleKey, f.TitleArgs)
		f.Detail = resolve(lang, f.Detail, f.DetailKey, f.DetailArgs)
		f.Suggestion = resolve(lang, f.Suggestion, f.SuggestionKey, f.SuggestionArgs)
		out[i] = f
	}
	return out
}
