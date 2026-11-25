// Command mudi-imsd is a host-side IMS client (external VoLTE stack) for the
// GL.iNet Mudi 7 (RG650V). The network side is the volte library
// (registration, IMS-AKA, sec-agree with IPsec in the kernel, calls, RTP,
// SMS); the SIM is reached through AT+CSIM (the GL ubus proxy, with the §69
// trap taken into account); the IMS PDN is brought up as a data call from
// the host (§71). MudiVoLTE FINDINGS §68–§85.
//
// Modes:
//
//	check         — offline validation;
//	register-once — REGISTER → 200 → de-REGISTER at once;
//	daemon        — persistent registration + incoming calls (allow-list)
//	                and SMS; with a "sip" block, a bridge to the phones in
//	                the LAN (S7); someone else brings up the IMS PDN (post.sh);
//	service       — S5, 24/7 operation under procd: brings up the IMS PDN
//	                itself, watches it and the registration, recovers
//	                (service.go);
//	sip-test      — S7: only the SIP server for the phones + echo test, no
//	                network.
//
// Gates: register-once does not hold the registration (by construction);
// daemon requires an explicit --hold-registration in a dev session; service
// is started only by the package's init script after `mudi-volte setup`.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/damonto/wwan-go/sim"

	"github.com/xarvel/MudiVoLTE/volte/call"
	"github.com/xarvel/MudiVoLTE/volte/ipsec"
	"github.com/xarvel/MudiVoLTE/volte/reg"
	volsim "github.com/xarvel/MudiVoLTE/volte/sim"
	"github.com/xarvel/MudiVoLTE/volte/sms"
)

const userAgent = "mudi-imsd/volte"

func main() {
	configPath := flag.String("config", "", "path to the JSON config (empty = defaults)")
	mode := flag.String("mode", "", "check | register-once | daemon | service | sip-test (overrides the config)")
	hold := flag.Bool("hold-registration", false, "daemon: hold the registration in the live network (a gate, for dev sessions)")
	flag.Parse()

	if flag.NArg() > 0 {
		os.Exit(runTool(*configPath, flag.Args()))
	}
	if err := run(*configPath, *mode, *hold); err != nil {
		os.Exit(1)
	}
}

func run(configPath, mode string, hold bool) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("mudi-imsd", "err", err)
		return err
	}
	if mode != "" {
		cfg.Mode = mode
	}
	logger, logClose := newLogger(cfg)
	defer logClose.Close()
	if err := runMode(cfg, logger, hold); err != nil {
		logger.Error("mudi-imsd", "err", err)
		return err
	}
	return nil
}

func runMode(cfg *Config, logger *slog.Logger, hold bool) error {
	switch cfg.Mode {
	case "check":
		return runCheck(cfg, logger)
	case "register-once":
		return runRegisterOnce(cfg, logger)
	case "daemon":
		return runDaemon(cfg, logger, hold)
	case "service":
		return runService(cfg, logger)
	case "sip-test":
		return runSIPTest(cfg, logger)
	default:
		return fmt.Errorf("unknown mode %q (check | register-once | daemon | service | sip-test)", cfg.Mode)
	}
}

