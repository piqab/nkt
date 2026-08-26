package msgs

// enCatalog holds every key that's been converted so far — see ruCatalog
// for the full baseline (DefaultLang). A key present only in ruCatalog
// falls back to Russian text for English requests too, same as the
// frontend's own i18next fallbackLng — that's the expected state for
// anything not yet migrated, not a bug.
var enCatalog = map[string]string{
	"err.notFound":       "not found",
	"err.fileNotFound":   "file not found",
	"err.pathNotAllowed": "file outside the allowed directories",
	"err.tooLarge":       "file too large for the editor",

	"auth.usernamePasswordRequired": "Enter a username and password",
	"auth.minPasswordLength":        "Password must be at least 10 characters long",
	"auth.loginAndPasswordRequired": "A username is required, and the password must be at least 10 characters long",
	"auth.cannotRemoveOwnAdmin":     "You can't remove your own admin role",
	"auth.cannotDisableOwnAccount":  "You can't disable your own account",
	"auth.cannotDeleteOwnAccount":   "You can't delete your own account",
	"auth.createUserFailed":         "Couldn't create the user: %s",
	"auth.loginRequired":            "Sign-in required",

	"certgen.lineageRequired": "specify a lineage",

	"job.notFound": "Job not found",

	"monitor.invalidTargetId":     "Invalid target ID",
	"monitor.schedulerNotRunning": "The scheduler isn't running",
	"monitor.nothingToChange":     "Nothing to change",
	"monitor.invalidRuleNumber":   "Invalid rule number",

	"pkgInstall.fixturesDisabled":          "package installation isn't available in fixtures mode",
	"pkgInstall.aptGetMissing":             "apt-get not found — package installation is only supported on Debian/Ubuntu",
	"pkgInstall.dbusAlreadyAvailable":      "D-Bus is already available",
	"pkgInstall.dbusManualOnly":            "automatic install isn't available (needs CAP_SYS_ADMIN in the systemd unit and nsenter on the host) — install dbus by hand",
	"pkgInstall.tmuxAlreadyInstalled":      "tmux is already installed",
	"pkgInstall.ufwAlreadyInstalled":       "ufw is already installed",
	"pkgInstall.firewalldAlreadyInstalled": "firewalld is already installed",

	"pkgUpdate.fixturesDisabled": "package updates aren't available in fixtures mode",
	"pkgUpdate.aptGetMissing":    "apt-get not found — package updates are only supported on Debian/Ubuntu",

	"terminal.disabled":         "the web terminal is disabled: set NKT_TERMINAL_ENABLED=true",
	"terminal.fixturesDisabled": "the web terminal isn't available in fixtures mode",
	"terminal.tmuxStartFailed":  "couldn't start tmux: %s",

	"vulns.scanAlreadyRunning": "a scan is already running",

	"configs.staleContent":         "The file changed since it was opened in the editor. Reload it and try the edit again.",
	"configs.invalidVersionNumber": "Invalid version number",
	"configs.validationFailed":     "The configuration failed validation, changes were rolled back.",
	"configs.fileSaved":            "File saved.",
	"configs.fileSavedAndReloaded": "File saved and the configuration was reloaded.",
	"configs.applyFailed":          " Apply failed: %s",
	"configs.versionRestored":      "Restored version #%d.",

	"server.unknownApiMethod": "Unknown API method: %s",
	"server.frontendNotBuilt": "Frontend isn't built: run npm run build in the web/ directory",

	"selfupdate.localModeOnly":      "self-update is only available in local mode",
	"selfupdate.parseRequestFailed": "parsing the request: %s",
	"selfupdate.missingBinaryFile":  "missing the binary file: %s",
	"selfupdate.incompleteRequest":  "incomplete request: unit, env, and sha256 are required",
	"selfupdate.stageDirFailed":     "update directory: %s",
	"selfupdate.writeBinaryFailed":  "writing the binary: %s",
	"selfupdate.checksumMismatch":   "the binary's checksum didn't match (got %s, expected %s) — the transfer was corrupted, try again",
	"selfupdate.writeUnitFailed":    "writing the systemd unit: %s",
	"selfupdate.writeEnvFailed":     "writing nkt.env: %s",
	"selfupdate.startFailed":        "starting the update: %s",

	"hub.noInstallsYet":          "there haven't been any installs for this host yet",
	"hub.localScannerNotRunning": "the hub's local scanner isn't running",
	"hub.hostUnreachable":        "host unreachable: %s",
}
