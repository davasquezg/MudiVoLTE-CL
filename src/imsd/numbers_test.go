package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo"
)

// Chile dials 9 digits without a trunk 0: they become E.164 with country 56;
// emergency and other short numbers stay as dialled; the European leading-0
// rule still works.
func TestNormalizeChile(t *testing.T) {
	cl := SIPConfig{DefaultCountry: "56"}.plan()
	cases := map[string]string{
		"912345678":       "+56912345678", // mobile
		"9 1234 5678":     "+56912345678",
		"221234567":       "+56221234567", // Santiago landline
		"+56 9 1234 5678": "+56912345678",
		"0056912345678":   "+56912345678",
		"+447700900456":   "+447700900456", // foreign stays foreign
		"133":             "133",           // emergency
		"149":             "149",           // help line: short, untouched
		"600":             "600",           // echo test
		"1234":            "1234",
		"12345678":        "12345678",   // 8 digits: not national
		"5691234567":      "5691234567", // 10 digits: not national
	}
	for in, want := range cases {
		if got, err := normalizeDialed(in, cl); err != nil || got != want {
			t.Errorf("Chile: %q → %q (%v), want %q", in, got, err, want)
		}
	}
	// The UK keeps the trunk 0 rule and gets no national-length rule.
	uk := SIPConfig{DefaultCountry: "44"}.plan()
	for in, want := range map[string]string{"07700900456": "+447700900456", "912345678": "912345678"} {
		if got, err := normalizeDialed(in, uk); err != nil || got != want {
			t.Errorf("UK: %q → %q (%v), want %q", in, got, err, want)
		}
	}
	// Without a country nothing national is read.
	if got, _ := normalizeDialed("912345678", numberPlan{}); got != "912345678" {
		t.Errorf("no country: %q", got)
	}
	// national_len: -1 switches the rule off, a length sets it for any country.
	if got, _ := normalizeDialed("912345678", SIPConfig{DefaultCountry: "56", NationalLen: -1}.plan()); got != "912345678" {
		t.Errorf("national_len -1: %q", got)
	}
	if got, _ := normalizeDialed("71234567", SIPConfig{DefaultCountry: "99", NationalLen: 8}.plan()); got != "+9971234567" {
		t.Errorf("national_len 8: %q", got)
	}
}

// 'dial' matches the E.164 form: '+56' allows a national number dialled in
// Chile and still bars foreign ones.
func TestDialAllowE164Chile(t *testing.T) {
	cl := SIPConfig{DefaultCountry: "56"}.plan()
	for in, want := range map[string]bool{"912345678": true, "221234567": true, "+447700900456": false, "0044 7700 900456": false} {
		num, err := normalizeDialed(in, cl)
		if err != nil || dialAllowed([]string{"+56"}, num) != want {
			t.Errorf("dial +56, %q (%q, %v): want %v", in, num, err, want)
		}
	}
}

func TestEmergencyPlan(t *testing.T) {
	cl := SIPConfig{DefaultCountry: "56"}.plan()
	for num, want := range map[string]string{
		"131": "sos.ambulance", "132": "sos.fire", "133": "sos.police", "134": "sos.police",
		"136": "sos.mountain", "137": "sos.marine", "138": "sos", "112": "sos", "911": "sos",
		"139": "", "147": "", "149": "", "600": "", "+56133": "",
	} {
		if got := cl.emergencyFor(num); got != want {
			t.Errorf("Chile %s: %q, want %q", num, got, want)
		}
	}
	// Other countries have no default list; sip.emergency replaces the list.
	if (SIPConfig{DefaultCountry: "44"}).plan().emergencyFor("112") != "" {
		t.Error("UK: a default emergency list nobody checked")
	}
	own := SIPConfig{DefaultCountry: "56", Emergency: []string{"112", " 999=police "}}.plan()
	if own.emergencyFor("999") != "sos.police" || own.emergencyFor("112") != "sos" || own.emergencyFor("133") != "" {
		t.Errorf("own list: %+v", own.emergency)
	}
	// A configured emergency number is never read as a trunk-0 number.
	zero := SIPConfig{DefaultCountry: "44", Emergency: []string{"000"}}.plan()
	if got, err := normalizeDialed("000", zero); err != nil || got != "000" {
		t.Errorf("000 → %q (%v)", got, err)
	}
}