// runCheck is the offline validation: config, P-CSCF, building the volte
// stack on stubs. Not a single AT command and not a single packet to the
// network.
func runCheck(cfg *Config, logger *slog.Logger) error {
	if t := cfg.IPsecTool; t != "" && t != "netlink" && t != "ip" {
		return fmt.Errorf("check: ipsec_tool %q is neither netlink nor ip", t)
	}
	if err := cfg.check(); err != nil {
		return fmt.Errorf("check: %w", err)
	}
	pcscf := cfg.pcscfIPs()
	if len(pcscf) == 0 {
		logger.Warn("check: no P-CSCF set; finding them through CGCONTRDP is tested on the device")
		pcscf = []net.IP{net.IPv4(10, 0, 0, 1)}
	}
	local := netip.MustParseAddr("10.0.0.2")
	if cfg.LocalIPv4 != "" && cfg.LocalIPv4 != "auto" {
		a, err := netip.ParseAddr(cfg.LocalIPv4)
		if err != nil {
			return fmt.Errorf("check: local_ipv4 %q is not an address", cfg.LocalIPv4)
		}
		local = a
	}
	ids, err := deriveIdentities("001010000000001", "001", "01", cfg.Domain)
	if err != nil {
		return fmt.Errorf("check: identities: %w", err)
	}
	rc, err := regConfig(cfg, ids, "000000000000000", local, pcscf, "3GPP-E-UTRAN-FDD", logger)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}
	st, err := newNetStack("", rc, nil, callConfig(cfg, "", logger), smsConfig(cfg, ids, "", logger), logger)
	if err != nil {
		return fmt.Errorf("check: stack: %w", err)
	}
	_ = st
	if cfg.SIP != nil {
		sc := cfg.SIP.withDefaults()
		if err := sc.validate(); err != nil {
			return fmt.Errorf("check: sip: %w", err)
		}
		logger.Info("check: sip OK", "listen", sc.Listen, "accounts", len(sc.Accounts),
			"codecs", strings.Join(sc.Codecs, ","), "dial_allow", strings.Join(sc.DialAllow, ","),
			"incoming_all", cfg.IncomingAll)
	}
	logger.Info("check: OK", "iface", cfg.Interface, "pcscf", len(cfg.PCSCF),
		"network_codecs", strings.Join(callConfig(cfg, "", logger).Codecs, ","),
		"loose_src_port", cfg.looseSrcPort())
	return nil
}

func parseLogLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return 0, false
}

// identities is who we are in IMS (TS 23.003 §13).
type identities struct {
	IMPI, IMPU, Domain string
}

// deriveIdentities derives the identities without an ISIM:
// IMPI = IMSI@domain, IMPU = sip:IMSI@domain,
// domain = ims.mncXXX.mccYYY.3gppnetwork.org (MNC padded to three digits,
// TS 23.003 §13.2).
func deriveIdentities(imsi, mcc, mnc, domain string) (identities, error) {
	if len(imsi) < 6 || !allDigits(imsi) || len(mcc) != 3 || (len(mnc) != 2 && len(mnc) != 3) {
		return identities{}, fmt.Errorf("IMSI/MCC/MNC are not usable (mcc %q, mnc %q)", mcc, mnc)
	}
	if domain == "" {
		if len(mnc) == 2 {
			mnc = "0" + mnc
		}
		domain = "ims.mnc" + mnc + ".mcc" + mcc + ".3gppnetwork.org"
	}
	return identities{IMPI: imsi + "@" + domain, IMPU: "sip:" + imsi + "@" + domain, Domain: domain}, nil
}

func regConfig(cfg *Config, ids identities, imei string, ue netip.Addr, pcscf []net.IP, ani string, logger *slog.Logger) (reg.Config, error) {
	port := uint16(cfg.PCSCFPort)
	if port == 0 {
		port = 5060
	}
	var addrs []netip.AddrPort
	for _, ip := range pcscf {
		if a, ok := netip.AddrFromSlice(ip.To4()); ok {
			addrs = append(addrs, netip.AddrPortFrom(a, port))
		}
	}
	rc := reg.Config{IMPI: ids.IMPI, IMPU: ids.IMPU, Domain: ids.Domain, IMEI: imei, UEAddr: ue,
		UserAgent: userAgent, PANI: ani, LooseInboundSrcPort: cfg.looseSrcPort(), Log: logger}
	if len(addrs) > 0 {
		rc.PCSCF, rc.PCSCFFallback = addrs[0], addrs[1:]
	}
	if cfg.IPsecTool == "ip" {
		rc.IPsec = ipsec.ExecRunner{} // netlink otherwise (the library's default)
	}
	// Config.check, which every mode that registers runs first, rejects an
	// invalid list already; one that gets here all the same is an error
	// rather than the default offer in its place.
	var err error
	if rc.Offers, err = cfg.ipsecOffers(); err != nil {
		return reg.Config{}, err
	}
	return rc, nil
}

