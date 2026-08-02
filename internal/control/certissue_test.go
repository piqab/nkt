package control

import (
	"testing"
)

func TestNormaliseCertbotDomainsRejectsWildcard(t *testing.T) {
	if _, err := normaliseCertbotDomains([]string{"*.example.com"}); err == nil {
		t.Error("ожидалась ошибка для wildcard-имени")
	}
}

func TestNormaliseCertbotDomainsRejectsEmpty(t *testing.T) {
	for _, in := range [][]string{nil, {}, {"", "  "}} {
		if _, err := normaliseCertbotDomains(in); err == nil {
			t.Errorf("normaliseCertbotDomains(%v): ожидалась ошибка", in)
		}
	}
}

func TestNormaliseCertbotDomainsRejectsBad(t *testing.T) {
	if _, err := normaliseCertbotDomains([]string{"bad domain with spaces"}); err == nil {
		t.Error("ожидалась ошибка валидации")
	}
}

// DNS, TLS SNI and X.509 SANs only ever carry ASCII — a domain typed in
// Cyrillic must convert to punycode instead of being rejected outright,
// same as SelfSignedRequest.normalise already does.
func TestNormaliseCertbotDomainsConvertsUnicode(t *testing.T) {
	got, err := normaliseCertbotDomains([]string{"Испытание.РФ"})
	if err != nil {
		t.Fatalf("normaliseCertbotDomains: %v", err)
	}
	if want := "xn--80akhbyknj4f.xn--p1ai"; len(got) != 1 || got[0] != want {
		t.Errorf("got = %v, ожидалось [%q]", got, want)
	}
}

// TestStartIssueCertbotSuccess covers a domain certbot has never heard of —
// unlike renewal, there is no existing lineage/renewal.conf, so this always
// stops nginx/haproxy and runs `certonly --standalone`, matching the fixture
// canned response for exactly this domain (see .commands/index.json).
func TestStartIssueCertbotSuccess(t *testing.T) {
	m, _ := renewSetup(t)

	id, err := m.StartIssueCertbot("test", []string{"newsite.example.com"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, errMsg := waitForJob(t, m, id)
	if errMsg != "" {
		t.Fatalf("задача завершилась с ошибкой: %s (события: %v)", errMsg, eventTexts(events))
	}

	texts := eventTexts(events)
	wantInOrder := []string{
		"Начинаю выпуск сертификата для newsite.example.com",
		"nginx: остановлен",
		"haproxy: остановлен",
		"certbot",
		"сертификат выпущен для newsite.example.com",
		"nginx: запущен",
		"haproxy: запущен",
		"Готово",
	}
	pos := 0
	for _, want := range wantInOrder {
		idx := indexOfSubstring(texts, want, pos)
		if idx == -1 {
			t.Fatalf("не нашёл %q после позиции %d в событиях: %v", want, pos, texts)
		}
		pos = idx + 1
	}
}

func TestStartIssueCertbotRejectsWildcard(t *testing.T) {
	m, _ := renewSetup(t)
	if _, err := m.StartIssueCertbot("test", []string{"*.example.com"}); err == nil {
		t.Error("ожидалась ошибка валидации до запуска задачи")
	}
}
