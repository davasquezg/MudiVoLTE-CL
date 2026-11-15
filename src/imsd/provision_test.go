package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type lpEntry struct {
	Name      string `xml:"name,attr"`
	Overwrite string `xml:"overwrite,attr"`
	Value     string `xml:",chardata"`
}

type lpSection struct {
	Name    string    `xml:"name,attr"`
	Entries []lpEntry `xml:"entry"`
}

type lpConfig struct {
	XMLName  xml.Name    `xml:"config"`
	Sections []lpSection `xml:"section"`
}

func (c lpConfig) get(section, name string) string {
	for _, s := range c.Sections {
		if s.Name != section {
			continue
		}
		for _, e := range s.Entries {
			if e.Name == name {
				return e.Value
			}
		}
	}
	return ""
}

func provisionTestConfig(t *testing.T) SIPConfig {
	t.Helper()
	return SIPConfig{
		Listen:           "127.0.0.1:5060",
		Accounts:         []SIPAccount{{User: "pixel", Password: "s3cret-pass-1"}, {User: "laptop", Password: "an0ther-pass-2"}},
		ProvisionLinkDir: filepath.Join(t.TempDir(), "run", "links"),
	}.withDefaults()
}

// mintLink makes a link for phone user of sc at now, as the CLI does.
func mintLink(t *testing.T, sc SIPConfig, user string, now time.Time) string {
	t.Helper()
	for _, a := range sc.Accounts {
		if a.User == user {
			tok, err := newProvisionLink(sc.linkDir(), a, provisionLinkTTL, now)
			if err != nil {
				t.Fatal(err)
			}
			return tok
		}
	}
	t.Fatalf("no phone %q", user)
	return ""
}

// linkFiles lists the names in the link directory.
func linkFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

// fetch asks p for path with method from address from and returns the status.
func fetch(p *provisioner, method, from, path string) int {
	req, _ := http.NewRequest(method, "http://192.168.8.1:8060"+path, nil)
	req.RemoteAddr = from + ":5555"
	rec := &recorder{h: http.Header{}, code: http.StatusOK}
	p.ServeHTTP(rec, req)
	return rec.code
}

// A link is 96 random bits. On disk there is only a file named by its
// SHA-256, with the phone, a check of the password and the expiry, that only
// root can read.
func TestProvisionLinkFile(t *testing.T) {
	sc := provisionTestConfig(t)
	now := time.Now()
	a := mintLink(t, sc, "pixel", now)
	b := mintLink(t, sc, "pixel", now)
	if !isLowerHex(a, provisionTokenLen) || !isLowerHex(b, provisionTokenLen) || a == b {
		t.Fatalf("tokens %q and %q", a, b)
	}
	dir := sc.linkDir()
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("link directory: %v %v", st, err)
	}
	if st, err := os.Stat(linkFile(dir, a)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("link file: %v %v", st, err)
	}
	raw, _ := os.ReadFile(linkFile(dir, a))
	if bytes.Contains(raw, []byte(a)) || bytes.Contains(raw, []byte("s3cret-pass-1")) {
		t.Fatalf("the link file holds the token or the password: %s", raw)
	}
	l, err := readProvisionLink(linkFile(dir, a))
	if err != nil || l.User != "pixel" || l.Check != passwordCheck("pixel", "s3cret-pass-1") || l.Expires != now.Add(provisionLinkTTL).Unix() {
		t.Fatalf("link %+v, %v", l, err)
	}
	if passwordCheck("pixel", "s3cret-pass-1") == passwordCheck("pixel", "s3cret-pass-2") ||
		passwordCheck("pixel", "s3cret-pass-1") == passwordCheck("pixe", "ls3cret-pass-1") {
		t.Fatal("the password check must change with the password and the login")
	}
	if names := linkFiles(t, dir); len(names) != 2 {
		t.Fatalf("files %v, want two links", names)
	}
}

