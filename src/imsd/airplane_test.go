package main

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// --- Service: the radio turned off on the router (airplane mode) ---

func TestParseCFUN(t *testing.T) {
	for out, want := range map[string]int{
		"\r\n+CFUN: 1\r\n\r\nOK\r\n": 1,
		"\r\n+CFUN: 4\r\n\r\nOK\r\n": 4,
		"+CFUN: 0\r\nOK":             0,
	} {
		if got, ok := parseCFUN(out); !ok || got != want {
			t.Errorf("parseCFUN(%q) = %d, %v; want %d", out, got, ok, want)
		}
	}
	for _, out := range []string{"\r\nOK\r\n", "+CFUN: x", "ERROR"} {
		if _, ok := parseCFUN(out); ok {
			t.Errorf("parseCFUN(%q) read a value", out)
		}
	}
}

// runLoopWith is runLoop with more canned modem answers.
func runLoopWith(t *testing.T, cfg *Config, n int, answers map[string]string, options ...func(*service)) (waits []time.Duration, cmds []string, st serviceState) {
	t.Helper()
	all := map[string]string{
		`AT+QCFG="ims"`: "\r\n+QCFG: \"ims\",2,0\r\n\r\nOK\r\n",
		"AT+CEREG?":     "\r\n+CEREG: 0,1\r\n\r\nOK\r\n",
		"AT+COPS?":      "\r\n+COPS: 0,0,\"Op\",7\r\n\r\nOK\r\n",
	}
	maps.Copy(all, answers)
	m := fakeModem(all, &cmds)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := &service{cfg: cfg, log: quietLog(), link: m.link, modem: m,
		state: newStateWriter(filepath.Join(t.TempDir(), "state.json"), quietLog()), net: &netSwitch{}}
	s.sleep = func(c context.Context, d time.Duration) error {
		if waits = append(waits, d); len(waits) == n {
			cancel()
		}
		return c.Err()
	}
	for _, o := range options {
		o(s)
	}
	s.loop(ctx)
	if len(waits) != n {
		t.Fatalf("the loop waited %d times, not %d: %v", len(waits), n, waits)
	}
	return waits, cmds, s.state.snapshot()
}

const cfunOff = "\r\n+CFUN: 4\r\n\r\nOK\r\n"

// With the radio off on the router the service waits: no data call, no
// failure, no restart of the radio, which would end the airplane mode.
func TestLoopWaitsWhileTheRadioIsOff(t *testing.T) {
	waits, cmds, st := runLoopWith(t, defaultConfig(), 3, map[string]string{"AT+CFUN?": cfunOff})
	if want := []time.Duration{30 * time.Second, 30 * time.Second, 30 * time.Second}; !slices.Equal(waits, want) {
		t.Errorf("waits %v, want %v", waits, want)
	}
	if slices.Contains(cmds, "AT+CFUN=4") || slices.Contains(cmds, "AT+CFUN=1") {
		t.Errorf("the radio was switched while it was off: %v", cmds)
	}
	if slices.Contains(cmds, "AT+CEREG?") {
		t.Errorf("the service waited for LTE with the radio off: %v", cmds)
	}
	if st.Phase != "radio-off" || st.Restarts != 0 || st.LastError != "" {
		t.Errorf("state: phase %q, restarts %d, last error %q", st.Phase, st.Restarts, st.LastError)
	}
}

// A radio restart that a failure called for does not happen when the radio
// was turned off meanwhile: the airplane mode stays.
func TestLoopNoRadioRestartWhileTheRadioIsOff(t *testing.T) {
	up, _ := scriptedUp(scriptedSession{endRAT, false})
	if _, cmds, _ := runLoopWith(t, defaultConfig(), 2, map[string]string{"AT+CFUN?": cfunOff}, up); slices.Contains(cmds, "AT+CFUN=4") {
		t.Errorf("the radio was restarted while it was off on the router: %v", cmds)
	}
	// With the radio on, the same failures restart it, as before.
	up, _ = scriptedUp(scriptedSession{endRAT, false})
	_, cmds, _ := runLoopWith(t, defaultConfig(), 2, map[string]string{"AT+CFUN?": "\r\n+CFUN: 1\r\n\r\nOK\r\n"}, up)
	if !strings.Contains(strings.Join(cmds, " "), "AT+CFUN=4 AT+CFUN=1") {
		t.Errorf("a fall back to 3G did not restart the radio: %v", cmds)
	}
}
