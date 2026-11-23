package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emiago/sipgo"
)

// --- The call of the line: the state file, mudi-volte status, MudiScreen ---

func TestStateWriterCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	w := newStateWriter(path, quietLog())
	mono := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	w.mono = func() time.Time { return mono }
	w.now = func() time.Time { return mono }
	read := func() serviceState {
		t.Helper()
		var s serviceState
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, &s) != nil {
			t.Fatalf("state file: %v %s", err, raw)
		}
		return s
	}
	if prev := w.setCall(&liveCall{Dir: "in", Phase: "ringing", Peer: "+447700900123"}); prev != nil {
		t.Fatalf("prev %+v", prev)
	}
	if c := read().Call; c == nil || c.Phase != "ringing" || c.Start != mono.Unix() {
		t.Fatalf("ringing: %+v", c)
	}
	mono = mono.Add(12 * time.Second)
	if prev := w.setCall(&liveCall{Dir: "in", Phase: "talking", Phone: "pixel", Peer: "+447700900123"}); prev == nil || prev.Phase != "ringing" {
		t.Fatalf("prev %+v", prev)
	}
	answered := mono.Unix()
	mono = mono.Add(90 * time.Second)
	w.update(nil) // any later write keeps the start of the phase
	if c := read().Call; c == nil || c.Phase != "talking" || c.Phone != "pixel" || c.Start != answered {
		t.Fatalf("talking: %+v, want start %d", c, answered)
	}
	w.setCall(nil)
	if c := read().Call; c != nil {
		t.Fatalf("after the call: %+v", c)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), `"call"`) {
		t.Fatalf("an empty call stays in the file:\n%s", raw)
	}
}

// recordLink is a screenLink that keeps the calls it makes.
func recordLink(t *testing.T) (*screenLink, func() []string) {
	var mu sync.Mutex
	var calls []string
	l := &screenLink{run: func(_ context.Context, args ...string) error {
		mu.Lock()
		calls = append(calls, strings.Join(args[2:], " "))
		mu.Unlock()
		return errors.New("Not found") // as without MudiScreen
	}}
	return l, func() []string {
		t.Helper()
		var out []string
		for i := 0; i < 100; i++ {
			mu.Lock()
			out = append(out[:0], calls...)
			mu.Unlock()
			if len(l.ch) == 0 {
				time.Sleep(20 * time.Millisecond) // the last one being run
				mu.Lock()
				out = append(out[:0], calls...)
				mu.Unlock()
				return out
			}
			time.Sleep(10 * time.Millisecond)
		}
		return out
	}
}

// An incoming call lights the display with a notice that goes when the
// call is answered; outgoing calls only have the app asked again. The order
// holds, so a notice is never taken back before it was shown.
func TestScreenLinkCallNotices(t *testing.T) {
	l, calls := recordLink(t)
	ring := &liveCall{Dir: "in", Phase: "ringing", Peer: "+447700900123"}
	talk := &liveCall{Dir: "in", Phase: "talking", Phone: "pixel", Peer: "+447700900123"}
	l.callChanged(nil, ring)
	l.callChanged(ring, talk)
	l.callChanged(talk, nil)
	out := &liveCall{Dir: "out", Phase: "ringing", Phone: "pixel", Peer: "+447700900456"}
	l.callChanged(nil, out)
	l.callChanged(out, nil)
	want := []string{
		"notify " + incomingNotice, "refresh " + refreshApp,
		"dismiss " + incomingGone, "refresh " + refreshApp,
		"refresh " + refreshApp,
		"refresh " + refreshApp,
		"refresh " + refreshApp,
	}
	if got := calls(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ubus calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A notice shows before the passcode: no number in it.
	number := regexp.MustCompile(`\+|[0-9]{5,}`)
	for _, n := range []string{incomingNotice, screenNotice} {
		if !json.Valid([]byte(n)) || number.MatchString(n) {
			t.Fatalf("bad notice %s", n)
		}
	}
}

// The line never waits on the display: a MudiScreen that does not answer
// (or is not there) costs a call nothing.
func TestScreenLinkNeverBlocks(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	l := &screenLink{run: func(ctx context.Context, _ ...string) error {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return ctx.Err()
	}}
	start := time.Now()
	for i := 0; i < 200; i++ {
		l.callChanged(nil, &liveCall{Dir: "in", Phase: "ringing"})
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("200 notices with a stuck display took %v", d)
	}
}

// An incoming call is shown ringing, then talking with the phone that
// answered, then gone; a hidden number shows as none.
func TestSIPIncomingCallShown(t *testing.T) {
	rig := newTestRig(t, &Config{IncomingAll: true}, true)
	var mu sync.Mutex
	var seen []string
	rig.mgr.onCall = func(c *liveCall) {
		mu.Lock()
		defer mu.Unlock()
		if c == nil {
			seen = append(seen, "none")
			return
		}
		seen = append(seen, c.Dir+" "+c.Phase+" "+c.Phone+" "+c.Peer)
	}
	ph := newTestPhone(t)
	if res, err := ph.register(rig.srvAddr, testPass); err != nil || res.StatusCode != 200 {
		t.Fatalf("registration: %v %v", res, err)
	}
	call := newFakeIMSCall("in-shown", CallIncoming)
	rig.mgr.incomingFromNetwork(call, "+447700900123")
	var dlg *sipgo.DialogServerSession
	select {
	case dlg = <-ph.incoming:
	case <-time.After(3 * time.Second):
		t.Fatal("the INVITE did not reach the phone")
	}
	if err := dlg.RespondSDP(ph.sdp()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, call.answered.Load, "the network was not answered")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := dlg.Bye(ctx); err != nil {
		t.Fatalf("BYE from the phone: %v", err)
	}
	waitFor(t, func() bool { return rig.mgr.current() == nil }, "the line was not released")
	mu.Lock()
	got := strings.Join(seen, " | ")
	mu.Unlock()
	if want := "in ringing  +447700900123 | in talking pixel +447700900123 | none"; got != want {
		t.Fatalf("shown: %s\nwant:  %s", got, want)
	}
	if shownNumber("") != "" || shownNumber("anonymous") != "" || shownNumber("+44 7700 900123") != "+447700900123" {
		t.Fatal("shownNumber")
	}
}

func TestPrintStatusCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	w := newStateWriter(path, quietLog())
	w.update(func(s *serviceState) { s.Phase, s.Registered = "registered", true })
	w.setCall(&liveCall{Dir: "in", Phase: "talking", Phone: "pixel", Peer: "+447700900123"})
	var buf bytes.Buffer
	printStatus(&buf, path, time.Now().Add(75*time.Second))
	if !strings.Contains(buf.String(), "Call:    incoming from …0123, answered by pixel, talking for 1m") ||
		strings.Contains(buf.String(), "900123") {
		t.Fatalf("status:\n%s", buf.String())
	}
}