func TestPhoneProvisionURL(t *testing.T) {
	sc := provisionTestConfig(t)
	sc.ProvisionPort = 8060
	cfg := &Config{SIP: &sc}
	now := time.Now()
	// 15 minutes, or longer when asked (the router's display), at most a day.
	for _, c := range []struct{ ask, want time.Duration }{
		{0, provisionLinkTTL},
		{time.Minute, provisionLinkTTL},
		{time.Hour, time.Hour},
		{1000 * time.Hour, maxProvisionLinkTTL},
	} {
		url, err := phoneProvisionURL(cfg, "pixel", c.ask, now)
		if err != nil {
			t.Fatal(err)
		}
		tok, ok := strings.CutPrefix(url, "http://127.0.0.1:8060/p/")
		if !ok || !isLowerHex(tok, provisionTokenLen) {
			t.Fatalf("url %q", url)
		}
		l, err := readProvisionLink(linkFile(sc.linkDir(), tok))
		if err != nil || l.User != "pixel" || l.Expires != now.Add(c.want).Unix() {
			t.Fatalf("ttl %v: link %+v, %v; want it to expire in %v", c.ask, l, err, c.want)
		}
	}
	a, _ := phoneProvisionURL(cfg, "pixel", 0, now)
	b, _ := phoneProvisionURL(cfg, "pixel", 0, now)
	if a == b {
		t.Fatal("every call must make a new link")
	}
	if _, err := phoneProvisionURL(cfg, "nobody", 0, now); err == nil {
		t.Fatal("unknown phone must fail")
	}
	sc.ProvisionPort = 0
	if _, err := phoneProvisionURL(&Config{SIP: &sc}, "pixel", 0, now); err == nil {
		t.Fatal("provisioning off must fail")
	}
}

// mudi-volte phone url and phone screen get their links from this command.
func TestPhoneURLCommand(t *testing.T) {
	dir := t.TempDir()
	links := filepath.Join(dir, "links")
	conf := filepath.Join(dir, "config.json")
	cfg := `{"sip": {"listen": "127.0.0.1:5060", "provision_port": 8060, "provision_link_dir": "` + links + `",
		"accounts": [{"user": "pixel", "password": "s3cret-pass-1"}]}}`
	if err := os.WriteFile(conf, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, string) {
		t.Helper()
		out, err := os.CreateTemp(dir, "out")
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		stdout, stderr := os.Stdout, os.Stderr
		os.Stdout, os.Stderr = out, out
		rc := runTool(conf, args)
		os.Stdout, os.Stderr = stdout, stderr
		b, _ := os.ReadFile(out.Name())
		return rc, string(b)
	}
	for _, c := range []struct {
		args []string
		ttl  time.Duration
	}{
		{[]string{"phone-url", "pixel"}, provisionLinkTTL},
		{[]string{"phone-url", "-ttl", "3600", "pixel"}, time.Hour},
		{[]string{"phone-url", "-ttl", "999999999999", "pixel"}, maxProvisionLinkTTL},
	} {
		before := time.Now()
		rc, out := run(c.args...)
		tok, ok := strings.CutPrefix(strings.TrimSpace(out), "http://127.0.0.1:8060/p/")
		if rc != 0 || !ok || !isLowerHex(tok, provisionTokenLen) {
			t.Fatalf("%v: rc %d, output %q", c.args, rc, out)
		}
		l, err := readProvisionLink(linkFile(links, tok))
		if err != nil || l.Expires < before.Add(c.ttl).Unix() || l.Expires > time.Now().Add(c.ttl).Unix() {
			t.Fatalf("%v: link %+v, %v; want it to expire in %v", c.args, l, err, c.ttl)
		}
	}
	for args, want := range map[string]int{"phone-url": 2, "phone-url -ttl -5 pixel": 2, "phone-url pixel extra": 2, "phone-url nobody": 1} {
		if rc, out := run(strings.Fields(args)...); rc != want {
			t.Errorf("%s: rc %d, want %d (%q)", args, rc, want, out)
		}
	}
}

