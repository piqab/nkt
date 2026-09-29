package fail2ban

import (
	"net/netip"
	"strings"

	"github.com/piqab/nkt/internal/model"
)

// SSHExposed — sshd слушает не только loopback.
func SSHExposed(listeners []model.Listener) bool {
	for _, l := range listeners {
		if l.Port != 22 && l.Process != "sshd" {
			continue
		}
		if l.Process != "" && l.Process != "sshd" && l.Process != "systemd" {
			continue
		}
		a := strings.Trim(l.Address, "[]")
		if a == "" || a == "*" || a == "0.0.0.0" || a == "::" {
			return true
		}
		if ip, err := netip.ParseAddr(a); err == nil && !ip.IsLoopback() {
			return true
		}
	}
	return false
}

func finding(id, severity, key string, titleArgs, detailArgs []any, object string) model.Finding {
	return model.Finding{
		ID: id, Rule: "fail2ban", Severity: severity, Service: model.ServiceFail2ban, Object: object,
		TitleKey: "finding." + key + ".title", TitleArgs: titleArgs,
		DetailKey: "finding." + key + ".detail", DetailArgs: detailArgs,
		SuggestionKey: "finding." + key + ".suggestion",
	}
}

// Findings — что не так с защитой перебора. hub — внешний адрес хаба,
// как его видит этот хост (не задан — проверка ignoreip пропускается).
func Findings(st *model.Fail2banState, listeners []model.Listener, hub netip.Addr) []model.Finding {
	if st == nil {
		return nil
	}
	ssh := SSHExposed(listeners)
	var out []model.Finding
	switch {
	case !st.Installed:
		if ssh {
			out = append(out, finding("fail2ban-missing", model.SeverityMedium, "f2bMissing", nil, nil, "fail2ban"))
		}
		return out
	case !st.Running:
		sev := model.SeverityMedium
		if ssh {
			sev = model.SeverityHigh
		}
		out = append(out, finding("fail2ban-stopped", sev, "f2bStopped", nil, []any{st.Error}, "fail2ban"))
		return out
	}
	if ssh {
		has := false
		for _, j := range st.Jails {
			if strings.Contains(j.Name, "ssh") {
				has = true
			}
		}
		if !has {
			out = append(out, finding("fail2ban-no-sshd", model.SeverityMedium, "f2bNoSSHJail", nil, nil, "sshd"))
		}
	}
	for _, j := range st.Jails {
		if j.Journal == "" && len(j.LogPaths) > 0 && len(j.MissingLogs) == len(j.LogPaths) {
			out = append(out, finding("fail2ban-deadjail-"+j.Name, model.SeverityMedium, "f2bDeadJail",
				[]any{j.Name}, []any{strings.Join(j.MissingLogs, ", ")}, j.Name))
		}
	}
	if hub.IsValid() {
		var missing []string
		for _, j := range st.Jails {
			if j.Name != ManualJail && !Covers(j.IgnoreIP, hub) {
				missing = append(missing, j.Name)
			}
		}
		if len(missing) > 0 {
			out = append(out, finding("fail2ban-hub-not-ignored", model.SeverityHigh, "f2bHubNotIgnored",
				[]any{hub.String()}, []any{strings.Join(missing, ", ")}, hub.String()))
		}
	}
	return out
}
