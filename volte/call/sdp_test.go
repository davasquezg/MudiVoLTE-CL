package call

import (
	"fmt"
	"net/netip"
	"os"
	"strings"
	"testing"
)

func TestParseLiveOffer(t *testing.T) {
	body, err := os.ReadFile("testdata/live_offer.sdp")
	if err != nil {
		t.Fatal(err)
	}
	a, err := ParseSDP(body)
	if err != nil {
		t.Fatal(err)
	}
	if a.Addr != netip.MustParseAddrPort("192.0.2.82:26390") || a.Ptime != 20 || a.Direction != "sendrecv" {
		t.Fatalf("%+v", a)
	}
	if len(a.Formats) != 17 || a.Formats[0].Name != "AMR-WB" || a.Formats[0].ClockRate != 16000 {
		t.Fatalf("formats %+v", a.Formats[:2])
	}
	if ms, _ := a.Formats[0].Param("mode-set"); ms != "0,1,2" {
		t.Fatalf("mode-set %q", ms)
	}
	n, err := Negotiate(a, DefaultCodecs)
	if err != nil {
		t.Fatal(err)
	}
	if n.Codec.PT != 101 || n.DTMF == nil || n.DTMF.PT != 116 || n.Codec.OctetAligned() {
		t.Fatalf("negotiated %+v dtmf %+v", n.Codec, n.DTMF)
	}
	ans := string(n.Answer(netip.MustParseAddrPort("198.51.100.26:40000"), 7, 1, a.Direction).Bytes())
	for _, want := range []string{
		"o=- 7 1 IN IP4 198.51.100.26\r\n", "c=IN IP4 198.51.100.26\r\n", "m=audio 40000 RTP/AVP 101 116\r\n",
		"a=rtpmap:101 AMR-WB/16000\r\n", "a=fmtp:101 mode-set=0,1,2;", "a=rtpmap:116 telephone-event/16000\r\n",
		"a=fmtp:116 0-15\r\n", "a=sendrecv\r\n",
	} {
		if !strings.Contains(ans, want) {
			t.Errorf("answer lacks %q:\n%s", want, ans)
		}
	}
	// The answer must parse back to the same stream.
	back, err := ParseSDP([]byte(ans))
	if err != nil || back.Formats[0].PT != 101 {
		t.Fatalf("round trip: %v %+v", err, back)
	}
}

func TestNegotiateFallbackAndHold(t *testing.T) {
	offer := "v=0\r\nc=IN IP4 192.0.2.1\r\nm=audio 5004 RTP/AVP 0 101\r\na=rtpmap:101 telephone-event/8000\r\na=sendonly\r\n"
	a, err := ParseSDP([]byte(offer))
	if err != nil {
		t.Fatal(err)
	}
	n, err := Negotiate(a, DefaultCodecs)
	if err != nil || n.Codec.Name != "PCMU" || n.DTMF == nil {
		t.Fatalf("%+v %v", n, err)
	}
	if d := n.Answer(netip.MustParseAddrPort("192.0.2.2:6000"), 1, 1, a.Direction); d.Direction != "recvonly" {
		t.Fatalf("hold answer direction %q", d.Direction)
	}
	if _, err := Negotiate(a, []string{"AMR-WB"}); err == nil {
		t.Fatal("expected no common codec")
	}
}

func TestOfferFormatsFiltered(t *testing.T) {
	var names []string
	for _, f := range OfferFormats([]string{"PCMA", "PCMU"}) {
		names = append(names, fmt.Sprintf("%s/%d", f.Name, f.ClockRate))
	}
	if got := strings.Join(names, " "); got != "PCMA/8000 PCMU/8000 telephone-event/8000" {
		t.Fatal(got)
	}
	if len(OfferFormats(nil)) != 6 {
		t.Fatal("unfiltered offer")
	}
	// Our full offer: AMR-WB, AMR, G.711, a telephone-event for each rate.
	names = nil
	for _, f := range OfferFormats(DefaultCodecs) {
		names = append(names, fmt.Sprintf("%d %s/%d", f.PT, f.Name, f.ClockRate))
	}
	if got := strings.Join(names, ", "); got != "104 AMR-WB/16000, 102 AMR/8000, 8 PCMA/8000, 0 PCMU/8000, "+
		"105 telephone-event/16000, 101 telephone-event/8000" {
		t.Fatal(got)
	}
	// Without AMR-WB linked in, AMR leads.
	names = nil
	for _, f := range OfferFormats([]string{"AMR", "PCMA", "PCMU"}) {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, " "); got != "AMR PCMA PCMU telephone-event" {
		t.Fatal(got)
	}
}

