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

	"certgen.lineageRequired":       "specify a lineage",
	"certgen.stoppingForStandalone": "Stopping nginx and haproxy for --standalone…",
	"certgen.serviceStopped":        "%s: stopped",
	"certgen.serviceStarted":        "%s: started",
	"certgen.errorPrefix":           "Error: %s",
	"certgen.running":               "Running: %s",
	"certgen.certRenewed":           "certbot: certificate renewed",
	"certgen.certIssued":            "certbot: certificate issued for %s",
	"certgen.recombinedFile":        "Rebuilt the file for %s: %s",
	"certgen.checkingPort":          "Checking port %d…",
	"certgen.startingRenewal":       "Starting renewal of %s",
	"certgen.startingIssuance":      "Starting certificate issuance for %s",

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

	"hub.startingInstall":        "Starting install",
	"hub.installCancelledByUser": "Install cancelled by user",
	"hub.connectingSSH":          "Connecting via SSH to %s…",
	"hub.hostArch":               "Host: %s/%s",
	"hub.waitingHealth":          "Waiting for the service to answer /health…",
	"hub.checkingAdminAccount":   "Checking the admin account…",
	"hub.loginFailedResetting": "Login failed — the host already has an admin account with a different " +
		"password (e.g. from an earlier install attempt); resetting the password on the host…",
	"hub.adminPasswordSynced": "The admin password on the host is now in sync",
	"hub.done":                "Done",

	"hub.goNotWorkingInstalling": "go (%s) isn't available — installing the hub's own Go…",
	"hub.usingCachedGo":          "Using the hub's already-installed Go: %s",
	"hub.downloadingGo":          "Downloading %s for linux-%s…",
	"hub.goInstalled":            "Go installed: %s",

	"hub.sourceNotFoundUsingBinDir": "Sources not found in %s — using %s (the hub binary's directory) instead",
	"hub.usingCachedBinary":         "Using an already-built binary for %s/%s",
	"hub.buildingBinary":            "Building the binary for %s/%s…",
	"hub.uploadingUnitAndConfig":    "Uploading the systemd unit and config…",
	"hub.installingFiles":           "Installing files…",
	"hub.startingSystemdService":    "Starting the systemd service…",
	"hub.uploadingBinary":           "Uploading the binary… %d%% (%.1f MB of %.1f MB)",

	"hub.sendingViaTunnel":          "Sending the binary and config over the fallback channel…",
	"hub.hostAcceptedRestarting":    "The host accepted the update and is restarting the service…",
	"hub.sshUnavailableUsingTunnel": "SSH unavailable — updating over the fallback channel (%s/%s)…",
	"hub.retryingTunnelUpdate":      "Retrying the update over the fallback channel…",
	"hub.doneViaTunnel":             "Done (via the fallback channel)",

	"parse.configUnavailable":       "config %s isn't available: %v",
	"parse.configParseFailed":       "parsing %s: %v",
	"parse.nginxMainConfigNotFound": "main config %s not found",
	"parse.includeFileNotFound":     "%s: include %s — file not found",
	"parse.ipAddrParseFailed":       "parsing ip addr output: %v",
	"parse.commandFailed":           "%s exited with code %d: %s",

	"parse.libvirtUnavailable":        "libvirt unavailable: %s",
	"parse.libvirtListFailed":         "libvirt: virsh list returned code %d: %s",
	"parse.libvirtDominfoUnavailable": "libvirt: dominfo %s unavailable",
	"parse.libvirtDumpxmlUnavailable": "libvirt: dumpxml %s unavailable",
	"parse.libvirtXMLParseFailed":     "libvirt: parsing domain XML %s: %v",
	"parse.lxdUnavailable":            "lxd unavailable: %s",
	"parse.lxdListFailed":             "lxd: lxc list returned code %d: %s",
	"parse.lxdListParseFailed":        "lxd: parsing the list: %v",
	"parse.podmanUnavailable":         "podman unavailable: %s",
	"parse.podmanListFailed":          "podman: container list returned HTTP %d",
	"parse.podmanListParseFailed":     "podman: parsing the container list: %v",
	"parse.dockerUnavailable":         "docker unavailable: neither an engine socket nor compose files found",
	"parse.noFirewallBackendReadable": "couldn't read iptables, ufw, or firewalld (needs root)",
	"parse.psNoParsedLines":           "ps ran, but not a single line could be parsed — it may have a different output format (busybox?)",
	"parse.noCgroupsRead":             "no /proc/<pid>/cgroup could be read — process origin (service/container/manual) can't be determined",
	"parse.memTotalNotFound":          "/proc/meminfo: MemTotal line not found",
	"parse.systemctlUnavailable":      "systemctl unavailable: couldn't read service state",

	"parse.certDirUnavailable":  "certificate directory unavailable: %v",
	"parse.certDirEmpty":        "certificate directory is empty",
	"parse.certFileUnavailable": "file unavailable: %v",
	"parse.certParseFailed":     "parsing the certificate: %v",
	"parse.noPEMCertsInFile":    "the file has no PEM-format certificates",

	"parse.renewalManualOutsideLE":   "path outside /etc/letsencrypt — renewal is probably manual",
	"parse.renewalConfMissing":       "the certificate is in /etc/letsencrypt/live/%s, but there's no renewal file /etc/letsencrypt/renewal/%s.conf — certbot won't renew it",
	"parse.renewalTimerActive":       "auto-renewal enabled: timer %s is active",
	"parse.renewalCronActive":        "auto-renewal enabled: cron job %s",
	"parse.renewalNoAutomationFound": "certbot knows about the certificate, but neither certbot.timer nor a cron job was found — renewal won't run on its own",
}
