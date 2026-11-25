package main

// SMS between the SIM and the phones.
//
// In: every incoming SMS, from GL's smsd spool (the operator delivers SMS
// over SGs, to the modem) or over IMS (saveSMS), goes to every phone account
// as a SIP MESSAGE from the sender's number, so that Linphone shows a
// conversation with that number. A phone that is away gets it as soon as it
// registers again. The state file keeps who still waits for which SMS: a
// restart neither loses messages nor sends them twice. Without a state file
// (the first start) the SMS already on the router count as delivered, and
// only new ones go to the phones.
//
// Out: a message a phone sends to a number becomes a file in smsd's outgoing
// spool, written the way GL's sendsms writes it; smsd sends it through the
// modem and moves the file to sent/ or failed/, which decides the answer to
// the phone.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/xarvel/MudiVoLTE/volte/sms"
)

// SMSConfig is the "sms" block of the daemon config; without it SMS stay
// out of the phones.
type SMSConfig struct {
	Incoming  string `json:"incoming"` // smsd spool directories, as in /tmp/smstools.cfg
	Outgoing  string `json:"outgoing"`
	Sent      string `json:"sent"`
	Failed    string `json:"failed"`
	Bus       string `json:"bus"`        // the modem smsd sends through, as for GL's sendsms: "cpu"
	Slot      int    `json:"slot"`       // the SIM slot smsd sends from; 0 = the line's (imsSlot)
	StateFile string `json:"state_file"` // who still waits for which SMS
}

func (c SMSConfig) withDefaults() SMSConfig {
	if c.Incoming == "" {
		c.Incoming = "/etc/spool/sms/incoming"
	}
	if c.Outgoing == "" {
		c.Outgoing = "/etc/spool/sms/outgoing"
	}
	if c.Sent == "" {
		c.Sent = "/etc/spool/sms/sent"
	}
	if c.Failed == "" {
		c.Failed = "/etc/spool/sms/failed"
	}
	if c.Bus == "" {
		c.Bus = "cpu"
	}
	if c.Slot == 0 {
		c.Slot = imsSlot
	}
	if c.StateFile == "" {
		c.StateFile = "/etc/mudi-volte/sms-phones.json"
	}
	return c
}

const (
	smsPoll        = 5 * time.Second    // how often the spool is read
	smsKeepPending = 7 * 24 * time.Hour // a phone away for longer misses the SMS
	smsSendWait    = 20 * time.Second   // how long a phone's message waits for smsd's verdict
	smsWatchLate   = 10 * time.Minute   // and then how long the outcome is still awaited
	maxSMSParts    = 10                 // longer messages are refused: 513
	smsIDPrefix    = "sms-"             // the MESSAGEs of one SMS: this, then its smsKey
)

type smsBridge struct {
	cfg   SMSConfig
	inbox string // SMS over IMS (saveSMS)
	lan   *lanSIP
	log   *slog.Logger
	loc   *time.Location
	now   func() time.Time
	mono  func() time.Time // monotonic instants (time.Now as well); tests step now without it

	poll, sendWait, watchLate, retryMin, retryMax time.Duration

	kickc chan struct{}

	mu    sync.Mutex
	state smsState
	fresh bool                 // no state file yet: what is on the router counts as delivered
	found map[string]time.Time // key → when this process found the SMS, on mono
	retry map[string]time.Time // key|user → not before
	fails map[string]int       // key|user → failed attempts in a row
	seq   int
}

type smsState struct {
	Messages map[string]*smsPending `json:"messages"`
}

type smsPending struct {
	Received string   `json:"received"`          // RFC 3339: when the daemon found it, the start of smsKeepPending
	Pending  []string `json:"pending,omitempty"` // phone logins still waiting for it
}

