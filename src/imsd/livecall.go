package main

// The call of the line, for others to see: the state file (mudi-volte
// status, the app for MudiScreen) and MudiScreen itself, an open user
// interface for the Mudi 7 touchscreen, when the router runs it. An
// incoming call lights its display with a notice, which goes when the call
// is answered or over, and every change has it ask the app again. The line
// does not need MudiScreen: without it ubus answers that there is no such
// object, and nothing else happens.

import (
	"context"
	"os/exec"
	"sync"
	"time"
)

// liveCall is the call of the line in the state file.
type liveCall struct {
	Dir   string `json:"dir"`             // in, out, internal
	Phase string `json:"phase"`           // ringing, talking
	Start int64  `json:"start"`           // Unix time the phase began; the state writer sets it
	Phone string `json:"phone,omitempty"` // our side: the phone that answered (in) or called (out, internal)
	Peer  string `json:"peer,omitempty"`  // the other side: the number ("" when hidden) or the called phone
}

func sameCall(a, b *liveCall) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Dir == b.Dir && a.Phase == b.Phase && a.Phone == b.Phone && a.Peer == b.Peer
}

// Notices on MudiScreen's display. They show before the passcode: no
// numbers. The ids let a later call take a notice back (dismiss).
const (
	incomingNotice = `{"id":"volte-call","title":"Incoming call","text":"Answer it on a phone","icon":"phone","color":"green","app":"volte","page":"main","timeout":120}`
	incomingGone   = `{"id":"volte-call"}`
	refreshApp     = `{"app":"volte"}`
)

// screenLink passes ubus calls to MudiScreen one at a time, in the order
// they come, so that the notice of a call is never taken back before it was
// shown. A full queue drops a call: the line never waits on the display.
type screenLink struct {
	once sync.Once
	ch   chan []string
	run  func(ctx context.Context, args ...string) error
}

var mudiScreen = &screenLink{run: runUbus}

func runUbus(ctx context.Context, args ...string) error {
	return exec.CommandContext(ctx, "ubus", args...).Run()
}

func (l *screenLink) send(args ...string) {
	l.once.Do(func() {
		l.ch = make(chan []string, 16)
		go func() {
			for a := range l.ch {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = l.run(ctx, a...)
				cancel()
			}
		}()
	})
	select {
	case l.ch <- args:
	default:
	}
}

// callChanged tells MudiScreen that the call of the line is now cur (nil:
// none); prev is the one before.
func (l *screenLink) callChanged(prev, cur *liveCall) {
	ringingIn := func(c *liveCall) bool { return c != nil && c.Dir == "in" && c.Phase == "ringing" }
	switch {
	case ringingIn(cur) && !ringingIn(prev):
		l.send("call", "mudi-screen", "notify", incomingNotice)
	case ringingIn(prev) && !ringingIn(cur):
		l.send("call", "mudi-screen", "dismiss", incomingGone)
	}
	l.send("call", "mudi-screen", "refresh", refreshApp)
}
