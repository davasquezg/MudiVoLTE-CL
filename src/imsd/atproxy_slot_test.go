package main

import (
	"context"
	"strings"
	"testing"
)

// withSlot runs a test with the line on subscription n and puts slot 1 back.
func withSlot(t *testing.T, n int) {
	t.Helper()
	if err := setIMSSlot(n); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = setIMSSlot(1) })
}

func TestSetIMSSlot(t *testing.T) {
	t.Cleanup(func() { _ = setIMSSlot(1) })
	for _, c := range []struct{ in, want int }{{0, 1}, {1, 1}, {2, 2}} {
		if err := setIMSSlot(c.in); err != nil || imsSlot != c.want {
			t.Errorf("setIMSSlot(%d): slot %d, err %v; want %d", c.in, imsSlot, err, c.want)
		}
	}
	for _, bad := range []int{-1, 3} {
		if err := setIMSSlot(bad); err == nil {
			t.Errorf("setIMSSlot(%d): want an error", bad)
		}
	}
}

// A line on the eSIM (subscription 2): every AT line selects SUB2 and a reply
// from SUB1 does not count as confirmed.
func TestATLinkOnSubscription2(t *testing.T) {
	withSlot(t, 2)
	var lines []string
	link := &ATLink{
		Retries: 2,
		Run: func(ctx context.Context, cmd string) (string, error) {
			lines = append(lines, cmd)
			switch {
			case cmd == "AT+QUIMSUB=2;+QUIMSUB?":
				return "\r\n+QUIMSUB: 2,\"SUB2\"\r\n\r\nOK\r\n", nil
			case strings.HasPrefix(cmd, "AT+QUIMSUB=2;+QUIMSUB?;+CSIM="):
				return "\r\n+QUIMSUB: 2,\"SUB2\"\r\n+CSIM: 4,\"9000\"\r\n\r\nOK\r\n", nil
			case strings.HasPrefix(cmd, "AT+QUIMSUB=2;+QUIMSUB?;+CIMI"):
				return "\r\n+QUIMSUB: 2,\"SUB2\"\r\n001010000000000\r\n\r\nOK\r\n", nil
			case cmd == `AT+QIMSCFG="impu"`:
				return "\r\n+QIMSCFG: \"impu\",\"sip:001010000000000@ims.mnc001.mcc001.3gppnetwork.org\"\r\n\r\nOK\r\n", nil
			}
			return "\r\nERROR\r\n", nil
		},
	}
	ctx := context.Background()
	if out, err := link.query(ctx, "AT+CIMI"); err != nil || !strings.Contains(out, "001010000000000") {
		t.Fatalf("query: %q, %v", out, err)
	}
	if out, err := link.csim(ctx, `AT+CSIM=10,"00A4040007"`); err != nil || !strings.Contains(out, "+CSIM:") {
		t.Fatalf("csim: %q, %v", out, err)
	}
	if out, err := link.command(ctx, `AT+QIMSCFG="impu"`); err != nil || !strings.Contains(out, "impu") {
		t.Fatalf("command: %q, %v", out, err)
	}
	for _, l := range lines {
		if strings.Contains(l, "QUIMSUB=1") {
			t.Errorf("a line selected SUB1: %q", l)
		}
	}
}

func TestATLinkSubscription2RefusesSUB1Reply(t *testing.T) {
	withSlot(t, 2)
	link := &ATLink{
		Retries: 2,
		Run: func(ctx context.Context, cmd string) (string, error) {
			// The proxy left the port on SUB1: the query names it.
			return "\r\n+QUIMSUB: 1,\"SUB1\"\r\n\r\nOK\r\n", nil
		},
	}
	_, err := link.command(context.Background(), "AT+CGSN")
	if err == nil || !strings.Contains(err.Error(), "not SUB2") {
		t.Fatalf("want a subscription error naming SUB2, got %v", err)
	}
}

func TestSMSSlotFollowsTheLine(t *testing.T) {
	withSlot(t, 2)
	if got := (SMSConfig{}).withDefaults().Slot; got != 2 {
		t.Errorf("SMS slot %d, want 2", got)
	}
	if got := (SMSConfig{Slot: 1}).withDefaults().Slot; got != 1 {
		t.Errorf("an explicit SMS slot is kept: %d", got)
	}
}
