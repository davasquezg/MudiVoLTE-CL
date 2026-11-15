package main

// Phone setup by QR code. Linphone (Android, iOS, desktop) supports remote
// provisioning: it fetches XML in the linphonerc format from the link in the
// QR code and applies it, with transient_provisioning=1 only once, without
// downloading it again at every start (wiki.linphone.org, "Remote
// Provisioning"). Link: http://<LAN>:<provision_port>/p/<token>, where the
// token is 96 random bits, new each time a QR code is shown (mudi-volte phone
// show, url and screen, the web pages). A link works once and for 15 minutes
// (longer when asked, for the router's display): the first fetch uses it up
// together with the phone's other links, and a link made before the phone
// was removed or got a new password does not work. Each link is a file in
// /var/run/mudi-volte/links named by the SHA-256 of its token: the command
// that makes a link needs no running daemon, a reboot ends every link, and
// the removal of the file decides which of two fetches at once gets the
// settings. The server listens only on the LAN address and answers only
// addresses from the LAN subnet. Instead of the password the XML carries the
// digest HA1 (MD5 of login:realm:password), as in the Linphone sample. The
// link is plain HTTP: before it the phone has nothing to check a certificate
// of the router against, so the moment of setup is the one time the sign-in
// data crosses the Wi-Fi in the clear. As the link then stops working, a
// photo of the code or a link read off the Wi-Fi cannot fetch the settings
// again later.

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	defaultProvisionLinkDir = "/var/run/mudi-volte/links"
	provisionTokenLen       = 24 // hex characters: 96 random bits
	// provisionLinkTTL: how long an unused link works.
	provisionLinkTTL = 15 * time.Minute
	// maxProvisionLinkTTL bounds a longer life asked for the router's display.
	maxProvisionLinkTTL = 24 * time.Hour
	// maxProvisionLinks: live links at once. Every "Connect / QR code" makes
	// one; beyond that number a new link ends the one that expires first.
	maxProvisionLinks = 16
)

// provisionLink is the content of a link file: the phone, a check of the
// password the link was made with and the end of its life (Unix seconds by
// the router's clock).
type provisionLink struct {
	User    string `json:"user"`
	Check   string `json:"check"`
	Expires int64  `json:"expires"`
}

func (c SIPConfig) linkDir() string {
	if c.ProvisionLinkDir != "" {
		return c.ProvisionLinkDir
	}
	return defaultProvisionLinkDir
}

// passwordCheck ties a link to the phone's password: once the password
// changes, or a phone of the same name is added anew, older links refuse.
func passwordCheck(user, pass string) string {
	h := sha256.Sum256([]byte("mudi-volte link\x00" + user + "\x00" + pass))
	return hex.EncodeToString(h[:16])
}

// linkFile is the file of a link, named by the SHA-256 of the token, so that
// the directory does not hold the links themselves.
func linkFile(dir, token string) string {
	h := sha256.Sum256([]byte(token))
	return filepath.Join(dir, hex.EncodeToString(h[:]))
}

// isLinkFileName: the name of a link file, 64 lowercase hex digits.
func isLinkFileName(name string) bool { return isLowerHex(name, 2*sha256.Size) }

// isLowerHex reports a string of n lowercase hex digits.
func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func readProvisionLink(path string) (provisionLink, error) {
	var l provisionLink
	b, err := os.ReadFile(path)
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return l, err
	}
	if l.User == "" || l.Check == "" {
		return l, errors.New("setup link: incomplete file")
	}
	return l, nil
}

// newProvisionLink makes a link for acc that works once until now+ttl and
// returns its token. Expired and unreadable links go first, then, beyond
// maxProvisionLinks, the ones that expire first.
func newProvisionLink(dir string, acc SIPAccount, ttl time.Duration, now time.Time) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	pruneProvisionLinks(dir, now, maxProvisionLinks-1)
	b := make([]byte, provisionTokenLen/2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	data, err := json.Marshal(provisionLink{User: acc.User, Check: passwordCheck(acc.User, acc.Password), Expires: now.Add(ttl).Unix()})
	if err != nil {
		return "", err
	}
	// A whole file or none under the link's name: written aside (0600), then renamed.
	f, err := os.CreateTemp(dir, ".new-")
	if err != nil {
		return "", err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(f.Name(), linkFile(dir, token))
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return "", werr
	}
	return token, nil
}