func callConfig(cfg *Config, ani string, logger *slog.Logger) call.Config {
	codecs := cfg.Codecs
	if len(codecs) == 0 {
		codecs = availableCodecs()
	}
	return call.Config{PANI: ani, UserAgent: userAgent, Codecs: codecs, Log: logger}
}

func smsConfig(cfg *Config, ids identities, smsc string, logger *slog.Logger) sms.Config {
	if cfg.SMSC != "" {
		smsc = cfg.SMSC
	}
	return sms.Config{Domain: ids.Domain, SMSC: smsc, UserAgent: userAgent, Log: logger}
}

// cardAuth runs AKA on the USIM/ISIM through wwan-go.
type cardAuth struct{ card *sim.Card }

func (a cardAuth) Authenticate(ctx context.Context, rnd, autn [16]byte) (volsim.AuthResult, error) {
	r, err := a.card.AKA(ctx, rnd[:], autn[:])
	if err != nil {
		return volsim.AuthResult{}, err
	}
	switch {
	case r.SynchronizationFailed():
		return volsim.AuthResult{AUTS: r.AUTS}, nil
	case r.AuthenticationRejected():
		return volsim.AuthResult{}, volsim.ErrMAC
	case !r.Successful():
		return volsim.AuthResult{}, errors.New("the SIM returned no RES/CK/IK")
	}
	return volsim.AuthResult{RES: r.RES, CK: r.CK, IK: r.IK}, nil
}