// A phone fetches its settings by the link once, and the HA1 in them passes
// our own digest; the same link refuses afterwards.
func TestProvisioningServesLinphoneConfig(t *testing.T) {
	sc := provisionTestConfig(t)
	p, err := startProvisioning(sc, 5062, 0, func() string { return "+447700900789" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	tok := mintLink(t, sc, "pixel", time.Now())
	port := p.addr.(*net.TCPAddr).Port
	base := "http://" + p.addr.String()
	client := &http.Client{Timeout: 5 * time.Second}

	res, err := client.Get(provisionURL(net.IPv4(127, 0, 0, 1), port, tok))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/xml") {
		t.Fatalf("status %d, type %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	var lp lpConfig
	if err := xml.Unmarshal(body, &lp); err != nil {
		t.Fatalf("xml: %v\n%s", err, body)
	}
	checks := map[[2]string]string{
		{"misc", "transient_provisioning"}: "1",
		{"sip", "default_proxy"}:           "0",
		{"auth_info_0", "username"}:        "pixel",
		{"auth_info_0", "realm"}:           "mudi",
		{"auth_info_0", "domain"}:          "127.0.0.1",
		{"auth_info_0", "algorithm"}:       "MD5",
		{"proxy_0", "reg_proxy"}:           "<sip:127.0.0.1:5062;transport=udp>",
		{"proxy_0", "reg_identity"}:        `"+447700900789" <sip:pixel@127.0.0.1>`,
		{"proxy_0", "reg_sendregister"}:    "1",
		{"app", "keep_service_alive"}:      "1",
		{"audio_codec_0", "mime"}:          "G722",
		{"audio_codec_0", "enabled"}:       "1",
		{"audio_codec_1", "mime"}:          "PCMU",
		{"audio_codec_2", "mime"}:          "PCMA",
		{"audio_codec_39", "mime"}:         "G722",
	}
	for k, want := range checks {
		if got := lp.get(k[0], k[1]); got != want {
			t.Errorf("%s/%s = %q, want %q", k[0], k[1], got, want)
		}
	}
	if bytes.Contains(body, []byte("s3cret-pass-1")) {
		t.Fatal("the plain password must not be in the XML")
	}
	// A phone with this HA1 passes the digest of our registrar.
	auth := newDigestAuth(sc.Realm, sc.Accounts)
	ha1 := lp.get("auth_info_0", "ha1")
	ch := parseDigestParams(strings.TrimPrefix(auth.challenge(false, "127.0.0.1"), "Digest "))
	nonce, uri, nc, cnonce := ch["nonce"], "sip:127.0.0.1", "00000001", "abcdef"
	resp := md5hex(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":auth:" + md5hex("REGISTER:"+uri))
	hdr := `Digest username="pixel", realm="mudi", nonce="` + nonce + `", uri="` + uri + `", response="` + resp +
		`", algorithm=MD5, qop=auth, nc=` + nc + `, cnonce="` + cnonce + `"`
	if user, ok, _ := auth.verify("REGISTER", hdr, "127.0.0.1", uri); !ok || user != "pixel" {
		t.Fatalf("HA1 from the XML is rejected by the registrar (user %q)", user)
	}

	for _, path := range []string{"/p/" + tok, "/p/" + strings.Repeat("0", provisionTokenLen), "/p/short", "/", "/p/"} {
		res, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, res.StatusCode)
		}
	}
	res, err = client.Post(base+"/p/x", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d", res.StatusCode)
	}
}

// What ends a link: a GET (a HEAD only checks it), the use of another link of
// the same phone, its 15 minutes, a new password and the removal of the phone.
func TestProvisioningLinkRefusals(t *testing.T) {
	sc := provisionTestConfig(t)
	now := time.Now()
	p := &provisioner{cfg: sc, dir: sc.linkDir(), ip: net.IPv4(192, 168, 8, 1), sipPort: 5060}
	get := func(tok string) int { return fetch(p, http.MethodGet, "192.168.8.20", "/p/"+tok) }

	a := mintLink(t, sc, "pixel", now)
	if code := fetch(p, http.MethodHead, "192.168.8.20", "/p/"+a); code != http.StatusOK {
		t.Fatalf("HEAD: status %d", code)
	}
	if code := get(a + ".xml"); code != http.StatusOK {
		t.Fatalf("first GET: status %d", code)
	}
	if code := get(a); code != http.StatusNotFound {
		t.Fatalf("second GET: status %d, want 404", code)
	}

	b1, b2 := mintLink(t, sc, "pixel", now), mintLink(t, sc, "pixel", now)
	c := mintLink(t, sc, "laptop", now)
	if get(b1) != http.StatusOK || get(b2) != http.StatusNotFound || get(c) != http.StatusOK {
		t.Fatal("a used link must end the other links of the same phone, and only those")
	}

	old := mintLink(t, sc, "pixel", now.Add(-provisionLinkTTL-time.Second))
	if code := get(old); code != http.StatusNotFound {
		t.Fatalf("expired link: status %d", code)
	}
	if _, err := os.Stat(linkFile(sc.linkDir(), old)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired link file left: %v", err)
	}

	d, e := mintLink(t, sc, "pixel", now), mintLink(t, sc, "laptop", now)
	p.cfg.Accounts = []SIPAccount{{User: "pixel", Password: "n3w-password-9"}}
	if code := get(d); code != http.StatusNotFound {
		t.Fatalf("link from before a new password: status %d", code)
	}
	if code := get(e); code != http.StatusNotFound {
		t.Fatalf("link of a removed phone: status %d", code)
	}
	if names := linkFiles(t, sc.linkDir()); len(names) != 0 {
		t.Fatalf("used and refused links left files: %v", names)
	}
}

// Of many fetches of one link at once, exactly one gets the settings.
func TestProvisioningLinkOneFetchWins(t *testing.T) {
	sc := provisionTestConfig(t)
	p := &provisioner{cfg: sc, dir: sc.linkDir(), ip: net.IPv4(192, 168, 8, 1), sipPort: 5060}
	tok := mintLink(t, sc, "pixel", time.Now())
	const n = 16
	codes := make(chan int, n)
	var start sync.WaitGroup
	start.Add(1)
	for i := 0; i < n; i++ {
		go func(i int) {
			start.Wait()
			codes <- fetch(p, http.MethodGet, fmt.Sprintf("192.168.8.%d", 20+i), "/p/"+tok)
		}(i)
	}
	start.Done()
	ok := 0
	for i := 0; i < n; i++ {
		if <-codes == http.StatusOK {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d of %d fetches got the settings, want 1", ok, n)
	}
}

// Beyond maxProvisionLinks a new link ends the ones that expire first.
// Expired links and broken files go with the next link, leftovers of a write
// once they are a minute old.
func TestProvisionLinkPrune(t *testing.T) {
	sc := provisionTestConfig(t)
	dir := sc.linkDir()
	now := time.Now()
	var toks []string
	for i := 0; i < maxProvisionLinks+4; i++ {
		toks = append(toks, mintLink(t, sc, "pixel", now.Add(time.Duration(i)*time.Second)))
	}
	if names := linkFiles(t, dir); len(names) != maxProvisionLinks {
		t.Fatalf("%d files, want %d", len(names), maxProvisionLinks)
	}
	at := now.Add(time.Duration(len(toks)) * time.Second)
	for i, tok := range toks {
		_, why := takeProvisionLink(dir, tok, sc.Accounts, at, false)
		if live := why == ""; live != (i >= 4) {
			t.Errorf("link %d: live %v (%q)", i, live, why)
		}
	}

	expired := mintLink(t, sc, "laptop", now.Add(-time.Hour))
	broken := filepath.Join(dir, strings.Repeat("ab", sha256.Size))
	staleTmp, freshTmp := filepath.Join(dir, ".new-1"), filepath.Join(dir, ".new-2")
	for _, f := range []string{broken, staleTmp, freshTmp} {
		if err := os.WriteFile(f, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(staleTmp, now.Add(-2*time.Minute), now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	mintLink(t, sc, "laptop", now)
	for f, want := range map[string]bool{linkFile(dir, expired): false, broken: false, staleTmp: false, freshTmp: true} {
		if _, err := os.Stat(f); (err == nil) != want {
			t.Errorf("%s: exists %v, want %v", filepath.Base(f), err == nil, want)
		}
	}
}

func TestProvisioningRejectsOtherSubnets(t *testing.T) {
	p := &provisioner{lan: &net.IPNet{IP: net.IPv4(192, 168, 8, 0), Mask: net.CIDRMask(24, 32)}}
	req, _ := http.NewRequest(http.MethodGet, "http://192.168.8.1:8060/p/x", nil)
	req.RemoteAddr = "192.168.9.20:5555"
	rec := &recorder{h: http.Header{}}
	p.ServeHTTP(rec, req)
	if rec.code != http.StatusForbidden {
		t.Fatalf("guest subnet: status %d", rec.code)
	}
}

// Wrong links from one address: after the tenth the address is blocked, even
// with the right link, which then still works from another address.
func TestProvisioningLimitsWrongLinks(t *testing.T) {
	sc := provisionTestConfig(t)
	p, err := startProvisioning(sc, 5062, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.lan = nil
	good := "/p/" + mintLink(t, sc, "pixel", time.Now())
	for i := 0; i < authFailLimit; i++ {
		if code := fetch(p, http.MethodGet, "192.168.8.50", "/p/"+strings.Repeat("0", provisionTokenLen)); code != http.StatusNotFound {
			t.Fatalf("wrong link %d: status %d", i+1, code)
		}
	}
	if code := fetch(p, http.MethodGet, "192.168.8.50", good); code != http.StatusTooManyRequests {
		t.Errorf("blocked address with the right link: status %d", code)
	}
	if code := fetch(p, http.MethodGet, "192.168.8.51", good); code != http.StatusOK {
		t.Errorf("another address: status %d", code)
	}
}

type recorder struct {
	h    http.Header
	code int
	buf  bytes.Buffer
}

func (r *recorder) Header() http.Header         { return r.h }
func (r *recorder) Write(b []byte) (int, error) { return r.buf.Write(b) }
func (r *recorder) WriteHeader(code int)        { r.code = code }

func TestQRRendering(t *testing.T) {
	const url = "http://192.168.8.1:8060/p/0123456789abcdef01234567"
	c, err := qrEncode(url)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := writeQRTerminal(&b, url); err != nil {
		t.Fatal(err)
	}
	n := c.Size + 2*qrQuiet
	if lines := strings.Count(b.String(), "\n"); lines != (n+1)/2 {
		t.Fatalf("terminal QR: %d lines, want %d", lines, (n+1)/2)
	}
	svg, err := qrSVG(url)
	if err != nil || !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, "h1v1h-1z") {
		t.Fatalf("svg: %v %.80s", err, svg)
	}
}

func TestRotatingLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "mudi-volte.log")
	r, err := openRotatingFile(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(strings.Repeat("x", 39) + "\n")
	for i := 0; i < 6; i++ {
		if _, err := r.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	r.Close()
	cur, _ := os.ReadFile(path)
	old, _ := os.ReadFile(path + ".1")
	if len(cur) != 80 || len(old) != 80 {
		t.Fatalf("current %d bytes, .1 %d bytes; want 80 and 80", len(cur), len(old))
	}
	// Opening again appends rather than truncates.
	r, _ = openRotatingFile(path, 1000)
	r.Write(line)
	r.Close()
	if cur, _ = os.ReadFile(path); len(cur) != 120 {
		t.Fatalf("append after reopen: %d bytes", len(cur))
	}
}
