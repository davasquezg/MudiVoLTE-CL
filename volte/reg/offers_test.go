package reg

import (
	"context"
	"encoding/hex"
	"log/slog"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xarvel/MudiVoLTE/volte/auth"
)

func TestParseOffer(t *testing.T) {
	for in, want := range map[string]Offer{
		"hmac-md5-96/null":              {"hmac-md5-96", "null"},
		"hmac-sha-1-96/aes-cbc":         {"hmac-sha-1-96", "aes-cbc"},
		" HMAC-SHA-1-96 / DES-EDE3-CBC": {"hmac-sha-1-96", "des-ede3-cbc"},
		"hmac-md5-96":                   {"hmac-md5-96", "null"},
	} {
		got, err := ParseOffer(in)
		if err != nil || got != want {
			t.Errorf("ParseOffer(%q) = %v, %v; want %v", in, got, err, want)
		}
		if again, err := ParseOffer(got.String()); err != nil || again != got {
			t.Errorf("%q does not read back: %v, %v", got.String(), again, err)
		}
	}
	for _, in := range []string{"", "/null", "hmac-sha-256/null", "hmac-md5-96/aes-gcm", "null/hmac-md5-96", "hmac-md5-96/null/x"} {
		if o, err := ParseOffer(in); err == nil {
			t.Errorf("ParseOffer(%q) accepted: %v", in, o)
		}
	}
}

// Every default pair is one the IPsec layer installs, and the pair offered
// alone before stays first.
func TestDefaultOffers(t *testing.T) {
	if DefaultOffers[0] != (Offer{"hmac-md5-96", "null"}) {
		t.Fatalf("first default offer %v", DefaultOffers[0])
	}
	for _, o := range DefaultOffers {
		if err := o.check(); err != nil {
			t.Errorf("%v: %v", o, err)
		}
	}
}

func TestNewRejectsAnOfferItCannotInstall(t *testing.T) {
	cfg := Config{IMPI: impi, IMPU: impu, Domain: domain, UEAddr: netip.MustParseAddr("127.0.0.1"),
		PCSCF: netip.MustParseAddrPort("127.0.0.1:5060"), Offers: []Offer{{"hmac-md5-96", "null"}, {"hmac-sha-256", "null"}}}
	if _, err := New(cfg, &fakeSIM{}, nil); err == nil || !strings.Contains(err.Error(), "hmac-sha-256") {
		t.Fatalf("New: %v", err)
	}
}

