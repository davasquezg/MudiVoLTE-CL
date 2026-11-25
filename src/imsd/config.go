package main

// JSON config of the daemon. The defaults suit any operator: the interface,
// the P-CSCF and the IMS PDN profile are discovered on the device (§88:
// CGCONTRDP, CGDCONT); a field in the file overrides the default. The
// mudi-volte package builds this file from UCI (/etc/config/mudi-volte) when
// the service starts.

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"

	"github.com/xarvel/MudiVoLTE/volte/reg"
)

type Config struct {
	Mode        string     `json:"mode"`         // check | register-once | daemon | service | sip-test
	Interface   string     `json:"interface"`    // rmnet_data1 | auto
	Allow       []string   `json:"allow"`        // allow-list for incoming calls (E.164); empty = reject everyone
	IncomingAll bool       `json:"incoming_all"` // S7: accept incoming calls from everyone (no allow-list needed)
	SIP         *SIPConfig `json:"sip"`          // S7: SIP bridge for the phones in the LAN; nil = off
	// MaxCallSec, RecordDir and PlayFile are for development without phones
	// (modes daemon and register-once); the package never writes them, and
	// RecordDir and PlayFile do nothing while the sip block is present, as in
	// the service (see package/README.md, "The daemon's own configuration").
	MaxCallSec  int        `json:"max_call_sec"` // >0: hang up every call after N seconds
	RecordDir   string     `json:"record_dir"`   // S4: directory for WAV recordings of the calls answered without the bridge; empty = none
	PlayFile    string     `json:"play_file"`    // S4: WAV (8 or 16 kHz mono PCM16) played into such a call; empty = silence
	SMSInbox    string     `json:"sms_inbox"`    // incoming SMS over IMS (JSON lines, 0600); empty = /root/imsd/sms-inbox.jsonl
	SMS         *SMSConfig `json:"sms"`          // SMS to and from the phones (needs sip); nil = off
	MissedCalls string     `json:"missed_calls"` // incoming calls that no phone answered (JSON lines, 0600); empty = not recorded
	LocalIPv4   string     `json:"local_ipv4"`   // "" | auto | address
	PCSCF       []string   `json:"pcscf"`        // in the order of attempts; empty = auto (CGCONTRDP, §88)
	PCSCFPort   int        `json:"pcscf_port"`   // unprotected P-CSCF port, 5060
	IMEI        string     `json:"imei"`         // "" or auto = from the modem (CGSN); else 15 digits
	Domain      string     `json:"domain"`       // "" = from the ISIM or derived from the IMSI (TS 23.003 §13)
	SIMSlot     int        `json:"sim_slot"`     // the SIM of the line: 1, or 2 (slot 2 or the built-in eSIM); 0 = 1
	SMSC        string     `json:"smsc"`         // "" = from the SIM (EF_SMSP) or from the first incoming message; else +digits
	Codecs      []string   `json:"codecs"`       // network codecs in order (AMR-WB, PCMA, PCMU); empty = whatever is linked in
	// IPsecOffers is the Security-Client, best first, as "alg/ealg" pairs
	// (hmac-md5-96 or hmac-sha-1-96 / null, aes-cbc or des-ede3-cbc); empty =
	// reg.DefaultOffers.
	IPsecOffers []string `json:"ipsec_offers"`
	// LooseInboundSrcPort: do not pin the P-CSCF source port in the inbound
	// IPsec policies (the misbehaving 192.0.2.20, §81–82). On by default.
	LooseInboundSrcPort *bool  `json:"loose_inbound_src_port"`
	LogLevel            string `json:"log_level"`
	LogFile             string `json:"log_file"`        // log to a file as well (rotated to .1); empty = stderr only
	LogFileMaxKB        int    `json:"log_file_max_kb"` // log file limit, KB; 0 = 1024
	// IPsecTool installs the security associations: "" or "netlink" through
	// the kernel's XFRM socket (the keys never pass through a command line),
	// "ip" through iproute2 as before, a fallback should netlink misbehave.
	IPsecTool string `json:"ipsec_tool"`

	// Service mode (S5): the daemon brings up the IMS PDN itself and watches it.
	IMSProfile int    `json:"ims_profile"` // IMS APN profile (cid); 0 = find ims_apn in CGDCONT, else 2
	IMSAPN     string `json:"ims_apn"`     // the APN of the IMS profile looked for; empty = "ims"
	DSIHelper  string `json:"dsi_helper"`  // the program that places the data call; empty = /usr/sbin/mudi-dsi-helper
	StateFile  string `json:"state_file"`  // state for mudi-volte status; empty = /var/run/mudi-volte/state.json
	PhonesFile string `json:"phones_file"` // phone registrations across restarts; empty = /var/run/mudi-volte/phones.json
	PushFile   string `json:"push_file"`   // phones' push parameters (lan_push.go); empty = /etc/mudi-volte/push.json
}