// An offer from an interconnect with only AMR (several payload types with
// different mode-sets, as in the live offer) and no G.711 is answered with
// the first AMR format, its fmtp unchanged (RFC 4867 §8.3.1: the mode-set is
// returned as offered) and the 8 kHz telephone-event.
func TestNegotiateAMROnly(t *testing.T) {
	offer := "v=0\r\nc=IN IP4 192.0.2.1\r\nm=audio 5004 RTP/AVP 100 113 96\r\n" +
		"a=rtpmap:100 AMR/8000\r\na=fmtp:100 mode-set=0,2,4,7;mode-change-period=2;octet-align=1\r\n" +
		"a=rtpmap:113 AMR/8000\r\na=fmtp:113 mode-change-capability=2;max-red=0\r\n" +
		"a=rtpmap:96 telephone-event/8000\r\na=ptime:20\r\n"
	a, err := ParseSDP([]byte(offer))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Negotiate(a, []string{"AMR-WB", "PCMA", "PCMU"}); err == nil {
		t.Fatal("AMR accepted without AMR in the list")
	}
	n, err := Negotiate(a, DefaultCodecs)
	if err != nil {
		t.Fatal(err)
	}
	if n.Codec.PT != 100 || n.Codec.Name != "AMR" || n.Codec.ClockRate != 8000 || !n.Codec.OctetAligned() ||
		n.DTMF == nil || n.DTMF.PT != 96 {
		t.Fatalf("negotiated %+v dtmf %+v", n.Codec, n.DTMF)
	}
	if ms, _ := n.Codec.Param("mode-set"); ms != "0,2,4,7" {
		t.Fatalf("mode-set %q", ms)
	}
	ans := string(n.Answer(netip.MustParseAddrPort("198.51.100.26:40000"), 1, 1, a.Direction).Bytes())
	for _, want := range []string{
		"m=audio 40000 RTP/AVP 100 96\r\n", "b=AS:30\r\n", "a=rtpmap:100 AMR/8000\r\n",
		"a=fmtp:100 mode-set=0,2,4,7;mode-change-period=2;octet-align=1\r\n", "a=rtpmap:96 telephone-event/8000\r\n",
	} {
		if !strings.Contains(ans, want) {
			t.Errorf("answer lacks %q:\n%s", want, ans)
		}
	}
	// The live offer lists AMR-WB first: AMR-WB stays the choice.
	body, err := os.ReadFile("testdata/live_offer.sdp")
	if err != nil {
		t.Fatal(err)
	}
	live, _ := ParseSDP(body)
	if n, err := Negotiate(live, DefaultCodecs); err != nil || n.Codec.Name != "AMR-WB" {
		t.Fatalf("live offer: %+v %v", n.Codec, err)
	}
	// Without AMR-WB the first AMR of the live offer (PT 108, mode-set=7)
	// with the 8 kHz telephone-event (PT 96).
	if n, err := Negotiate(live, []string{"AMR", "PCMA", "PCMU"}); err != nil || n.Codec.PT != 108 || n.DTMF == nil || n.DTMF.PT != 96 {
		t.Fatalf("live offer without AMR-WB: %+v %+v %v", n.Codec, n.DTMF, err)
	}
}

// The offerer's order decides between AMR and G.711 (RFC 3264 §6.1). An offer
// like the one of §86 (AMR-NB before PCMU), which used to be answered with
// PCMU, is now answered with AMR; with "codecs" set to AMR-WB and G.711 only
// it goes back to PCMU. An offer that lists PCMU first keeps PCMU.
func TestNegotiateAMRBeforeG711(t *testing.T) {
	offer := func(order string) Audio {
		a, err := ParseSDP([]byte("v=0\r\nc=IN IP4 192.0.2.1\r\nm=audio 5004 RTP/AVP " + order + "\r\n" +
			"a=rtpmap:102 AMR/8000\r\na=fmtp:102 mode-set=0,2,4,7\r\n" +
			"a=rtpmap:0 PCMU/8000\r\na=rtpmap:101 telephone-event/8000\r\na=ptime:20\r\n"))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	for _, tc := range []struct {
		order     string
		supported []string
		name      string
		pt        int
	}{
		{"102 0 101", DefaultCodecs, "AMR", 102},
		{"102 0 101", []string{"AMR-WB", "PCMA", "PCMU"}, "PCMU", 0},
		{"0 102 101", DefaultCodecs, "PCMU", 0},
	} {
		n, err := Negotiate(offer(tc.order), tc.supported)
		if err != nil || n.Codec.Name != tc.name || n.Codec.PT != tc.pt || n.DTMF == nil || n.DTMF.PT != 101 {
			t.Errorf("offer %q, supported %v: %+v dtmf %+v %v", tc.order, tc.supported, n.Codec, n.DTMF, err)
		}
	}
}