func clientWithOffers(t *testing.T, f *fakePCSCF, r *fakeRunner, offers []Offer) *Client {
	t.Helper()
	c, err := New(Config{
		IMPI: impi, IMPU: impu, Domain: domain,
		UEAddr: netip.MustParseAddr("127.0.0.1"), PCSCF: f.plain.Addr(),
		PANI: "3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000001", UserAgent: "volte-test",
		Offers: offers, IPsec: r, Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}, &fakeSIM{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.teardown)
	return c
}

// The Security-Client lists the default pairs in their order, all with the
// same SPIs and ports (TS 33.203 §7.1), and the network's choice of one of
// them is what goes into the kernel.
func TestSecurityClientOffersEveryDefaultPair(t *testing.T) {
	f, r := newFakePCSCF(t), &fakeRunner{}
	f.set(func() { f.secAlgs = "alg=hmac-sha-1-96;prot=esp;mod=trans;ealg=aes-cbc" })
	c := clientWithOffers(t, f, r, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Register(ctx); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	raw := f.held.clientRaw
	f.mu.Unlock()
	ms, err := auth.ParseMechanisms(raw)
	if err != nil {
		t.Fatal(err)
	}
	var got []Offer
	for _, m := range ms {
		got = append(got, Offer{m.Alg, m.Ealg})
		if m.Name != auth.MechIPsec3GPP || m.Prot != "esp" || m.Mode != "trans" ||
			m.SPIc != ms[0].SPIc || m.SPIs != ms[0].SPIs || m.PortC != ms[0].PortC || m.PortS != ms[0].PortS {
			t.Errorf("mechanism %q", m.Raw())
		}
	}
	if !slices.Equal(got, DefaultOffers) {
		t.Errorf("Security-Client %q\noffers %v, want %v", raw, got, DefaultOffers)
	}

	// TS 33.203 Annex I: SHA-1 takes IK with 32 zero bits, AES-CBC takes CK.
	_, ck, ik := akaVectors(f.rand)
	states := 0
	for _, cmd := range r.cmds {
		if !strings.HasPrefix(cmd, "xfrm state add") {
			continue
		}
		states++
		if !strings.Contains(cmd, "auth-trunc hmac(sha1) 0x"+hex.EncodeToString(ik)+"00000000 96") ||
			!strings.Contains(cmd, "enc cbc(aes) 0x"+hex.EncodeToString(ck)) {
			t.Errorf("SA %s", cmd)
		}
	}
	if states != 4 {
		t.Errorf("%d SAs: %v", states, r.cmds)
	}
}

// A network that picks a pair left out of the offer gets no SAs.
func TestServerChoiceOutsideTheOfferIsRefused(t *testing.T) {
	f, r := newFakePCSCF(t), &fakeRunner{}
	f.set(func() { f.secAlgs = "alg=hmac-sha-1-96;prot=esp;mod=trans;ealg=null" })
	c := clientWithOffers(t, f, r, []Offer{{"hmac-md5-96", "null"}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Register(ctx); err == nil || !strings.Contains(err.Error(), "none of the offered") {
		t.Fatalf("Register: %v", err)
	}
	if n := r.count("xfrm state add"); n != 0 {
		t.Errorf("%d SAs installed", n)
	}
}

// A kernel without the cipher the network picks does not keep the client
// from registering: the next attempt offers only the pairs without
// encryption, and so do the ones after it.
func TestCipherTheKernelLacksFallsBackToNullEncryption(t *testing.T) {
	f, r := newFakePCSCF(t), &fakeRunner{fail: "enc cbc(aes)"}
	f.set(func() {
		f.secAlgs = "alg=hmac-sha-1-96;prot=esp;mod=trans;ealg=aes-cbc,alg=hmac-md5-96;prot=esp;mod=trans;ealg=null"
	})
	c := clientWithOffers(t, f, r, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Register(ctx); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	raw := f.held.clientRaw
	f.mu.Unlock()
	ms, err := auth.ParseMechanisms(raw)
	if err != nil {
		t.Fatal(err)
	}
	var got []Offer
	for _, m := range ms {
		got = append(got, Offer{m.Alg, m.Ealg})
	}
	if want := []Offer{{"hmac-md5-96", "null"}, {"hmac-sha-1-96", "null"}}; !slices.Equal(got, want) {
		t.Errorf("Security-Client after the failure %q: offers %v, want %v", raw, got, want)
	}
	if b, ok := c.Binding(); !ok || b.Expires == 0 {
		t.Errorf("not registered: %+v %v", b, ok)
	}
	var held int
	for _, cmd := range r.since(0) {
		if strings.HasPrefix(cmd, "xfrm state add") && strings.Contains(cmd, "auth-trunc hmac(md5)") && !strings.Contains(cmd, "enc cbc(aes)") {
			held++
		}
	}
	if held != 4 {
		t.Errorf("%d SAs with hmac(md5) and no cipher: %v", held, r.since(0))
	}
}

// Without a pair free of encryption in the offer there is nothing to fall
// back to: the failure is reported as it is.
func TestCipherFailureWithoutNullPairIsReported(t *testing.T) {
	f, r := newFakePCSCF(t), &fakeRunner{fail: "enc cbc(aes)"}
	f.set(func() { f.secAlgs = "alg=hmac-md5-96;prot=esp;mod=trans;ealg=aes-cbc" })
	c := clientWithOffers(t, f, r, []Offer{{"hmac-md5-96", "aes-cbc"}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Register(ctx); err == nil || !strings.Contains(err.Error(), "installing SAs") {
		t.Fatalf("Register: %v", err)
	}
	if n := len(f.seen()); n != 1 {
		t.Errorf("%d REGISTER requests, want 1 (no retry without a fallback)", n)
	}
}