func TestNumberPlanValidation(t *testing.T) {
	base := SIPConfig{
		Listen: "127.0.0.1:5060", Accounts: []SIPAccount{{User: "pixel", Password: testPass, Ext: "201"}},
		DialAllow: []string{"+56"}, DefaultCountry: "56", EchoExt: "600",
	}.withDefaults()
	if err := base.validate(); err != nil {
		t.Fatalf("base: %v", err)
	}
	good := []func(*SIPConfig){
		func(c *SIPConfig) { c.NationalLen = -1 },
		func(c *SIPConfig) { c.NationalLen = 9 },
		func(c *SIPConfig) { c.Emergency = []string{"133=police", "112", "131=ambulance"} },
	}
	for i, f := range good {
		c := base
		f(&c)
		if err := c.validate(); err != nil {
			t.Errorf("good %d: %v", i, err)
		}
	}
	bad := []func(*SIPConfig){
		func(c *SIPConfig) { c.NationalLen = -2 },
		func(c *SIPConfig) { c.NationalLen = 3 },
		func(c *SIPConfig) { c.NationalLen = 14 }, // 56 + 14 digits > 15
		func(c *SIPConfig) { c.NationalLen = 9; c.DefaultCountry = "" },
		func(c *SIPConfig) { c.Emergency = []string{"1"} },
		func(c *SIPConfig) { c.Emergency = []string{"13a"} },
		func(c *SIPConfig) { c.Emergency = []string{"133=Police"} },
		func(c *SIPConfig) { c.Emergency = []string{"133="} },
		func(c *SIPConfig) { c.Emergency = []string{"600"} }, // the echo test
		func(c *SIPConfig) { c.Emergency = []string{"201"} }, // a phone
	}
	for i, f := range bad {
		c := base
		f(&c)
		if err := c.validate(); err == nil {
			t.Errorf("bad %d accepted: %+v", i, c)
		}
	}
}

// The incoming allow-list reads both sides with the number plan (issue #3).
func TestNumberAllowedChile(t *testing.T) {
	cl := SIPConfig{DefaultCountry: "56"}.plan()
	cases := []struct {
		name  string
		allow []string
		from  string
		plan  numberPlan
		want  bool
	}{
		{"list E.164, caller national", []string{"+56912345678"}, "912345678", cl, true},
		{"list national, caller E.164", []string{"912345678"}, "+56912345678", cl, true},
		{"both E.164", []string{"+56 9 1234 5678"}, "+56912345678", cl, true},
		{"both national", []string{"9 1234 5678"}, "912345678", cl, true},
		{"other number", []string{"+56912345678"}, "912345679", cl, false},
		{"foreign caller, same tail", []string{"+56912345678"}, "+44912345678", cl, false},
		{"foreign entry, national caller", []string{"+44912345678"}, "912345678", cl, false},
		{"foreign both", []string{"+447700900123"}, "+447700900123", cl, true},
		{"short entry, exact", []string{"1500"}, "1500", cl, true},
		{"short entry, no suffix match", []string{"5678"}, "912345678", cl, false},
		{"short caller", []string{"+56912345678"}, "5678", cl, false},
		{"hidden caller", []string{"+56912345678"}, "", cl, false},
		{"anonymous caller", []string{"+56912345678"}, "anonymous", cl, false},
		{"anonymous SIP URI", []string{"+56912345678"}, "sip:anonymous@anonymous.invalid", cl, false},
		// Without a plan (no default_country), a caller ID of 9 digits or more
		// still matches the end of an E.164 entry; a shorter one does not.
		{"no plan, caller national", []string{"+56912345678"}, "912345678", numberPlan{}, true},
		{"no plan, 8 digits", []string{"+56912345678"}, "12345678", numberPlan{}, false},
		// The UK reads a trunk 0 on either side.
		{"UK, caller national", []string{"+447700900123"}, "07700900123", SIPConfig{DefaultCountry: "44"}.plan(), true},
	}
	for _, tc := range cases {
		if got := numberAllowed(tc.allow, tc.from, tc.plan); got != tc.want {
			t.Errorf("%s: numberAllowed(%q, %q) = %v, want %v", tc.name, tc.allow, tc.from, got, tc.want)
		}
	}
}

// chileRig is a bridge for Chile: dial '+56', country 56.
func chileRig(t *testing.T, edit func(*SIPConfig)) *testRig {
	return newTestRigWith(t, &Config{}, func(c *SIPConfig) {
		c.DialAllow, c.DefaultCountry = []string{"+56"}, "56"
		if edit != nil {
			edit(c)
		}
	})
}