func newSMSBridge(cfg SMSConfig, inbox string, lan *lanSIP, log *slog.Logger) *smsBridge {
	b := &smsBridge{cfg: cfg.withDefaults(), inbox: inbox, lan: lan, log: log, loc: time.Local, now: time.Now, mono: time.Now,
		poll: smsPoll, sendWait: smsSendWait, watchLate: smsWatchLate, retryMin: time.Minute, retryMax: 15 * time.Minute,
		kickc: make(chan struct{}, 1), found: map[string]time.Time{}, retry: map[string]time.Time{}, fails: map[string]int{},
		state: smsState{Messages: map[string]*smsPending{}}}
	raw, err := os.ReadFile(b.cfg.StateFile)
	switch {
	case os.IsNotExist(err):
		b.fresh = true
	case err != nil:
		log.Warn("SMS to the phones: state not read, starting afresh", "file", b.cfg.StateFile, "err", err)
		b.fresh = true
	default:
		if err := json.Unmarshal(raw, &b.state); err != nil || b.state.Messages == nil {
			log.Warn("SMS to the phones: state damaged, starting afresh", "file", b.cfg.StateFile, "err", err)
			b.state, b.fresh = smsState{Messages: map[string]*smsPending{}}, true
		}
	}
	return b
}

// run reads the spool every poll and on kick, until ctx ends.
func (b *smsBridge) run(ctx context.Context) {
	t := time.NewTicker(b.poll)
	defer t.Stop()
	for {
		b.scan(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-b.kickc:
		}
	}
}

// kick asks for a read of the spool now (an SMS over IMS has come).
func (b *smsBridge) kick() {
	select {
	case b.kickc <- struct{}{}:
	default:
	}
}

// phoneOnline: a phone has registered; what waits for it goes at once.
func (b *smsBridge) phoneOnline(user string) {
	b.mu.Lock()
	for k := range b.retry {
		if strings.HasSuffix(k, "|"+user) {
			delete(b.retry, k)
		}
	}
	b.mu.Unlock()
	b.kick()
}

// smsKey identifies an SMS across reads of the spool: the same sender,
// service centre time stamp and text are one SMS even when GL's poller
// stored it twice (listSMS keeps one copy).
func smsKey(it smsItem) string {
	stamp := it.sent
	if stamp == "" {
		stamp = it.Time
	}
	h := sha256.Sum256([]byte(it.Via + "\x00" + it.From + "\x00" + stamp + "\x00" + it.Text))
	return hex.EncodeToString(h[:10])
}

type smsWork struct {
	key, user string
	it        smsItem
}