// pruneProvisionLinks removes expired and unreadable links, then, beyond
// keep, the ones that expire first. Leftovers of an interrupted write go
// once they are a minute old.
func pruneProvisionLinks(dir string, now time.Time, keep int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type live struct {
		path    string
		expires int64
	}
	var links []live
	for _, e := range ents {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			continue
		}
		if !isLinkFileName(e.Name()) {
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > time.Minute {
				_ = os.Remove(path)
			}
			continue
		}
		l, err := readProvisionLink(path)
		if err != nil || now.Unix() >= l.Expires {
			_ = os.Remove(path)
			continue
		}
		links = append(links, live{path, l.Expires})
	}
	if len(links) <= keep {
		return
	}
	slices.SortFunc(links, func(a, b live) int { return cmp.Compare(a.expires, b.expires) })
	for _, l := range links[:len(links)-keep] {
		_ = os.Remove(l.path)
	}
}

// takeProvisionLink finds the phone of a link. With use, the link is used up
// together with the phone's other links. A refused link gets why, for the
// log, and its file goes when it can never work again.
func takeProvisionLink(dir, token string, accounts []SIPAccount, now time.Time, use bool) (acc SIPAccount, why string) {
	if !isLowerHex(token, provisionTokenLen) {
		return SIPAccount{}, "not a link"
	}
	path := linkFile(dir, token)
	l, err := readProvisionLink(path)
	if err != nil {
		return SIPAccount{}, "unknown or used link"
	}
	found := false
	for _, a := range accounts {
		if a.User == l.User {
			acc, found = a, true
			break
		}
	}
	switch {
	case now.Unix() >= l.Expires:
		why = "expired link"
	case !found:
		why = "the phone was removed"
	case subtle.ConstantTimeCompare([]byte(passwordCheck(acc.User, acc.Password)), []byte(l.Check)) != 1:
		why = "the phone got a new password"
	}
	if why != "" {
		_ = os.Remove(path)
		return SIPAccount{}, why
	}
	if !use {
		return acc, ""
	}
	// Of two fetches at once, the one that removes the file gets the settings.
	if err := os.Remove(path); err != nil {
		return SIPAccount{}, "unknown or used link"
	}
	dropProvisionLinks(dir, acc.User)
	return acc, ""
}

// dropProvisionLinks ends every link of phone user.
func dropProvisionLinks(dir, user string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !isLinkFileName(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if l, err := readProvisionLink(path); err == nil && l.User == user {
			_ = os.Remove(path)
		}
	}
}

func provisionURL(ip net.IP, port int, token string) string {
	return "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(port)) + "/p/" + token
}

// phoneProvisionURL makes a new setup link for phone user from the daemon
// config (for the CLI). It works once, for 15 minutes or ttl when longer
// (at most a day).
func phoneProvisionURL(cfg *Config, user string, ttl time.Duration, now time.Time) (string, error) {
	if cfg.SIP == nil {
		return "", errors.New("the SIP server for phones is not configured")
	}
	sc := cfg.SIP.withDefaults()
	if sc.ProvisionPort <= 0 {
		return "", errors.New("phone setup by QR code is turned off (provision_port 0)")
	}
	ip, _, err := sc.listenAddr()
	if err != nil {
		return "", err
	}
	ttl = min(max(ttl, provisionLinkTTL), maxProvisionLinkTTL)
	for _, a := range sc.Accounts {
		if a.User != user {
			continue
		}
		token, err := newProvisionLink(sc.linkDir(), a, ttl, now)
		if err != nil {
			return "", fmt.Errorf("setup link: %w", err)
		}
		return provisionURL(ip, sc.ProvisionPort, token), nil
	}
	return "", fmt.Errorf("no phone named %q", user)
}

