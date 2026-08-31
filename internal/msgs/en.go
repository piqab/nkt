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
	"pkgInstall.unknownService":            "unknown service: %s",
	"pkgInstall.serviceAlreadyInstalled":   "%s is already installed",
	"pkgInstall.unknownPackage":            "unknown package: %s",
	"pkgInstall.noPackagesSelected":        "no packages selected",

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

	"finding.portConflict.title":      "Port %d conflict between %s and %s",
	"finding.portConflict.detail":     "%s (%s, %s:%d) and %s (%s, %s:%d) declare the same port. The second service won't be able to bind the socket and will fail to start.",
	"finding.portConflict.suggestion": "Split the services across different ports or bind addresses.",

	"finding.declaredNotListening.title":      "Port %d is declared in the config, but nothing is listening on it",
	"finding.declaredNotListening.detail":     "%s (%s) declares %s, but there's no such listener in ss's output. Either the config wasn't applied (needs a reload), or the service failed to bind the port.",
	"finding.declaredNotListening.suggestion": "Check whether the config was applied: reload %s and check its log.",

	"finding.listeningNotDeclared.title":        "Unaccounted-for listener on port %d (%s)",
	"finding.listeningNotDeclared.detail":       "Process %s is listening on %s:%d, but this port isn't described in any parsed config.",
	"finding.listeningNotDeclared.detailPublic": "Process %s is listening on %s:%d, but this port isn't described in any parsed config. The socket is open on all interfaces.",
	"finding.listeningNotDeclared.suggestion":   "Make sure the service is actually needed, and describe it in the config or close the port.",

	"finding.noDefaultDeny.title":      "Default INPUT policy is ACCEPT, and no firewall manager is active",
	"finding.noDefaultDeny.detail":     "Incoming traffic is allowed by default: any open port is reachable from outside, whether you intended that or not.",
	"finding.noDefaultDeny.suggestion": "Enable ufw (ufw default deny incoming) or firewalld, or set iptables -P INPUT DROP and explicitly allow the ports you need.",

	"finding.publicPortBlocked.title":      "Port %d is open on the service, but blocked by the firewall",
	"finding.publicPortBlocked.detail":     "%s (%s) listens on %s on all interfaces, but there are no rules allowing incoming traffic to this port, and the INPUT policy is %s. The service is unreachable from outside.",
	"finding.publicPortBlocked.suggestion": "If the service should be reachable: ufw allow %d/tcp.",

	"finding.dockerBypassesFirewall.title":           "Container %s publishes port %d on 0.0.0.0, bypassing the firewall",
	"finding.dockerBypassesFirewall.detail":          "Docker adds DNAT rules to the PREROUTING/FORWARD chain, so published port %d never passes through INPUT and isn't blocked by ufw rules.",
	"finding.dockerBypassesFirewall.detailSensitive": "Docker adds DNAT rules to the PREROUTING/FORWARD chain, so published port %d never passes through INPUT and isn't blocked by ufw rules. %s runs on this port — publishing it externally is almost certainly not intended.",
	"finding.dockerBypassesFirewall.suggestion":      "Bind the publication to localhost (\"127.0.0.1:%d:%d\") or use the DOCKER-USER chain for filtering.",

	"finding.staleFirewallRule.title":             "Firewall rule for port %d is unused",
	"finding.staleFirewallRule.detail":            "The rule allows incoming traffic on port %d, but no process on the host is listening on it.",
	"finding.staleFirewallRule.detailZeroPackets": "The rule allows incoming traffic on port %d, but no process on the host is listening on it. The rule's packet counter is zero — no traffic has ever hit it.",
	"finding.staleFirewallRule.suggestion":        "Delete the rule if the service is no longer needed: ufw delete allow %d/tcp.",

	"finding.sensitivePortPublic.title":           "%s is listening on port %d on all interfaces",
	"finding.sensitivePortPublic.detail":          "Process %s accepts connections on 0.0.0.0:%d. Services like this usually aren't meant for public access and have no brute-force protection of their own.",
	"finding.sensitivePortPublic.detailReachable": "Process %s accepts connections on 0.0.0.0:%d. The port isn't blocked by any firewall rule. Services like this usually aren't meant for public access and have no brute-force protection of their own.",
	"finding.sensitivePortPublic.suggestion":      "Bind the service to 127.0.0.1 or an internal network, and close the port on the firewall.",

	"finding.weakTLS.title":      "Outdated TLS versions enabled: %s",
	"finding.weakTLS.detail":     "ssl_protocols = %q. These versions are considered unsafe and are disabled in modern browsers.",
	"finding.weakTLS.suggestion": "Keep only TLSv1.2 and TLSv1.3: ssl_protocols TLSv1.2 TLSv1.3;",

	"finding.missingHSTS.title":      "No HSTS header on %s",
	"finding.missingHSTS.detail":     "The TLS server doesn't send Strict-Transport-Security, so a client can be redirected back to http.",
	"finding.missingHSTS.suggestion": `add_header Strict-Transport-Security "max-age=31536000" always;`,

	"finding.tlsCertMissing.title":      "listen ... ssl without ssl_certificate on %s",
	"finding.tlsCertMissing.detail":     "The listener is declared as TLS, but no certificate is set in the block — nginx won't start.",
	"finding.tlsCertMissing.suggestion": "Add ssl_certificate and ssl_certificate_key — a quick way to get the files: generate a self-signed certificate on the \"Certificates\" page.",

	"finding.tlsCertUnreadable.title":      "Certificate can't be read: %s",
	"finding.tlsCertUnreadable.detail":     "%s is set in the configuration for %s, but couldn't be read: %s. If the file genuinely doesn't exist, the service won't bring up its TLS listener.",
	"finding.tlsCertUnreadable.suggestion": "Check the path and permissions, and reissue the certificate if needed.",

	"finding.tlsCertExpired.title":  "Certificate %s expired %d day(s) ago",
	"finding.tlsCertExpired.detail": "Validity ended %s. Serves %s. Browsers show an error and block users from proceeding.",

	"finding.tlsCertExpiring.title":          "Certificate %s expires in %d day(s)",
	"finding.tlsCertExpiring.detail":         "Valid until %s, serves %s. %s",
	"finding.tlsCertExpiring.detailWithTime": "Valid until %s, serves %s. %s",

	"finding.tlsCertNotYetValid.title":      "Certificate isn't valid yet: %s",
	"finding.tlsCertNotYetValid.detail":     "Only valid from %s. This usually means the host's clock is behind, or the certificate was issued for the future.",
	"finding.tlsCertNotYetValid.suggestion": "Check the system time and the certificate's issue date.",

	"finding.tlsCertRenewalNotAutomatic.title":      "Certificate auto-renewal isn't running: %s",
	"finding.tlsCertRenewalNotAutomatic.suggestion": "Enable the timer: systemctl enable --now certbot.timer — or add a cron job.",

	"finding.tlsCertOrphanLineage.title":      "certbot certificate is missing its renewal file",
	"finding.tlsCertOrphanLineage.suggestion": "Reissue the certificate via certbot certonly to restore the renewal record.",

	"finding.tlsCertNotReloaded.title":      "The socket serves a different certificate than the one in the config",
	"finding.tlsCertNotReloaded.detail":     "File %s doesn't match what %s actually serves on a TLS connection: the socket presents a certificate with serial %s, valid until %s. This usually means the file on disk was updated (e.g. certbot renew) but the service never reread its config.",
	"finding.tlsCertNotReloaded.suggestion": "Reload %s so it picks up the current certificate.",

	"finding.tlsCertSelfSigned.title":      "Self-signed certificate on %s",
	"finding.tlsCertSelfSigned.detail":     "The issuer matches the subject (%s). No browser trusts a certificate like this; that's fine for internal services, not for public ones.",
	"finding.tlsCertSelfSigned.suggestion": "For a public service, issue a certificate from a trusted CA.",

	"finding.tlsCertWeakKey.title":      "Weak RSA key: %d bits",
	"finding.tlsCertWeakKey.detail":     "Certificate %s uses a key shorter than %d bits. Modern clients reject connections like this.",
	"finding.tlsCertWeakKey.suggestion": "Reissue the certificate with an RSA 2048+ or ECDSA P-256 key.",

	"finding.tlsCertWeakSignature.title":      "Outdated signature algorithm: %s",
	"finding.tlsCertWeakSignature.detail":     "SHA-1- and MD5-based signatures are considered unsafe and aren't accepted by modern browsers.",
	"finding.tlsCertWeakSignature.suggestion": "Reissue the certificate with a SHA-256 (or stronger) signature.",

	"finding.tlsCertNameMismatch.title":      "Certificate doesn't cover name %s",
	"finding.tlsCertNameMismatch.detail":     "The server responds on %s, but certificate %s was issued for %s. The client will see a name-mismatch warning.",
	"finding.tlsCertNameMismatch.suggestion": "Add %s to the certificate's SAN, or use a separate certificate.",

	"finding.renewalSuggestionCertbot": "Renew it now: certbot renew --cert-name %s, then reload the service.",
	"finding.renewalSuggestionManual":  "Issue and install a new certificate, then reload the service.",

	"finding.publicPlaintextProxy.title":      "%s proxies traffic over plain HTTP, no TLS",
	"finding.publicPlaintextProxy.detail":     "Listener %s accepts requests on all interfaces without encryption and forwards them onward. Headers, cookies, and tokens travel in plain text.",
	"finding.publicPlaintextProxy.suggestion": "Move the service to https (for a quick test, generate a self-signed certificate on the \"Certificates\" page), or leave port 80 as a redirect to https only.",

	"finding.upstreamUndefined.title":      "Reference to a nonexistent upstream %q",
	"finding.upstreamUndefined.detail":     "Route %q in %s points at pool %q, but no such pool is defined.",
	"finding.upstreamUndefined.suggestion": "Check the pool name, or add the corresponding upstream/backend block.",

	"finding.upstreamOrphan.title":      "Pool %q is declared but never used",
	"finding.upstreamOrphan.detail":     "No route references this pool — likely a leftover from an earlier config.",
	"finding.upstreamOrphan.suggestion": "Remove the unused block, or wire it up to the route that needs it.",

	"finding.upstreamMemberDown.title":      "Backend %s of pool %q isn't listening on its port",
	"finding.upstreamMemberDown.detail":     "Pool %q sends traffic to %s, but locally nothing is bound to that port. Requests will fail with a 502/504.",
	"finding.upstreamMemberDown.suggestion": "Start a service on that port, or remove it from the pool.",

	"finding.singleBackend.title":      "Pool %q has a single server — no redundancy",
	"finding.singleBackend.detail":     "Losing the only backend takes the whole route down.",
	"finding.singleBackend.suggestion": "Add a second server, or an explicit backup.",

	"finding.allBackendsDisabled.title":      "Every server in pool %q is marked down/backup",
	"finding.allBackendsDisabled.detail":     "No active servers remain — all traffic to this pool will be rejected.",
	"finding.allBackendsDisabled.suggestion": "Bring at least one server back into service.",

	"finding.backendNoHealthcheck.title":             "Pool %q has no server health check",
	"finding.backendNoHealthcheck.detail":            "%d of %d servers have a check. The load balancer will keep sending requests to a downed backend.",
	"finding.backendNoHealthcheck.suggestionHAProxy": "Add the check parameter to each server line, and option httpchk in the backend.",
	"finding.backendNoHealthcheck.suggestionNginx":   "Set max_fails and fail_timeout for passive checking.",
	"finding.backendNoHealthcheck.suggestionCaddy":   "Add health_uri/health_interval to reverse_proxy for active checking.",

	"finding.containerRestarting.title":      "Container %s is stuck in a restart loop",
	"finding.containerRestarting.detail":     "Status: %s. Usually a port conflict, a config error, or the process crashing on startup.",
	"finding.containerRestarting.suggestion": "Check its log: docker logs %s.",

	"finding.containerNotRunning.title":      "Container %s is declared in compose, but isn't running",
	"finding.containerNotRunning.detail":     "Service %q from file %s is missing from the running containers.",
	"finding.containerNotRunning.suggestion": "Start the stack: docker compose up -d.",

	"finding.containerUndeclared.title":      "Container %s is running outside any compose files",
	"finding.containerUndeclared.detail":     "The container is running, but isn't described in any known compose file — its state isn't reproducible.",
	"finding.containerUndeclared.suggestion": "Describe the container in compose, or add its file to NKT_COMPOSE_FILES.",

	"finding.containerNoRestartPolicy.title":      "Container %s has no restart policy set",
	"finding.containerNoRestartPolicy.detail":     "After a host reboot or a process crash, the container won't come back on its own.",
	"finding.containerNoRestartPolicy.suggestion": "Add restart: unless-stopped.",

	"finding.adminInterfaceOpen.title":      "Stats panel %s is reachable without a password",
	"finding.adminInterfaceOpen.detail":     "Section %q enables stats and listens on %s, but there's no stats auth directive. Anyone who reaches the port sees the backend composition and service state.",
	"finding.adminInterfaceOpen.suggestion": "Add stats auth <user>:<password> and bind it to an internal address.",
}