// scan finds new SMS, drops what is gone from the router or too old, and
// delivers what is due, oldest first.
func (b *smsBridge) scan(ctx context.Context) {
	items, err := listSMS(b.cfg.Incoming, b.inbox, b.loc, 0)
	if err != nil {
		b.log.Warn("SMS to the phones: cannot read the SMS", "err", err)
		return
	}
	var users []string
	for _, a := range b.lan.cfg.Accounts {
		users = append(users, a.User)
	}
	now, mono := b.now(), b.mono()
	var todo []smsWork
	b.mu.Lock()
	changed := false
	if b.fresh {
		for _, it := range items {
			b.state.Messages[smsKey(it)] = &smsPending{Received: now.Format(time.RFC3339)}
		}
		b.fresh, changed = false, true
		b.log.Info("SMS to the phones: on; the SMS already on the router are not sent to them", "sms", len(items))
	}
	present := map[string]bool{}
	for i := len(items) - 1; i >= 0; i-- { // listSMS gives the newest first
		it := items[i]
		k := smsKey(it)
		present[k] = true
		p := b.state.Messages[k]
		if p == nil {
			p = &smsPending{Received: now.Format(time.RFC3339), Pending: append([]string(nil), users...)}
			b.state.Messages[k] = p
			b.found[k] = mono
			changed = true
			b.log.Info("SMS: new, going to the phones", "from", maskNumber(it.From), "via", it.Via, "phones", len(users))
		}
		if len(p.Pending) == 0 {
			continue
		}
		// After a boot the router's clock starts from the newest file under
		// /etc, behind by at least as long as the router was off, until NTP
		// steps it. An SMS this process found ages on the monotonic clock, so
		// the step does not count, and the file gets its time put right for
		// after a restart.
		var age time.Duration
		if at, ok := b.found[k]; ok {
			age = mono.Sub(at)
			r := now.Add(-age)
			if t, err := time.Parse(time.RFC3339, p.Received); err != nil || r.Sub(t).Abs() > time.Minute {
				p.Received, changed = r.Format(time.RFC3339), true
			}
		} else if t, err := time.Parse(time.RFC3339, p.Received); err == nil {
			age = now.Sub(t)
		}
		if age > smsKeepPending {
			b.log.Warn("SMS: not delivered, the phones were away for too long", "from", maskNumber(it.From), "phones", strings.Join(p.Pending, ","))
			p.Pending, changed = nil, true
			continue
		}
		kept := p.Pending[:0]
		for _, u := range p.Pending {
			if !containsString(users, u) {
				changed = true // the phone has been deleted
				continue
			}
			kept = append(kept, u)
			if until, ok := b.retry[k+"|"+u]; ok && now.Before(until) {
				continue
			}
			todo = append(todo, smsWork{key: k, user: u, it: it})
		}
		p.Pending = kept
	}
	for k := range b.state.Messages {
		if !present[k] {
			delete(b.state.Messages, k) // deleted from the router (GL admin panel)
			delete(b.found, k)
			changed = true
		}
	}
	if changed {
		b.saveLocked()
	}
	b.mu.Unlock()

	failed := map[string]bool{}
	for _, w := range todo {
		if ctx.Err() != nil {
			return
		}
		if failed[w.user] {
			b.later(w.key, w.user, nil) // one try per phone and pass: a silent phone costs msgTimeout each
			continue
		}
		if len(b.lan.reg.forUser(w.user)) == 0 {
			continue // away: its REGISTER brings it back here
		}
		ok, err := b.lan.sendText(ctx, w.user, w.it.From, smsDisplayName(w.it.From), w.it.Text, smsIDPrefix+w.key)
		if !ok {
			failed[w.user] = true
			b.later(w.key, w.user, err)
			continue
		}
		b.delivered(w.key, w.user)
		b.log.Info("SMS: delivered to a phone", "user", w.user, "from", maskNumber(w.it.From))
	}
}

// delivered ends the attempts at one SMS for one phone; false when none were
// pending.
func (b *smsBridge) delivered(key, user string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.retry, key+"|"+user)
	delete(b.fails, key+"|"+user)
	p := b.state.Messages[key]
	if p == nil || !containsString(p.Pending, user) {
		return false
	}
	p.Pending = removeString(p.Pending, user)
	b.saveLocked()
	return true
}

// lateAnswer: a phone has said 200 to an SMS after the wait for the answer
// was over, so no further attempt is due. Over UDP a long text goes in
// pieces and the 200 for one says nothing about the rest: the next attempt
// settles those.
func (b *smsBridge) lateAnswer(id, user string) {
	key, ok := strings.CutPrefix(id, smsIDPrefix)
	if !ok || strings.Contains(key, ".") {
		return
	}
	if b.delivered(key, user) {
		b.log.Info("SMS: delivered to a phone, its answer came late", "user", user)
	}
}

// later puts off the next try for one phone: 1, 2, 4… minutes up to
// retryMax, or at once when the phone registers again.
func (b *smsBridge) later(key, user string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rk := key + "|" + user
	b.fails[rk]++
	wait := b.retryMin
	for i := 1; i < b.fails[rk] && wait < b.retryMax; i++ {
		wait *= 2
	}
	if wait > b.retryMax {
		wait = b.retryMax
	}
	b.retry[rk] = b.now().Add(wait)
	if err != nil {
		b.log.Warn("SMS: the phone did not take it, trying again later", "user", user, "in", wait.String(), "err", err)
	}
}