// provisioner is the HTTP server for the links from QR codes.
type provisioner struct {
	cfg     SIPConfig
	dir     string // link files (newProvisionLink)
	ip      net.IP
	sipPort int
	tlsPort int        // 0: the phones get UDP without encryption
	lan     *net.IPNet // nil: subnet not found, everyone is let in (the socket is on the LAN address anyway)
	name    func() string
	log     *slog.Logger
	srv     *http.Server
	addr    net.Addr
	wrong   failLimiter // wrong links per address: guessing and noise in the log
}

// startProvisioning starts the link server. sipPort is the UDP port of the
// phones' SIP server and tlsPort its TLS port (0 = no TLS); name gives the
// account label on the phone (the SIM number, when known).
func startProvisioning(cfg SIPConfig, sipPort, tlsPort int, name func() string, log *slog.Logger) (*provisioner, error) {
	ip, _, err := cfg.listenAddr()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort(ip.String(), strconv.Itoa(cfg.ProvisionPort)))
	if err != nil {
		return nil, err
	}
	p := &provisioner{cfg: cfg, dir: cfg.linkDir(), ip: ip, sipPort: sipPort, tlsPort: tlsPort, lan: lanSubnet(ip), name: name, log: log, addr: ln.Addr()}
	p.srv = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          nil,
	}
	go func() { _ = p.srv.Serve(ln) }()
	if log != nil {
		log.Info("phone setup by QR code: ready", "listen", ln.Addr().String())
	}
	return p, nil
}

func (p *provisioner) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.srv.Shutdown(ctx)
}

func (p *provisioner) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if from := net.ParseIP(host); p.lan != nil && (from == nil || !p.lan.Contains(from)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if p.wrong.blocked(host) {
		http.Error(w, "too many wrong links, try again later", http.StatusTooManyRequests)
		return
	}
	tok, ok := strings.CutPrefix(r.URL.Path, "/p/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	// GET uses the link up; HEAD only checks it.
	acc, why := takeProvisionLink(p.dir, strings.TrimSuffix(tok, ".xml"), p.cfg.Accounts, time.Now(), r.Method == http.MethodGet)
	if why != "" {
		blocked := p.wrong.fail(host)
		if p.log != nil {
			if blocked {
				p.log.Warn("phone setup: too many wrong links, address blocked", "from", host, "for", authBlockFor.String())
			} else {
				p.log.Warn("phone setup: link refused, show the QR code again", "from", host, "reason", why)
			}
		}
		http.NotFound(w, r)
		return
	}
	display := "Mudi"
	if p.name != nil {
		if n := p.name(); n != "" {
			display = n
		}
	}
	body := linphoneXML(acc, p.cfg.Realm, p.ip, p.sipPort, p.tlsPort, display)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method != http.MethodGet {
		return
	}
	_, _ = w.Write(body)
	if p.log != nil {
		p.log.Info("phone setup: settings sent to the phone, the link is used up", "user", acc.User, "from", host, "encrypted", p.tlsPort != 0)
	}
}

// lanSubnet returns the subnet of the interface that holds address ip.
func lanSubnet(ip net.IP) *net.IPNet {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifs {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
				return &net.IPNet{IP: n.IP.Mask(n.Mask), Mask: n.Mask}
			}
		}
	}
	return nil
}

// entry is one name and value of a section of the provisioning XML.
type entry struct{ name, value string }

// linphoneCodecSlots is how many audio_codec_N sections we write.
// liblinphone (coreapi/linphonecore.c, codecs_config_read) reads
// audio_codec_0, 1, … up to the first empty one and for each sets the
// enabled flag on the codec from its own list; a repeat of the same codec
// does not duplicate it in the list but sets the flag again, so the last one
// wins. Codecs missing from the config are appended at the end with their
// default flags (G.722 is off there). Hence G.722 first, then PCMU and PCMA,
// and the remaining slots up to 40 are G.722 again: that way the phone's old
// entries (where G.722 may have been off) cannot override enabling it, and
// Linphone brings back the omitted codecs (Opus and the like) by itself.
const linphoneCodecSlots = 40

