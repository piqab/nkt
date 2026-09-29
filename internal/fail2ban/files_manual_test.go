package fail2ban

import (
	"strings"
	"testing"
)

// Без группы <HOST> fail2ban отвергает фильтр при reload («No failure-id
// group»), хотя fail2ban-client -t его пропускает.
func TestManualFilterHasHostGroup(t *testing.T) {
	re := ParseINI(ManualFilterContent)["Definition"]["failregex"]
	if !strings.Contains(re, "<HOST>") {
		t.Fatalf("failregex without <HOST>: %q", re)
	}
}