// parseIMEI returns the 15-digit token of an AT+CGSN reply (the rest is
// OK/garbage).
func parseIMEI(out string) string {
	for _, tok := range strings.Fields(out) {
		if len(tok) == 15 && allDigits(tok) {
			return tok
		}
	}
	return ""
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

func newATLink() *ATLink {
	return &ATLink{Run: ubusAT(12 * time.Second), Retries: 4, Timeout: 12 * time.Second}
}

// imsProfile returns the cid of the IMS APN: from the config (ims_profile),
// else the profile with the APN ims_apn ("ims") in CGDCONT, else 2 (as on
// the tested Mudi, §71). The log tells which one and why.
func imsProfile(ctx context.Context, cfg *Config, link *ATLink, logger *slog.Logger) int {
	if cfg.IMSProfile > 0 {
		logger.Info("IMS profile", "cid", cfg.IMSProfile, "from", "ims_profile")
		return cfg.IMSProfile
	}
	apn := cfg.imsAPN()
	out, err := link.query(ctx, "AT+CGDCONT?")
	if err == nil {
		if cid := parseIMSProfile(out, apn); cid > 0 {
			logger.Info("IMS profile", "cid", cid, "apn", apn, "from", "CGDCONT")
			return cid
		}
		logger.Warn("IMS profile: no profile with the APN in CGDCONT, trying cid 2; set ims_apn or ims_profile",
			"apn", apn, "cid", 2)
	} else {
		logger.Warn("IMS profile: CGDCONT failed, trying cid 2", "apn", apn, "cid", 2, "err", err)
	}
	return 2
}

// pdnEnv is the live IMS PDN: the interface, our address and the P-CSCFs in
// the order of attempts.
type pdnEnv struct {
	iface string
	local net.IP
	pcscf []net.IP
}

// discoverPDN maps CGCONTRDP of the IMS profile to the address and the
// P-CSCFs, and the address to the interface. P-CSCFs from the config take
// precedence (the order for a misbehaving P-CSCF, §81).
func discoverPDN(ctx context.Context, cfg *Config, link *ATLink, profile int, logger *slog.Logger) (pdnEnv, error) {
	var info pdnInfo
	if out, err := link.query(ctx, fmt.Sprintf("AT+CGCONTRDP=%d", profile)); err == nil {
		info = parseCGCONTRDP(out)
	} else {
		logger.Warn("CGCONTRDP", "profile", profile, "err", err)
	}
	auto := func(v string) bool { return v == "" || v == "auto" }
	iface, localIP, err := findIMSIface(cfg.Interface, info.Local)
	if err != nil {
		// The daemon registers over IPv4 only (docs/PROTOCOLS.md §2.3,
		// "IPv6"). When no interface with IPv4 is found and CGCONTRDP shows
		// the PDN with a global IPv6 address and no IPv4 one, the PDN is
		// named as such, unless the config gives the interface or the
		// address itself. A PDN whose IPv4 address CGCONTRDP does not show
		// is still found on the interfaces first; link-local alone is no
		// IPv6 PDN.
		if info.Local == nil && info.Local6 != nil && info.Local6.IsGlobalUnicast() &&
			auto(cfg.Interface) && auto(cfg.LocalIPv4) {
			return pdnEnv{}, fmt.Errorf("the IMS PDN (APN %q) has only the IPv6 address %s in CGCONTRDP (%v): %w",
				info.APN, info.Local6, err, errIPv6Only)
		}
		return pdnEnv{}, fmt.Errorf("IMS PDN interface: %w", err)
	}
	if !auto(cfg.LocalIPv4) {
		if localIP = net.ParseIP(cfg.LocalIPv4).To4(); localIP == nil {
			return pdnEnv{}, fmt.Errorf("local_ipv4 %q is not an address", cfg.LocalIPv4)
		}
	}
	pcscf, from := cfg.pcscfIPs(), "the config"
	if len(pcscf) == 0 {
		from = "CGCONTRDP"
		for _, ip := range append(append([]net.IP(nil), info.PCSCF...), info.PCSCF6...) {
			if !ip.Equal(localIP) {
				pcscf = append(pcscf, ip)
			}
		}
	}
	// Our address is IPv4, and the SAs take the family of both ends
	// (volte/ipsec Set.Validate): an IPv6 P-CSCF is left out, with a line in
	// the log, and a list of IPv6 ones only is named as such.
	var pcscf4, pcscf6 []net.IP
	for _, ip := range pcscf {
		if ip.To4() != nil {
			pcscf4 = append(pcscf4, ip)
		} else {
			pcscf6 = append(pcscf6, ip)
		}
	}
	if len(pcscf6) > 0 {
		logger.Info("IMS-PDN: IPv6 P-CSCFs left out, the IMS address is IPv4", "from", from, "pcscf", fmtIPs(pcscf6))
	}
	if len(pcscf4) == 0 && len(pcscf6) > 0 {
		return pdnEnv{}, fmt.Errorf("the P-CSCFs from %s are IPv6 only (%s): %w", from, fmtIPs(pcscf6), errIPv6Only)
	}
	pcscf = pcscf4
	if len(pcscf) == 0 {
		return pdnEnv{}, errors.New("no P-CSCF found: neither CGCONTRDP nor the config has any")
	}
	logger.Info("IMS-PDN", "iface", iface, "ip", localIP.String(), "apn", info.APN, "pcscf", fmtIPs(pcscf))
	return pdnEnv{iface: iface, local: localIP, pcscf: pcscf}, nil
}

// buildLiveStack sets up the live stack on top of an IMS PDN that is already
// up (the daemon and register-once modes). It sends nothing to the network.
func buildLiveStack(cfg *Config, logger *slog.Logger) (*netStack, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	link := newATLink()
	ctx := context.Background()
	env, err := discoverPDN(ctx, cfg, link, imsProfile(ctx, cfg, link, logger), logger)
	if err != nil {
		return nil, err
	}
	return buildStack(ctx, cfg, link, env, logger)
}

// buildStack gathers the IMEI, P-Access-Network-Info and SIM (IMSI, SMSC,
// keys) and builds the volte stack on the PDN found. It sends nothing to the
// network.
func buildStack(ctx context.Context, cfg *Config, link *ATLink, env pdnEnv, logger *slog.Logger) (*netStack, error) {
	iface, pcscf := env.iface, env.pcscf
	ue, _ := netip.AddrFromSlice(env.local.To4())

	imei := cfg.IMEI
	if imei == "" || imei == "auto" {
		out, err := link.query(ctx, "AT+CGSN")
		if err != nil {
			return nil, fmt.Errorf("CGSN: %w", err)
		}
		if imei = parseIMEI(out); imei == "" {
			return nil, errors.New("no IMEI in the reply to CGSN")
		}
		logger.Info("IMEI read (15 digits)")
	}

	// SIM: IMSI/MCC/MNC, ISIM identities, SMSC, AKA keys. The card's logger is
	// muted: it writes the identities at debug level.
	reader, err := sim.NewReader(newCSIMTransmitter(link))
	if err != nil {
		return nil, fmt.Errorf("SIM reader: %w", err)
	}
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	simCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	card, err := sim.New(simCtx, reader, quiet)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("SIM: %w", err)
	}
	var ids identities
	if card.PrivateIdentity() != "" && card.PublicIdentity() != "" && card.HomeDomain() != "" {
		ids = identities{IMPI: card.PrivateIdentity(), IMPU: card.PublicIdentity(), Domain: card.HomeDomain()}
		if cfg.Domain != "" {
			ids.Domain = cfg.Domain
		}
		logger.Info("identities from the ISIM")
	} else if ids, err = deriveIdentities(card.IMSI(), card.MCC(), card.MNC(), cfg.Domain); err != nil {
		return nil, err
	}
	logger.Info("IMS domain", "domain", ids.Domain, "smsc_on_sim", card.SMSC() != "", "rtp_iface", iface)

	// After the SIM: it gives the home MNC for a 1-digit cell MNC (#12). A
	// cell that cannot be read does not stop the line (#7): see resolveANI.
	ani, err := servingCellANI(ctx, link, card.MCC(), card.MNC())
	if ctx.Err() != nil {
		return nil, fmt.Errorf("serving cell: %w", ctx.Err())
	}
	ani = resolveANI(ani, err, logger)
	if ani != "" {
		// The cell gives away the router's location to within hundreds of
		// metres, and people attach the log to bug reports: at info level only
		// the network (PLMN).
		logger.Info("P-Access-Network-Info", "ani", maskANI(ani))
		logger.Debug("P-Access-Network-Info", "ani_full", ani)
	}

	rc, err := regConfig(cfg, ids, imei, ue, pcscf, ani, logger)
	if err != nil {
		return nil, err
	}
	return newNetStack(iface, rc, cardAuth{card},
		callConfig(cfg, ani, logger), smsConfig(cfg, ids, card.SMSC(), logger), logger)
}

