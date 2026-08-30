package parse

import (
	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// Fail2ban discovers fail2ban's own config files for the Configs page's
// raw-text editor. Unlike nginx/haproxy/caddy, there is no structured
// block parser here — fail2ban's jail.local/.d syntax is its own INI
// dialect this app has no reason to understand — so this only has to
// report which files exist and are editable; ServiceManager.Validate's
// own default case (nothing registered for ServiceFail2ban) and
// Configs.tsx's BLOCK_SERVICES exclusion already turn "no parser" into
// "plain text editor, no validation" for free once these are discoverable
// at all.
//
// jail.conf itself (the package-shipped default) is deliberately never
// listed — fail2ban's own docs are explicit that it gets overwritten on
// upgrade and local overrides belong in jail.local/jail.d instead, so
// offering to edit it would be offering to edit a file whose changes are
// expected to vanish.
func Fail2ban(c collect.Collector, root string) []model.ManagedFile {
	var files []model.ManagedFile
	jailLocal := root + "/jail.local"
	if c.Exists(jailLocal) {
		files = append(files, describeFile(c, jailLocal, model.ServiceFail2ban, true))
	}
	matches, _ := c.Glob(root + "/jail.d/*.conf")
	for _, p := range matches {
		files = append(files, describeFile(c, p, model.ServiceFail2ban, true))
	}
	return files
}
