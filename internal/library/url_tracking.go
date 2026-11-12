package library

import (
	"net/url"
	"strings"
)

// Query parameters that only identify who shared a link or which campaign it came
// from. They are removed from every stored address; the article is the same.
var trackingKeys = map[string]bool{
	"fbclid": true, "gclid": true, "dclid": true, "gbraid": true, "wbraid": true, "msclkid": true,
	"yclid": true, "twclid": true, "igshid": true, "mc_cid": true, "mc_eid": true,
	"_hsenc": true, "_hsmi": true, "mkt_tok": true, "ref_src": true,
}

// Parameters that are tracking only on some sites (on others `si`, `trk` or `rcm`
// may select content).
var hostTrackingKeys = []struct {
	suffix string
	keys   map[string]bool
}{
	{"linkedin.com", map[string]bool{"rcm": true, "trk": true, "trkemail": true, "lipi": true, "trackingid": true, "refid": true, "midtoken": true, "midsig": true, "otptoken": true, "eid": true}},
	{"youtube.com", map[string]bool{"si": true, "feature": true, "pp": true}},
	{"youtu.be", map[string]bool{"si": true, "feature": true, "pp": true}},
}

func isTrackingKey(host, key string) bool {
	key = strings.ToLower(key)
	if strings.HasPrefix(key, "utm_") || trackingKeys[key] {
		return true
	}
	host = strings.ToLower(host)
	for _, h := range hostTrackingKeys {
		if (host == h.suffix || strings.HasSuffix(host, "."+h.suffix)) && h.keys[key] {
			return true
		}
	}
	return false
}

// dropTracking removes tracking parameters from a raw query, keeping the order and
// encoding of the others.
func dropTracking(host, rawQuery string) (string, error) {
	var parts []string
	for _, part := range strings.Split(rawQuery, "&") {
		if part == "" {
			continue
		}
		key, err := url.QueryUnescape(strings.SplitN(part, "=", 2)[0])
		if err != nil {
			return "", err
		}
		if isTrackingKey(host, key) {
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "&"), nil
}

// cleanAddress is the address as the user gave it (fragment included) minus
// tracking parameters; an address that cannot be parsed is returned unchanged.
func cleanAddress(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return raw
	}
	query, err := dropTracking(u.Hostname(), u.RawQuery)
	if err != nil {
		return raw
	}
	u.RawQuery = query
	return u.String()
}
