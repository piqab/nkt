package msgs

// Messages of hub API tokens and external access — a separate file.
func init() {
	for k, v := range enAPICatalog {
		enCatalog[k] = v
	}
}

var enAPICatalog = map[string]string{
	"auth.tokensUnsupported": "Only the hub accepts API tokens",
	"auth.tokenInvalid":      "Invalid API token or request signature",
	"auth.tokenTooMany":      "Too many invalid tokens from this address; wait a few minutes",
	"auth.tokenExpired":      "API token \"%s\" has expired",
	"auth.tokenIPDenied":     "The API token is not allowed from %s",
	"auth.tokenStale":        "The request signature is stale: the client and hub clocks differ by more than 5 minutes",
	"auth.tokenReplay":       "This signature has already been used: use a new nonce for every request",
	"auth.tokenBodyTooLarge": "A signed request body is limited to 1 MB",
	"auth.tokenRouteDenied":  "The API token cannot call %s %s",
	"auth.tokenReadOnly":     "The API token is read-only: actions are not allowed",
	"auth.tokenHostDenied":   "The host is outside the API token's scope",
	"auth.tokenDryContent":   "A scoped API token can dry-run only a saved pipeline (pipeline_id, no content)",
	"hub.tokenBadName":       "A token name is 1 to 64 characters on one line",
	"hub.tokenBadRole":       "A token role is read or admin, not %q",
	"hub.tokenBadExpiry":     "A token lifetime is 0 (no expiry) to 3650 days, not %d",
	"hub.tokenBadHost":       "No host with number %d",
	"hub.tokenBadGroup":      "No group %q",
	"hub.tokenBadIP":         "Not an address or subnet: %q",
	"hub.tokenNameTaken":     "Token \"%s\" already exists",
	"hub.tokenMissing":       "No token #%d",
	"hub.jobMissing":         "No job #%d",
	"auth.tokenEdgeSigned":   "Through nkt-edge only signed token requests (X-NKT-API-*) are accepted, no Bearer or cookies",
	"auth.tokenEdgeDenied":   "Token \"%s\" is not allowed through nkt-edge %s",
	"edge.apiPathDenied":     "Through nkt-edge only token API calls are available: /api/auth/me, /api/hub/…, /api/hosts/…",
	"edge.apiNoClient":       "nkt-edge did not pass the client address",
	"edge.missing":           "No edge #%d",
	"edge.badRoles":          "Unknown edge roles: %s",
	"edge.manualIncomplete":  "An edge needs a tunnel address and a token (EDGE_TOKEN)",
}