// linphoneXML returns the account in Linphone's remote provisioning format
// (lpconfig.xsd): the phone's first account (proxy_0/auth_info_0) points to
// the router's SIP server, over TLS with SRTP when tlsPort is set, otherwise
// over UDP without encryption. transient_provisioning=1 means apply once.
// Also: G.722 is enabled and first (wideband audio; Linphone has it off by
// default, and a call would go through 8 kHz PCMU); keep_service_alive: on
// Android, Linphone keeps its service and registration running, so the phone
// rings at once and not after waking up; publish_presence=0: our server does
// not handle presence.
//
// Encryption lives in Linphone's [sip] section and so applies to all its
// accounts (liblinphone has no per-account setting for it): SRTP mandatory,
// so that a call never goes out without it, and no check of the server
// certificate, which is self-signed (lan_tls.go). Over UDP the same entries
// are set back to Linphone's defaults, or a phone moved off TLS would refuse
// every call.
func linphoneXML(acc SIPAccount, realm string, ip net.IP, sipPort, tlsPort int, display string) []byte {
	host := ip.String()
	proxy := host
	if sipPort != 0 && sipPort != 5060 {
		proxy = net.JoinHostPort(host, strconv.Itoa(sipPort))
	}
	regProxy := "<sip:" + proxy + ";transport=udp>"
	sipEntries := []entry{{"default_proxy", "0"},
		{"media_encryption", "none"}, {"media_encryption_mandatory", "0"},
		{"verify_server_certs", "1"}, {"verify_server_cn", "1"}}
	if tlsPort != 0 {
		regProxy = "<sip:" + net.JoinHostPort(host, strconv.Itoa(tlsPort)) + ";transport=tls>"
		sipEntries = []entry{{"default_proxy", "0"},
			{"media_encryption", "srtp"}, {"media_encryption_mandatory", "1"},
			{"verify_server_certs", "0"}, {"verify_server_cn", "0"}}
	}
	sections := []struct {
		name      string
		overwrite bool
		entries   []entry
	}{
		{"misc", false, []entry{{"transient_provisioning", "1"}}},
		{"app", false, []entry{{"keep_service_alive", "1"}, {"publish_presence", "0"}}},
		{"sip", false, sipEntries},
		{"auth_info_0", true, []entry{
			{"username", acc.User},
			{"ha1", md5hex(acc.User + ":" + realm + ":" + acc.Password)},
			{"realm", realm},
			{"domain", host},
			{"algorithm", "MD5"},
		}},
		{"proxy_0", true, []entry{
			{"reg_proxy", regProxy},
			{"reg_identity", fmt.Sprintf("\"%s\" <sip:%s@%s>", sipDisplayName(display), acc.User, host)},
			{"realm", realm},
			{"reg_expires", "3600"},
			{"reg_sendregister", "1"},
			{"publish", "0"},
		}},
	}
	codecs := []entry{{"G722", "8000"}, {"PCMU", "8000"}, {"PCMA", "8000"}}
	for i := 0; i < linphoneCodecSlots; i++ {
		c := codecs[0]
		if i < len(codecs) {
			c = codecs[i]
		}
		sections = append(sections, struct {
			name      string
			overwrite bool
			entries   []entry
		}{fmt.Sprintf("audio_codec_%d", i), true, []entry{
			{"mime", c.name}, {"rate", c.value}, {"channels", "1"}, {"enabled", "1"},
		}})
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<config xmlns="http://www.linphone.org/xsds/lpconfig.xsd" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:schemaLocation="http://www.linphone.org/xsds/lpconfig.xsd lpconfig.xsd">` + "\n")
	for _, s := range sections {
		if s.overwrite {
			fmt.Fprintf(&b, "  <section name=%q overwrite=\"true\">\n", s.name)
		} else {
			fmt.Fprintf(&b, "  <section name=%q>\n", s.name)
		}
		for _, e := range s.entries {
			fmt.Fprintf(&b, "    <entry name=%q overwrite=\"true\">", e.name)
			_ = xml.EscapeText(&b, []byte(e.value))
			b.WriteString("</entry>\n")
		}
		b.WriteString("  </section>\n")
	}
	b.WriteString("</config>\n")
	return b.Bytes()
}

// sipDisplayName returns the account label for a SIP quoted string: safe
// characters only.
func sipDisplayName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("+ ._-()", r):
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "Mudi"
	}
	return b.String()
}
