package main

// Numbers for outgoing calls from the phones: normalisation to E.164 and a
// destination policy (protection against calls to premium-rate and foreign
// destinations); the same reading of a number for the incoming allow-list;
// the emergency numbers of the country.

import (
	"errors"
	"strings"
)

// numberPlan is how a number without "+" is read in the country of the line
// (sip.default_country): a leading 0 is the trunk prefix (the European way);
// a number of exactly nationalLen digits without it is a national number of a
// country without a trunk prefix (Chile: 9 digits); an emergency number stays
// as it was dialled. The zero plan changes nothing but "+" and "00".
type numberPlan struct {
	country     string
	nationalLen int               // 0 = no rule for national numbers without a trunk 0
	emergency   map[string]string // emergency number → RFC 5031 service, "sos" or "sos.police"
}

// countryNationalLen is the length of the national significant number in the
// countries that dial it without a trunk prefix. Chile: 9 digits for every
// number (mobile, landline, VoIP) and no trunk prefix, after the Wikipedia
// article "Telephone numbers in Chile" (a secondary source, not checked against
// a Subtel document). Countries not listed keep only the leading-0 rule;
// sip.national_len sets or switches off the rule for any country.
var countryNationalLen = map[string]int{"56": 9}

// countryEmergency are the emergency numbers of a country, with the RFC 5031
// service each one is (sip.emergency_urn); sip.emergency replaces the list.
// Chile, after the Spanish Wikipedia "Número de teléfono de emergencia" (no
// primary Chilean source could be read; to verify with the operator): 131 SAMU
// (ambulance), 132 Bomberos (fire), 133 Carabineros and 134 PDI (police), 136
// Socorro Andino (mountain rescue), 137 maritime search and rescue (marine),
// 138 air rescue (no RFC 5031 service of its own: plain sos), and 112 and 911,
// which mobile networks send on to 133. Information and help lines (139, 147,
// 149…) are not emergencies (RFC 5031: sos is not meant for public information
// or counselling) and follow 'dial' like any short number.
var countryEmergency = map[string][]string{
	"56": {"131=ambulance", "132=fire", "133=police", "134=police", "136=mountain", "137=marine", "138", "112", "911"},
}

// plan returns the number plan of the bridge's config.
func (c SIPConfig) plan() numberPlan {
	p := numberPlan{country: c.DefaultCountry, nationalLen: c.NationalLen}
	if p.nationalLen == 0 {
		p.nationalLen = countryNationalLen[c.DefaultCountry]
	}
	if p.nationalLen < 0 || p.country == "" {
		p.nationalLen = 0
	}
	list := c.Emergency
	if len(list) == 0 {
		list = countryEmergency[c.DefaultCountry]
	}
	if len(list) > 0 {
		p.emergency = map[string]string{}
		for _, e := range list {
			num, kind, _ := strings.Cut(strings.TrimSpace(e), "=")
			p.emergency[num] = emergencyService(kind)
		}
	}
	return p
}

// plan returns the number plan of the config; without the bridge only "+"
// and "00" are read.
func (c *Config) plan() numberPlan {
	if c.SIP == nil {
		return numberPlan{}
	}
	return c.SIP.plan()
}

// emergencyService is the service URN label for a kind of emergency service:
// "" → "sos", "police" → "sos.police".
func emergencyService(kind string) string {
	if kind == "" {
		return "sos"
	}
	return "sos." + kind
}

// emergencyFor returns the RFC 5031 service of an emergency number as dialled
// (digits only, no "+"); "" when it is not one.
func (p numberPlan) emergencyFor(number string) string {
	return p.emergency[number]
}

// normalizeDialed brings a dialled number to E.164: "+44 7700…"/"0044…" →
// "+447700…"; "07700…" → "+44" + "7700…" when default_country is set;
// "912345678" → "+56912345678" when the plan has a national length of 9 for
// country 56; emergency and other short service numbers stay as they are.
func normalizeDialed(raw string, p numberPlan) (string, error) {
	s := strings.TrimSpace(raw)
	if i := strings.IndexAny(s, ";@"); i >= 0 {
		s = s[:i] // tail of a URI user part (;phone-context=…)
	}
	var out string
	d := digitsOnly(s)
	switch {
	case !strings.HasPrefix(s, "+") && p.emergencyFor(d) != "":
		out = d // before the 0 rules: a configured emergency number may start with 0
	case strings.HasPrefix(s, "+"):
		out = "+" + digitsOnly(s[1:])
	case strings.HasPrefix(s, "00"):
		out = "+" + digitsOnly(s[2:])
	case strings.HasPrefix(s, "0") && p.country != "":
		out = "+" + p.country + digitsOnly(s[1:])
	case p.nationalLen > 0 && len(d) == p.nationalLen:
		out = "+" + p.country + d
	default:
		out = d
	}
	d = strings.TrimPrefix(out, "+")
	if len(d) < 2 || len(d) > 15 {
		return "", errors.New("the number is too short or too long")
	}
	// The original string may hold only digits, + and separators.
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !strings.ContainsRune("+-() .", r) {
			return "", errors.New("the number has characters that do not belong in one")
		}
	}
	return out, nil
}

// dialAllowed reports whether a number normalised by normalizeDialed (E.164
// for anything but short numbers) starts with an allowed prefix: the prefixes
// in 'dial' match the E.164 form, so "+56" allows "912345678" dialled in
// Chile. Short numbers (without "+") are allowed only when listed explicitly;
// "*" allows everything (the package default: the phones on the home network
// are our own). The emergency numbers do not go through here (callmgr.go).
func dialAllowed(allow []string, number string) bool {
	for _, p := range allow {
		if p == "*" {
			return true
		}
		if strings.HasPrefix(p, "+") {
			if strings.HasPrefix(number, "+") && strings.HasPrefix(number, p) {
				return true
			}
		} else if number == p {
			return true
		}
	}
	return false
}

// callerUser returns the user part for the From of an incoming call on the
// phone: the caller's number (with "+" if it had one) or anonymous.
func callerUser(from string) string {
	f := strings.TrimSpace(from)
	d := digitsOnly(f)
	switch {
	case d == "":
		return "anonymous"
	case strings.HasPrefix(f, "+"):
		return "+" + d
	case strings.HasPrefix(d, "0"):
		return d // national format: the phone calls back via default_country
	case len(d) >= 11:
		return "+" + d // international without "+"
	}
	return d
}

// e164Digits reads a number for the incoming allow-list as normalizeDialed
// does and returns its digits without the plus: "912345678" and
// "+56 9 1234 5678" are both "56912345678" in Chile. What normalizeDialed
// refuses is compared by its bare digits.
func e164Digits(s string, p numberPlan) string {
	if n, err := normalizeDialed(s, p); err == nil {
		return strings.TrimPrefix(n, "+")
	}
	return digitsOnly(s)
}
