package ups

import (
	"fmt"

	"github.com/piqab/nkt/internal/model"
)

func finding(name, severity, key string, titleArgs, detailArgs []any) model.Finding {
	return model.Finding{
		ID: "ups-" + key + ":" + name, Rule: "ups", Severity: severity, Service: model.ServiceHost, Object: name,
		TitleKey: "finding." + key + ".title", TitleArgs: titleArgs,
		DetailKey: "finding." + key + ".detail", DetailArgs: detailArgs,
		SuggestionKey: "finding." + key + ".suggestion",
	}
}

// LowOnBattery — ИБП на батарее и вот-вот сядет: сам ИБП говорит LB,
// заряд ниже chargePct или осталось меньше runtimeMin минут.
func LowOnBattery(u model.UPS, chargePct, runtimeMin int) bool {
	if !u.OnBattery {
		return false
	}
	return u.LowBattery || u.HasCharge && u.ChargePct < float64(chargePct) || u.HasRuntime && u.RuntimeS < int64(runtimeMin)*60
}

// Findings — что не так с ИБП: нет связи, работает от батареи (и вот-вот
// сядет), перегрузка, батарею пора менять.
func Findings(list []model.UPS, chargePct, runtimeMin int) []model.Finding {
	var out []model.Finding
	for _, u := range list {
		switch {
		case u.Error != "":
			out = append(out, finding(u.Name, model.SeverityHigh, "upsNoComm", []any{u.Name}, []any{u.Error}))
			continue
		case LowOnBattery(u, chargePct, runtimeMin):
			out = append(out, finding(u.Name, model.SeverityCritical, "upsBatteryLow", []any{u.Name}, []any{pct(u), mins(u)}))
		case u.OnBattery:
			out = append(out, finding(u.Name, model.SeverityHigh, "upsOnBattery", []any{u.Name}, []any{pct(u), mins(u)}))
		}
		if u.Overload {
			out = append(out, finding(u.Name, model.SeverityHigh, "upsOverload", []any{u.Name}, []any{fmt.Sprintf("%.0f", u.LoadPct)}))
		}
		if u.ReplaceBattery {
			out = append(out, finding(u.Name, model.SeverityMedium, "upsReplaceBattery", []any{u.Name}, nil))
		}
	}
	return out
}

func pct(u model.UPS) string {
	if !u.HasCharge {
		return "?"
	}
	return fmt.Sprintf("%.0f", u.ChargePct)
}

func mins(u model.UPS) string {
	if !u.HasRuntime {
		return "?"
	}
	return fmt.Sprintf("%d", u.RuntimeS/60)
}