// A phone dials a Chilean number nationally: the network gets E.164 and
// 'dial' +56 lets it through (issue #2).
func TestSIPOutgoingChileNational(t *testing.T) {
	rig := chileRig(t, nil)
	d := &fakeDialer{mgr: rig.mgr, call: newFakeIMSCall("out-cl", CallOutgoing), failCode: 486}
	rig.mgr.dialer = d
	ph := newTestPhone(t)
	if res, err := ph.register(rig.srvAddr, testPass); err != nil || res.StatusCode != 200 {
		t.Fatalf("registration: %v %v", res, err)
	}
	_, _, err := ph.call(rig.srvAddr, "912345678")
	var de *sipgo.ErrDialogResponse
	if !errors.As(err, &de) || de.Res.StatusCode != 486 {
		t.Fatalf("national number: %v", err)
	}
	if to, _ := d.to.Load().(string); to != "+56912345678" {
		t.Fatalf("Dial went to %q", to)
	}
	if uri, _ := d.uri.Load().(string); uri != "" {
		t.Fatalf("Request-URI %q for an ordinary call", uri)
	}
	_, _, err = ph.call(rig.srvAddr, "+447700900456")
	if !errors.As(err, &de) || de.Res.StatusCode != 403 {
		t.Fatalf("foreign number with dial +56: %v", err)
	}
}

// Emergency numbers pass whatever 'dial' says (even no outgoing calls at
// all), go out as dialled, and only with emergency_urn to urn:service:sos…
// (issue #5).
func TestSIPOutgoingEmergency(t *testing.T) {
	t.Run("tel", func(t *testing.T) { testSIPOutgoingEmergency(t, false) })
	t.Run("urn", func(t *testing.T) { testSIPOutgoingEmergency(t, true) })
}

func testSIPOutgoingEmergency(t *testing.T, urn bool) {
	rig := chileRig(t, func(c *SIPConfig) { c.DialAllow, c.EmergencyURN = nil, urn })
	d := &fakeDialer{mgr: rig.mgr, call: newFakeIMSCall("out-sos", CallOutgoing), failCode: 486}
	rig.mgr.dialer = d
	ph := newTestPhone(t)
	if res, err := ph.register(rig.srvAddr, testPass); err != nil || res.StatusCode != 200 {
		t.Fatalf("registration: %v %v", res, err)
	}
	_, _, err := ph.call(rig.srvAddr, "133")
	var de *sipgo.ErrDialogResponse
	if !errors.As(err, &de) || de.Res.StatusCode != 486 {
		t.Fatalf("urn %v: 133 did not reach the network: %v", urn, err)
	}
	wantURI := ""
	if urn {
		wantURI = "urn:service:sos.police"
	}
	if to, _ := d.to.Load().(string); to != "133" {
		t.Fatalf("urn %v: Dial went to %q", urn, to)
	}
	if uri, _ := d.uri.Load().(string); uri != wantURI {
		t.Fatalf("urn %v: Request-URI %q, want %q", urn, uri, wantURI)
	}
	// A help line is no emergency: with no outgoing calls it is barred.
	_, _, err = ph.call(rig.srvAddr, "149")
	if !errors.As(err, &de) || de.Res.StatusCode != 403 {
		t.Fatalf("urn %v: 149: %v", urn, err)
	}
	waitFor(t, func() bool { return rig.mgr.current() == nil }, "the line was not released")
}

// An SMS to a national number goes into the spool as an international number
// (To: +56…, To_TOA: international), allowed by 'dial' +56.
func TestSIPMessageChileNational(t *testing.T) {
	rig := chileRig(t, nil)
	c := smsTestDirs(t)
	b := newSMSBridge(c, filepath.Join(t.TempDir(), "inbox.jsonl"), rig.lan, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.sendWait, b.watchLate = 300*time.Millisecond, 300*time.Millisecond
	rig.lan.setSMS(b, b.phoneOnline)
	pixel := newTestPhoneUser(t, "127.0.0.1", "pixel")
	if res, err := pixel.register(rig.srvAddr, testPass); err != nil || res.StatusCode != 200 {
		t.Fatalf("register: %v %v", res, err)
	}
	res, err := pixel.message(rig.srvAddr, "912345678", "text/plain", []byte("Hola"))
	if err != nil || res.StatusCode != 202 {
		t.Fatalf("message: %v %v", res, err)
	}
	es, _ := os.ReadDir(c.Outgoing)
	if len(es) != 1 {
		t.Fatalf("outgoing holds %d files", len(es))
	}
	got, _ := os.ReadFile(filepath.Join(c.Outgoing, es[0].Name()))
	if !strings.HasPrefix(string(got), "To: +56912345678\n") || !strings.Contains(string(got), "\nTo_TOA: international\n") {
		t.Fatalf("spool file:\n%s", got)
	}
}