// runRegisterOnce — REGISTER → AKA → IPsec → 200 → de-REGISTER at once. The
// registration is not held whatever the outcome.
func runRegisterOnce(cfg *Config, logger *slog.Logger) error {
	st, err := buildLiveStack(cfg, logger)
	if err != nil {
		return fmt.Errorf("register-once: %w", err)
	}
	logger.Info("register-once: REGISTER → AKA → IPsec → 200…")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	b, err := st.reg.Register(ctx)
	if err != nil {
		return fmt.Errorf("register-once: %w", err)
	}
	logger.Info("register-once: REGISTERED, de-registering at once",
		"expires", b.Expires, "service_routes", len(b.ServiceRoutes), "public_ids", len(b.AssociatedURIs))
	dctx, dcancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer dcancel()
	if err := st.reg.Deregister(dctx); err != nil {
		logger.Error("register-once: de-REGISTER", "err", err)
	}
	return nil
}

// runDaemon keeps a persistent registration and handles incoming calls and
// SMS. It ends on a signal: de-REGISTER and removal of the SAs.
func runDaemon(cfg *Config, logger *slog.Logger, holdRegistration bool) error {
	if !holdRegistration {
		return errors.New("daemon holds the registration in the live network: " +
			"start it with --hold-registration in a dev session")
	}
	st, err := buildLiveStack(cfg, logger)
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	st.sms.OnMessage = func(m sms.Message) error { return saveSMS(cfg, logger, m) }

	var mgr *callManager
	if cfg.SIP != nil {
		sc := cfg.SIP.withDefaults()
		mgr = newCallManager(cfg, sc, st, logger)
		lan, err := startLANSIP(sc, mgr, logger)
		if err != nil {
			return fmt.Errorf("daemon: SIP: %w", err)
		}
		defer lan.Close()
		defer mgr.shutdown()
		st.onIncoming = func(c *netCall, from string) { mgr.incomingFromNetwork(c, from) }
		st.onEvent = func(c *netCall, s CallState) { mgr.onIMSEvent(c, s) }
	} else {
		st.onIncoming = func(c *netCall, from string) { handleIncomingCall(c, from, cfg, logger) }
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		st.reg.Run(ctx, func(s reg.Status, err error) {
			if err != nil {
				logger.Warn("daemon: status", "status", s.String(), "err", err)
				return
			}
			logger.Info("daemon: status", "status", s.String())
		})
	}()
	logger.Info("daemon: running (an empty allow list declines everyone)",
		"allow", len(cfg.Allow), "incoming_all", cfg.IncomingAll, "sip", cfg.SIP != nil,
		"max_call_sec", cfg.MaxCallSec)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	s := <-sig
	logger.Info("daemon: stop signal, de-registering", "sig", s.String())
	if mgr != nil {
		mgr.shutdown()
	}
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		logger.Warn("daemon: de-REGISTER did not finish in 20 s, exiting")
	}
	return nil
}