func (b *smsBridge) saveLocked() {
	if err := writeJSONAtomic(b.cfg.StateFile, b.state); err != nil {
		b.log.Warn("SMS to the phones: state not saved", "file", b.cfg.StateFile, "err", err)
	}
}

// smsDisplayName is the name Linphone shows for a sender: an alphanumeric
// sender ("Operator") as it is, nothing for a number (Linphone finds it among
// the contacts).
func smsDisplayName(from string) string {
	d := strings.TrimPrefix(from, "+")
	if d != "" && digitsOnly(d) == d {
		return ""
	}
	return from
}

// --- out: a phone's message to a number ---

type smsOutcome int

const (
	smsWaiting smsOutcome = iota
	smsSent
	smsFailed
)

// send queues the message for smsd and waits for its verdict a while; the
// answer to the phone: 200 sent, 500 smsd gave up, 202 still at it (then
// watchLate follows it), 503 the spool cannot be written.
func (b *smsBridge) send(user, number, text string) (int, string) {
	name, err := b.queue(number, text)
	if err != nil {
		b.log.Error("SMS: cannot hand it to smsd", "user", user, "err", err)
		return 503, "SMS Unavailable"
	}
	b.log.Info("SMS: from a phone, handed to smsd", "user", user, "to", maskNumber(number), "parts", smsParts(text))
	out, reason := b.wait(name, b.sendWait)
	switch out {
	case smsSent:
		b.log.Info("SMS: sent", "user", user, "to", maskNumber(number))
		return 200, "OK"
	case smsFailed:
		b.log.Warn("SMS: smsd could not send it", "user", user, "to", maskNumber(number), "reason", reason)
		return 500, "SMS Not Sent"
	}
	go b.followLate(user, number, name)
	return 202, "Accepted"
}

// followLate waits for the outcome of an SMS that smsd had not sent by the
// time the phone got its answer; a failure comes to the phone as a message in
// the same conversation.
func (b *smsBridge) followLate(user, number, name string) {
	out, reason := b.wait(name, b.watchLate)
	switch out {
	case smsSent:
		b.log.Info("SMS: sent (late)", "user", user, "to", maskNumber(number))
		return
	case smsWaiting:
		b.log.Warn("SMS: smsd has not sent it yet; is smstools running?", "user", user, "to", maskNumber(number), "after", b.watchLate.String())
		return
	}
	b.log.Warn("SMS: smsd could not send it", "user", user, "to", maskNumber(number), "reason", reason)
	ctx, cancel := context.WithTimeout(context.Background(), 2*msgTimeout)
	defer cancel()
	_, _ = b.lan.sendText(ctx, user, number, "", "The SMS above was not sent ("+reason+").", "")
}

// queue writes the message into smsd's outgoing spool as GL's sendsms does:
// To, Alphabet, Bus, Slot, To_TOA, an empty line, the text. The file appears
// in the spool at once (rename), never half written.
func (b *smsBridge) queue(number, text string) (string, error) {
	b.mu.Lock()
	b.seq++
	name := fmt.Sprintf("mudi-volte-%d-%d-%d", b.now().Unix(), os.Getpid(), b.seq)
	b.mu.Unlock()
	alphabet, body := smsEncoding(text)
	toa := "international"
	if !strings.HasPrefix(number, "+") {
		toa = "National"
	}
	var f bytes.Buffer
	fmt.Fprintf(&f, "To: %s\nAlphabet: %s\nBus: %s\nSlot: %d\nTo_TOA: %s\n\n", number, alphabet, b.cfg.Bus, b.cfg.Slot, toa)
	f.Write(body)
	if fi, err := os.Stat(b.cfg.Outgoing); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("no smsd outgoing spool %s", b.cfg.Outgoing)
	}
	tmp := filepath.Join(filepath.Dir(b.cfg.Outgoing), "."+name)
	if err := os.WriteFile(tmp, f.Bytes(), 0o660); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(b.cfg.Outgoing, name)); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return name, nil
}

