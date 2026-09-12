package control

import (
	"strings"
	"testing"
)

func TestParseSupportedAndLocaleGen(t *testing.T) {
	list := parseSupported("# comment\naa_DJ.UTF-8 UTF-8\nru_RU.UTF-8 UTF-8\n\nru_RU ISO-8859-5\n")
	if len(list) != 3 || list[1].Name != "ru_RU.UTF-8" || list[1].Charset != "UTF-8" {
		t.Fatalf("parseSupported = %+v", list)
	}
	gen := "# en_US ISO-8859-1\nen_US.UTF-8 UTF-8\n# ru_RU.UTF-8 UTF-8\n"
	text, changed := enableInLocaleGen(gen, list[1])
	if !changed || !strings.Contains(text, "\nru_RU.UTF-8 UTF-8\n") || strings.Contains(text, "# ru_RU.UTF-8") {
		t.Errorf("раскомментирование: %q", text)
	}
	if _, changed := enableInLocaleGen(text, list[1]); changed {
		t.Error("повторное включение отмечено как изменение")
	}
	text, changed = enableInLocaleGen(text, list[0])
	if !changed || !strings.HasSuffix(text, "aa_DJ.UTF-8 UTF-8\n") {
		t.Errorf("добавление: %q", text)
	}
	if normLocale("ru_RU.UTF-8") != normLocale("ru_RU.utf8") {
		t.Error("normLocale не сводит UTF-8 и utf8")
	}
}

func TestParseTimesync(t *testing.T) {
	st := parseTimesyncStatus(`LinkNTPServers=
SystemNTPServers=time.google.com pool.ntp.org
FallbackNTPServers=ntp.ubuntu.com
ServerName=time.google.com
ServerAddress=216.239.35.0
NTPMessage={ Leap=0, Version=4, Mode=3, Stratum=1, Precision=-20, RootDelay=0, RootDistance=1.2ms, Reference=GOOG, OriginateTimestamp=x, ReceiveTimestamp=y, TransmitTimestamp=z, DestinationTimestamp=w, Ignored=no, PacketCount=3, Jitter=1.1ms, Offset=+2.345ms, Delay=12ms }
`)
	if st.server != "time.google.com" || st.stratum != "1" || st.offset != "+2.345ms" || st.systemServers != "time.google.com pool.ntp.org" {
		t.Errorf("timesync = %+v", st)
	}
	ch := parseChronyTracking(`Reference ID    : D8EF2300 (216.239.35.0)
Stratum         : 2
Ref time (UTC)  : Fri Sep 12 12:00:00 2026
System time     : 0.000123456 seconds fast of NTP time
`)
	if ch.server != "216.239.35.0" || ch.stratum != "2" || !strings.HasPrefix(ch.offset, "0.000123456") {
		t.Errorf("chrony = %+v", ch)
	}
}