// saveSMS writes an incoming SMS as a JSON line to the file (0600). Only the
// sender (masked) and the length go to the log: the text may be a code from
// a bank. A write error goes back to the network as RP-ERROR, so the SMS is
// not lost: the network retries.
func saveSMS(cfg *Config, logger *slog.Logger, m sms.Message) error {
	path := cfg.smsInbox()
	at := m.Time
	if at.IsZero() {
		at = time.Now()
	}
	rec := map[string]string{"received_at": at.Format(time.RFC3339), "from": m.From,
		"service_center": m.SMSC, "text": m.Text}
	line, _ := json.Marshal(rec)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = f.Write(append(line, '\n'))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		logger.Error("SMS: not saved, the network will send it again", "from", maskNumber(m.From), "len", len(m.Text), "err", err)
		return err
	}
	logger.Warn("SMS: received and saved", "from", maskNumber(m.From), "len", len(m.Text), "file", path)
	return nil
}

// runSIPTest is S7 without the network: the SIP server for the phones and
// the echo test. It does not touch the modem or the IMS registration;
// outgoing calls to the network get 503.
func runSIPTest(cfg *Config, logger *slog.Logger) error {
	if cfg.SIP == nil {
		return errors.New("sip-test: the config has no \"sip\" section")
	}
	sc := cfg.SIP.withDefaults()
	if sc.EchoExt == "" {
		sc.EchoExt = "600"
	}
	mgr := newCallManager(cfg, sc, nil, logger)
	lan, err := startLANSIP(sc, mgr, logger)
	if err != nil {
		return fmt.Errorf("sip-test: %w", err)
	}
	defer lan.Close()
	if sc.ProvisionPort > 0 {
		if prov, err := startProvisioning(sc, lan.port, lan.tlsPort, nil, logger); err != nil {
			logger.Warn("phone setup by QR code is off", "err", err)
		} else {
			defer prov.Close()
		}
	}
	logger.Info("sip-test: waiting for phones; for the echo test call " + sc.EchoExt)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	s := <-sig
	logger.Info("sip-test: stopping", "sig", s.String())
	mgr.shutdown()
	return nil
}