// wait looks for the file in sent/ and failed/ until d runs out.
func (b *smsBridge) wait(name string, d time.Duration) (smsOutcome, string) {
	deadline := time.Now().Add(d)
	for {
		if _, err := os.Stat(filepath.Join(b.cfg.Sent, name)); err == nil {
			return smsSent, ""
		}
		if raw, err := os.ReadFile(filepath.Join(b.cfg.Failed, name)); err == nil {
			return smsFailed, failReason(raw)
		}
		if !time.Now().Before(deadline) {
			return smsWaiting, ""
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// failReason is smsd's own word on a failed message (Fail_reason, Error).
func failReason(raw []byte) string {
	head, _, _ := bytes.Cut(raw, []byte("\n\n"))
	reason, errText := "", ""
	for _, line := range strings.Split(string(head), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Fail_reason":
			reason = strings.TrimSpace(v)
		case "Error":
			errText = strings.TrimSpace(v)
		}
	}
	switch {
	case reason != "" && errText != "":
		return reason + ": " + errText
	case reason != "":
		return reason
	case errText != "":
		return errText
	}
	return "no reason given"
}

// gsmSeptets is how many septets r takes when smsd sends the text in the GSM
// 7-bit default alphabet (TS 23.038 §6.2.1, the table of volte/sms): 1, 2 for
// the extension table (€ [ ] { } ^ ~ \ |: the escape and the code), 0 when
// the text has to go as UCS-2. ñ Ñ é É ü Ü ¿ ¡ are in the alphabet; á í ó ú
// are not. The text reaches smsd as UTF-8 and smsd maps it to septets, by
// way of the ISO-8859-15 character set smstools uses for text (to verify on
// the router). So of the alphabet this leaves out what ISO-8859-15 lacks, the
// Greek capitals and ¤, and the form feed, as before: those go as UCS-2.
// Whether GL's smsd sends ñ or € in the GSM alphabet, and not as UCS-2 or as
// "?", is to verify on the router as well.
func gsmSeptets(r rune) int {
	if r == '\f' || r == '¤' || r >= 0x391 && r <= 0x3a9 { // 0x391-0x3a9: the Greek capitals
		return 0
	}
	return sms.SeptetLen(r)
}

// gsmPlain reports whether smsd can send the text in the GSM alphabet.
// Anything else goes as UCS-2, as GL's sendsms does for text outside its
// charset.
func gsmPlain(s string) bool {
	for _, r := range s {
		if gsmSeptets(r) == 0 {
			return false
		}
	}
	return true
}

// smsEncoding returns the Alphabet header and the body for smsd: UTF-8 text
// for the GSM alphabet, big-endian UTF-16 for UCS-2.
func smsEncoding(text string) (string, []byte) {
	if gsmPlain(text) {
		return "UTF-8", []byte(text)
	}
	u := utf16.Encode([]rune(text))
	out := make([]byte, 2*len(u))
	for i, c := range u {
		out[2*i], out[2*i+1] = byte(c>>8), byte(c)
	}
	return "UCS", out
}

// smsParts is how many SMS the text takes: 160 septets or 70 UCS-2 units in
// one, 153 or 67 per part when it is split (TS 23.040 §9.2.3.24.1). A part
// ends early rather than split an escape from its character or a surrogate
// pair, so a character never straddles two parts. smsd splits the text
// itself; this is the count it should come to, for the limit and the log.
func smsParts(text string) int {
	single, part, cost := 160, 153, gsmSeptets
	if !gsmPlain(text) {
		single, part, cost = 70, 67, func(r rune) int {
			if r > 0xffff {
				return 2 // surrogate pair
			}
			return 1
		}
	}
	n := 0
	for _, r := range text {
		n += cost(r)
	}
	if n <= single {
		return 1
	}
	parts, used := 1, 0
	for _, r := range text {
		c := cost(r)
		if used+c > part {
			parts++
			used = 0
		}
		used += c
	}
	return parts
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func removeString(xs []string, s string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
