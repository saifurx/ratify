package lead

import "strings"

// The same lists exist in agents/pyworker/features.py (feature extraction);
// keep them in sync.
var freeMail = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "yahoo.com": true, "outlook.com": true, "hotmail.com": true,
	"live.com": true, "msn.com": true, "icloud.com": true, "me.com": true, "aol.com": true, "proton.me": true,
	"protonmail.com": true, "gmx.com": true, "mail.com": true, "yandex.com": true, "zoho.com": true,
}

var disposable = map[string]bool{
	"mailinator.com": true, "guerrillamail.com": true, "10minutemail.com": true, "tempmail.com": true,
	"yopmail.com": true, "trashmail.com": true, "sharklasers.com": true, "getnada.com": true,
}

// EmailDomainType returns "corporate", "free" or "disposable".
func EmailDomainType(domain string) string {
	d := strings.ToLower(domain)
	switch {
	case disposable[d]:
		return "disposable"
	case freeMail[d]:
		return "free"
	}
	return "corporate"
}

// CleanDomain normalises a user-supplied company domain/URL to a bare host.
func CleanDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, p)
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "www.")
	if len(s) > 253 || !strings.Contains(s, ".") || strings.ContainsAny(s, " @:") {
		return ""
	}
	return s
}
