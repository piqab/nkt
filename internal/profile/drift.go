package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/store"
)

// Дрейф — то, ради чего профили и затевались: не «примени вот это», а
// «на сервере уже не то, что задумано». Проверка идёт по расписанию,
// ничего не меняет и оставляет результат там, где его увидят и без
// захода в раздел профилей — в общем списке проблем.

// driftKey — где лежит последний результат проверки.
const driftKey = "profile.drift"

// DriftResult — итог проверки одного профиля.
type DriftResult struct {
	ProfileID int64 `json:"profile_id"`
	Name      string `json:"name"`
	Changes   int    `json:"changes"`
	// Risky — сколько из расхождений помечены опасными.
	Risky int `json:"risky"`
	// Unknown — о скольких ресурсах судить не удалось.
	Unknown int    `json:"unknown"`
	Error   string `json:"error,omitempty"`
}

// DriftReport — результат последней проверки всех профилей.
type DriftReport struct {
	TS      string        `json:"ts"`
	Results []DriftResult `json:"results"`
}

// CheckDrift строит план по каждому сохранённому профилю и запоминает
// сводку. Возвращает число профилей, у которых нашлись расхождения.
func CheckDrift(ctx context.Context, db *store.DB, r Reader) (int, error) {
	profiles, err := db.ListProfiles(ctx)
	if err != nil {
		return 0, err
	}
	report := DriftReport{TS: store.FormatTime(time.Now())}
	drifted := 0
	for _, row := range profiles {
		full, err := db.ProfileByID(ctx, row.ID)
		if err != nil {
			report.Results = append(report.Results, DriftResult{
				ProfileID: row.ID, Name: row.Name, Error: err.Error(),
			})
			continue
		}
		p, err := Parse([]byte(full.Content))
		if err != nil {
			// Сломанное описание — тоже повод сказать вслух: молчаливо
			// «расхождений нет» здесь было бы прямой ложью.
			report.Results = append(report.Results, DriftResult{
				ProfileID: row.ID, Name: row.Name, Error: err.Error(),
			})
			drifted++
			continue
		}
		plan := Build(ctx, p, r)
		res := DriftResult{
			ProfileID: row.ID, Name: row.Name,
			Changes: len(plan.Changes), Unknown: len(plan.Unknown),
		}
		for _, c := range plan.Changes {
			if c.Risk != "" {
				res.Risky++
			}
		}
		if res.Changes > 0 {
			drifted++
		}
		report.Results = append(report.Results, res)
	}

	raw, err := json.Marshal(report)
	if err != nil {
		return drifted, err
	}
	return drifted, db.KVSet(ctx, driftKey, string(raw))
}

// LastDrift читает последний результат проверки.
func LastDrift(ctx context.Context, db *store.DB) (DriftReport, bool) {
	raw, ok, err := db.KVGet(ctx, driftKey)
	if err != nil || !ok {
		return DriftReport{}, false
	}
	var report DriftReport
	if json.Unmarshal([]byte(raw), &report) != nil {
		return DriftReport{}, false
	}
	return report, true
}

// DriftFindings превращает сводку в находки — те же, что показывает
// общий список проблем. Отдельного механизма оповещений у профилей нет и
// не нужно: расхождение с задуманным — такая же находка, как открытый
// наружу порт.
func DriftFindings(report DriftReport) []model.Finding {
	out := []model.Finding{}
	for _, res := range report.Results {
		switch {
		case res.Error != "":
			out = append(out, model.Finding{
				ID:       fmt.Sprintf("profile-drift-%d", res.ProfileID),
				Rule:     "profile.drift",
				Severity: model.SeverityMedium,
				Service:  "profile",
				Object:   res.Name,
				// Ключ каталога рядом с готовым текстом: перевод
				// подставляется на выдаче (model.LocalizeSnapshot), а
				// текст остаётся запасным вариантом там, где перевода
				// нет. Тот же приём, что у находок анализатора.
				TitleKey:      "finding.profileBroken.title",
				TitleArgs:     []any{res.Name},
				Title:         fmt.Sprintf("Профиль «%s» не проверить", res.Name),
				Detail:        res.Error,
				SuggestionKey: "finding.profileBroken.suggestion",
				Suggestion: "Откройте раздел «Профили» и исправьте описание — " +
					"пока оно не читается, расхождения не отслеживаются.",
			})
		case res.Changes > 0:
			severity := model.SeverityLow
			if res.Risky > 0 {
				severity = model.SeverityMedium
			}
			out = append(out, model.Finding{
				ID:        fmt.Sprintf("profile-drift-%d", res.ProfileID),
				Rule:      "profile.drift",
				Severity:  severity,
				Service:   "profile",
				Object:    res.Name,
				TitleKey:  "finding.profileDrift.title",
				TitleArgs: []any{res.Name, res.Changes},
				Title: fmt.Sprintf("Хост разошёлся с профилем «%s»: расхождений — %d",
					res.Name, res.Changes),
				DetailKey:     "finding.profileDrift.detail",
				DetailArgs:    []any{res.Changes, res.Risky, res.Unknown},
				Detail:        driftDetail(res),
				SuggestionKey: "finding.profileDrift.suggestion",
				Suggestion: "Откройте «Профили», постройте план и примените то, " +
					"что действительно нужно.",
			})
		}
	}
	return out
}

func driftDetail(res DriftResult) string {
	detail := fmt.Sprintf("Проверка сравнила состояние хоста с описанием: расхождений — %d", res.Changes)
	if res.Risky > 0 {
		detail += fmt.Sprintf(", из них помеченных опасными — %d", res.Risky)
	}
	if res.Unknown > 0 {
		detail += fmt.Sprintf("; о %d ресурсах судить не удалось", res.Unknown)
	}
	return detail + "."
}