func defaultConfig() *Config {
	return &Config{
		Mode:      "check",
		Interface: "auto",
		PCSCFPort: 5060,
	}
}

func (c *Config) stateFile() string {
	if c.StateFile != "" {
		return c.StateFile
	}
	return "/var/run/mudi-volte/state.json"
}

func (c *Config) smsInbox() string {
	if c.SMSInbox != "" {
		return c.SMSInbox
	}
	return "/root/imsd/sms-inbox.jsonl"
}

func (c *Config) phonesFile() string {
	if c.PhonesFile != "" {
		return c.PhonesFile
	}
	return "/var/run/mudi-volte/phones.json"
}

func (c *Config) pushFile() string {
	if c.PushFile != "" {
		return c.PushFile
	}
	return "/etc/mudi-volte/push.json"
}

func (c *Config) dsiHelper() string {
	if c.DSIHelper != "" {
		return c.DSIHelper
	}
	return "/usr/sbin/" + helperProgram
}

// checkIMSProfile checks ims_profile: 0 to find it, else a profile number the
// DSI library can take (it keeps one byte of it).
func (c *Config) checkIMSProfile() error {
	if p := c.IMSProfile; p < 0 || p > 255 {
		return fmt.Errorf("ims_profile %d is not a modem profile number (1 to 255, or 0 to find it)", p)
	}
	return nil
}

// imsAPN is the APN whose profile carries IMS (ims_apn, else "ims").
func (c *Config) imsAPN() string {
	if c.IMSAPN != "" {
		return c.IMSAPN
	}
	return "ims"
}

// ipsecOffers parses ipsec_offers; nil when it is empty (the library's
// defaults).
func (c *Config) ipsecOffers() ([]reg.Offer, error) {
	var out []reg.Offer
	for _, s := range c.IPsecOffers {
		o, err := reg.ParseOffer(s)
		if err != nil {
			return nil, fmt.Errorf("ipsec_offers: %w", err)
		}
		out = append(out, o)
	}
	return out, nil
}

// check rejects the operator settings the daemon could not use, with the
// field named, before anything is sent to the modem or the network: the
// modes that use them run it first (the service with the error in its state
// file, for mudi-volte status), the tools do not need it.
func (c *Config) check() error {
	if err := c.checkIMSProfile(); err != nil {
		return err
	}
	for _, s := range c.PCSCF {
		ip := net.ParseIP(s)
		if ip == nil {
			return fmt.Errorf("pcscf %q is not an IP address", s)
		}
		if ip.To4() == nil {
			return fmt.Errorf("pcscf %q: only IPv4 P-CSCFs are used", s)
		}
	}
	if p := c.PCSCFPort; p < 0 || p > 65535 {
		return fmt.Errorf("pcscf_port %d is not a port (1 to 65535, or 0 for 5060)", p)
	}
	if c.IMEI != "" && c.IMEI != "auto" && (len(c.IMEI) != 15 || !allDigits(c.IMEI)) {
		return fmt.Errorf("imei %q: 15 digits, or empty to read it from the modem", c.IMEI)
	}
	if c.Domain != "" && !dnsName(c.Domain, 253) {
		return fmt.Errorf("domain %q is not a domain name (letters, digits, hyphens and dots)", c.Domain)
	}
	if s := strings.TrimPrefix(c.SMSC, "+"); c.SMSC != "" && (len(s) > 15 || !allDigits(s)) {
		return fmt.Errorf("smsc %q: an international number (+ and up to 15 digits)", c.SMSC)
	}
	have := availableCodecs()
	for _, name := range c.Codecs {
		if !slices.ContainsFunc(have, func(h string) bool { return strings.EqualFold(h, name) }) {
			return fmt.Errorf("codecs: %q is not one of %s", name, strings.Join(have, ", "))
		}
	}
	// APN network identifier: labels of letters, digits and hyphens, 100
	// octets at most (TS 23.003 §9.1).
	if c.IMSAPN != "" && !dnsName(c.IMSAPN, 100) {
		return fmt.Errorf("ims_apn %q is not an APN name (letters, digits, hyphens and dots)", c.IMSAPN)
	}
	_, err := c.ipsecOffers()
	return err
}

// dnsName: dot-separated labels of letters, digits and hyphens, none empty,
// longer than 63 or starting or ending with a hyphen; limit characters in all.
func dnsName(s string, limit int) bool {
	if s == "" || len(s) > limit {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func loadConfig(path string) (*Config, error) {
	cfg := defaultConfig()
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	// Every AT command, the IMS data call and the SMS spool follow the
	// line's subscription from here on.
	if err := setIMSSlot(cfg.SIMSlot); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) looseSrcPort() bool { return c.LooseInboundSrcPort == nil || *c.LooseInboundSrcPort }

// pcscfIPs returns the P-CSCF list from the config (no "auto" logic: empty → nil).
func (c *Config) pcscfIPs() []net.IP {
	var ips []net.IP
	for _, s := range c.PCSCF {
		if ip := net.ParseIP(s); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips
}