// handleIncomingCall handles a call without the bridge (S4): allow-list;
// allowed callers are answered and their audio is recorded/played, the rest
// get 603 (a 6xx, so the network does not try CSFB, §87). Numbers in the
// logs are masked.
func handleIncomingCall(c imsCall, fromNumber string, cfg *Config, logger *slog.Logger) {
	from := maskNumber(fromNumber)
	if !cfg.IncomingAll && !numberAllowed(cfg.Allow, fromNumber, cfg.plan()) {
		logger.Info("incoming: declined (caller not allowed)", "from", from)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.Reject(ctx, rejectDecline, "Decline"); err != nil {
			logger.Error("Reject", "err", err)
		}
		return
	}
	logger.Info("incoming: allowed, answering", "from", from)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.Answer(ctx); err != nil {
		logger.Error("Answer", "err", err)
		return
	}
	logger.Info("call answered", "from", from)
	recordAndPlay(c, cfg, logger, from)
	if cfg.MaxCallSec > 0 {
		time.AfterFunc(time.Duration(cfg.MaxCallSec)*time.Second, func() {
			hctx, hcancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer hcancel()
			if err := c.Hangup(hctx); err != nil {
				logger.Error("Hangup", "err", err)
			}
		})
	}
}

// numberAllowed does a normalised suffix match: "+995 555 123 456" and
// "995555123456" match "+995555123456". Both sides are first read with the
// number plan (numbers.go), so a caller ID in national form matches an entry
// in E.164 and back: in Chile "912345678" matches "+56912345678". Without a
// country (no plan to read the national form with), a caller ID of at least
// minReverseSuffix digits also matches the end of a longer entry; with one,
// only the normalised forms are compared. Short allow
// entries (<7 digits) need an exact match, otherwise "456" would match too
// much. An empty allow means reject everyone (the safe default), and so does
// a hidden caller (no digits).
func numberAllowed(allow []string, from string, p numberPlan) bool {
	const minReverseSuffix = 9
	f := e164Digits(from, p)
	if f == "" || len(allow) == 0 {
		return false
	}
	reverse := p.country == "" && len(f) >= minReverseSuffix
	for _, a := range allow {
		ad := e164Digits(a, p)
		if ad == "" {
			continue
		}
		if len(ad) >= 7 {
			if strings.HasSuffix(f, ad) || (reverse && strings.HasSuffix(ad, f)) {
				return true
			}
		} else if f == ad {
			return true
		}
	}
	return false
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// maskNumber masks a number for logs: only the last 4 digits are visible.
func maskNumber(s string) string {
	d := digitsOnly(s)
	if len(d) <= 4 {
		return d
	}
	return "…" + d[len(d)-4:]
}

// maskANI masks P-Access-Network-Info for logs: the access type and the PLMN
// are visible, the TAC and the cell ID (the last 11 hex digits of
// utran-cell-id-3gpp on LTE, the last 15 on NR) are hidden.
func maskANI(ani string) string {
	const key = "utran-cell-id-3gpp="
	i := strings.Index(ani, key)
	if i < 0 {
		return ani
	}
	hidden := 11 // TAC 4 + ECI 7
	if strings.HasPrefix(ani, "3GPP-NR-") {
		hidden = 15 // TAC 6 + NCI 9
	}
	id := ani[i+len(key):]
	if len(id) <= hidden {
		return ani[:i+len(key)] + "…"
	}
	return ani[:i+len(key)] + id[:len(id)-hidden] + "…"
}

func fmtIPs(ips []net.IP) string {
	parts := make([]string, len(ips))
	for i, ip := range ips {
		parts[i] = ip.String()
	}
	return strings.Join(parts, ", ")
}
