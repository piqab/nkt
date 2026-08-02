package model

import "golang.org/x/net/idna"

// HostnameASCII converts a human-typed hostname to the ASCII (punycode) form
// that DNS, TLS SNI and X.509 SANs actually carry on the wire — a browser
// never sends "испытание.рф" in a ClientHello, it sends
// "xn--80akhbyknj4f.xn--p1ai". Already-ASCII input passes through unchanged.
func HostnameASCII(name string) (string, error) {
	return idna.Lookup.ToASCII(name)
}

// HostnameUnicode decodes a punycode label back to its readable form for
// display next to the ASCII name the app actually has to use, e.g.
// "xn--80akhbyknj4f.xn--p1ai" -> "испытание.рф". Returns "" when there is
// nothing to show — the name has no punycode label, or fails to decode —
// so callers can treat an empty result as "no footnote needed".
func HostnameUnicode(name string) string {
	u, err := idna.ToUnicode(name)
	if err != nil || u == name {
		return ""
	}
	return u
}
