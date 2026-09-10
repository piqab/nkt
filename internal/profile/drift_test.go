package profile

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/store"
)

func driftDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestCheckDriftCountsAndReports(t *testing.T) {
	ctx := context.Background()
	db := driftDB(t)

	if _, err := db.CreateProfile(ctx, store.Profile{
		Name: "совпадает", Content: "version: 1\nname: совпадает\npackages: [nginx]\n",
	}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if _, err := db.CreateProfile(ctx, store.Profile{
		Name: "разошёлся", Content: "version: 1\nname: разошёлся\npackages: [fail2ban]\n" +
			"services:\n  ssh:\n    active: false\n",
	}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	host := fakeReader{
		packages: map[string]bool{"nginx": true},
		services: map[string][3]bool{"ssh": {true, true, true}},
	}
	drifted, err := CheckDrift(ctx, db, host)
	if err != nil {
		t.Fatalf("CheckDrift: %v", err)
	}
	if drifted != 1 {
		t.Errorf("разошедшихся профилей = %d, ожидался 1", drifted)
	}

	report, ok := LastDrift(ctx, db)
	if !ok {
		t.Fatal("результат проверки не сохранён")
	}
	if len(report.Results) != 2 {
		t.Fatalf("в сводке %d профилей", len(report.Results))
	}
	byName := map[string]DriftResult{}
	for _, r := range report.Results {
		byName[r.Name] = r
	}
	if byName["совпадает"].Changes != 0 {
		t.Errorf("совпавший профиль показан разошедшимся: %+v", byName["совпадает"])
	}
	// Остановка sshd помечена опасной — из-за неё находка поднимается со
	// «низкой» до «средней».
	if byName["разошёлся"].Changes != 2 || byName["разошёлся"].Risky != 1 {
		t.Errorf("разошедшийся профиль: %+v", byName["разошёлся"])
	}

	findings := DriftFindings(report)
	if len(findings) != 1 {
		t.Fatalf("находок = %d, ожидалась одна", len(findings))
	}
	if findings[0].Severity != model.SeverityMedium {
		t.Errorf("важность = %q, при опасном пункте ожидалась средняя", findings[0].Severity)
	}
	if findings[0].TitleKey == "" || findings[0].DetailKey == "" {
		t.Error("находка без ключей каталога — на английском покажется по-русски")
	}
}

// Сломанное описание — повод сказать вслух: молчаливое «расхождений нет»
// здесь было бы прямой ложью.
func TestCheckDriftReportsBrokenProfile(t *testing.T) {
	ctx := context.Background()
	db := driftDB(t)
	if _, err := db.CreateProfile(ctx, store.Profile{
		Name: "сломан", Content: "packages: [\"nginx; rm -rf /\"]\n",
	}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	drifted, err := CheckDrift(ctx, db, fakeReader{})
	if err != nil {
		t.Fatalf("CheckDrift: %v", err)
	}
	if drifted != 1 {
		t.Errorf("сломанный профиль не посчитан: %d", drifted)
	}
	report, _ := LastDrift(ctx, db)
	findings := DriftFindings(report)
	if len(findings) != 1 || !strings.Contains(findings[0].Title, "не проверить") {
		t.Errorf("находки = %+v", findings)
	}
}
