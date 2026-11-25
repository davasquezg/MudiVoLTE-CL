package main

// AT transport through the GL ubus proxy (Mudi) with protection against the
// §69 trap (the GL poller switches the AT port context between slots; without
// AT+QUIMSUB=<line's slot> commands go to the other subscription). Proxy
// reply format: {"data": "<AT output>"}.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// imsSlot is the subscription of the SIM that carries the line: 1 for the
// nano-SIM in slot 1, 2 for slot 2 or the built-in eSIM, which takes the
// place of SIM 2 (PROTOCOLS 3.3). The GL proxy pins a call to it (sub_id; 0
// leaves the slot to whoever switched it last) and remembers the slot it set,
// so a bare AT+QUIMSUB behind its back makes it skip the pin for GL's own
// requests to the other slot (§117). Set once at start from the
// configuration (sim_slot) by setIMSSlot, before any AT command.
var imsSlot = 1

// setIMSSlot sets the subscription of the line: 1 or 2; 0 keeps slot 1.
func setIMSSlot(n int) error {
	switch n {
	case 0:
		imsSlot = 1
	case 1, 2:
		imsSlot = n
	default:
		return fmt.Errorf("sim_slot %d: 1 or 2", n)
	}
	return nil
}

// subSelect is the AT selection of the line's subscription with its query:
// "QUIMSUB=<n>;+QUIMSUB?" (without the leading "AT+").
func subSelect() string { return fmt.Sprintf("QUIMSUB=%d;+QUIMSUB?", imsSlot) }

// subConfirmed: the reply of the selection query names the line's subscription.
func subConfirmed(out string) bool { return strings.Contains(out, fmt.Sprintf("+QUIMSUB: %d", imsSlot)) }

// atRunner sends one AT line to the modem and returns the raw AT output (the
// ubus JSON is already parsed, line breaks are inside the text).
type atRunner func(ctx context.Context, cmd string) (string, error)

// ubusAT is the real transport: ubus call modem.CPU.AT get_result_AT.
func ubusAT(timeout time.Duration) atRunner {
	return func(ctx context.Context, cmd string) (string, error) {
		payload, err := json.Marshal(map[string]any{
			"cmd":     cmd,
			"timeout": int(timeout.Seconds()),
			"sub_id":  imsSlot,
		})
		if err != nil {
			return "", fmt.Errorf("ubus: encoding %q: %w", cmd, err)
		}
		out, err := exec.CommandContext(ctx, "ubus", "call", "modem.CPU.AT", "get_result_AT", string(payload)).Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) && len(ee.Stderr) > 0 {
				return "", fmt.Errorf("ubus call modem.CPU.AT (%s): %s", atLabel(cmd), strings.TrimSpace(string(ee.Stderr)))
			}
			return "", fmt.Errorf("ubus call modem.CPU.AT (%s): %w", atLabel(cmd), err)
		}
		var resp struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(out), &resp); err != nil {
			return "", fmt.Errorf("ubus: parsing the reply to %s: %s: %w", atLabel(cmd), strings.TrimSpace(string(out)), err)
		}
		if resp.Data == "" {
			return "", fmt.Errorf("ubus: empty reply to %s: %s", atLabel(cmd), strings.TrimSpace(string(out)))
		}
		return resp.Data, nil
	}
}

// ATLink runs every command in the confirmed context of the line's
// subscription (imsSlot): first a separate AT+QUIMSUB=<n> (QIMSCFG does not work in a compound line), then the command
// itself; on symptoms of a foreign context it tries again. Parsing the
// +QUIMSUB reply confirms the slot. Reads go through query and APDUs through
// csim, which keep the selection in the command's own AT line.
type ATLink struct {
	Run     atRunner
	Retries int
	Timeout time.Duration
	Debugf  func(format string, args ...any)
}

func (l *ATLink) command(ctx context.Context, cmd string) (string, error) {
	retries := l.Retries
	if retries < 1 {
		retries = 4
	}
	var last error
	for attempt := 1; attempt <= retries; attempt++ {
		// SET answers just OK; only the query (+QUIMSUB?) confirms the slot, so
		// set+query go as one line (the post.sh pattern, §69).
		sub, err := l.Run(ctx, "AT+"+subSelect())
		if err != nil {
			last = fmt.Errorf("QUIMSUB (attempt %d): %w", attempt, err)
			continue
		}
		if !subConfirmed(sub) {
			last = fmt.Errorf("QUIMSUB (attempt %d): the subscription is not SUB%d: %q", attempt, imsSlot, sub)
			continue
		}
		out, err := l.Run(ctx, cmd)
		if err != nil {
			last = fmt.Errorf("%s (attempt %d): %w", atLabel(cmd), attempt, err)
			continue
		}
		if wrongContext(out) {
			last = fmt.Errorf("%s (attempt %d): reply from the other SIM slot: %q", atLabel(cmd), attempt, out)
			continue
		}
		return out, nil
	}
	return "", last
}

// query sends a read-only request ("AT+…") in one AT line together with the
// selection of the line's subscription: "AT+QUIMSUB=<n>;+QUIMSUB?;+COMMAND". Between two separate
// proxy calls another client (the GL poller, `mudi-volte status`) manages to
// switch the slot, and the reply comes from the empty SUB2 (QCFG ims 0,0
// instead of 2,0: the service took the modem for a non-external one and sat
// idle for 3 minutes). The modem runs one line as a whole. Right after boot
// compound lines sometimes give ERROR (§69); then the old command path is
// taken. Not for QIMSCFG (command) and CSIM (csim).
func (l *ATLink) query(ctx context.Context, cmd string) (string, error) {
	if strings.HasPrefix(cmd, "AT+") {
		line := "AT+" + subSelect() + ";" + cmd[2:]
		for attempt := 1; attempt <= 2; attempt++ {
			out, err := l.Run(ctx, line)
			if err != nil {
				break
			}
			if subConfirmed(out) && !wrongContext(out) {
				return out, nil
			}
		}
	}
	return l.command(ctx, cmd)
}

// csim sends one APDU in a single AT line with the subscription selection, so no
// other client of the port can switch the slot in between: with a live SIM in
// the other slot, an APDU that lands there gets an ordinary reply and nothing
// shows that it went to the wrong card (§117). Only when the line fails, as
// compound lines can right after boot (§69), does it go the command way. A
// reply with +CSIM: is final: the SIM has run the APDU (§72).
func (l *ATLink) csim(ctx context.Context, cmd string) (string, error) {
	out, err := l.Run(ctx, "AT+"+subSelect()+";"+strings.TrimPrefix(cmd, "AT"))
	if err == nil && subConfirmed(out) && strings.Contains(out, "+CSIM:") {
		return out, nil
	}
	return l.command(ctx, cmd)
}

// wrongContext looks for the signs of a reply from the empty slot or of a
// command that did not run (§69: CME ERROR 10, No Service). A valid SIM reply
// (+CSIM:) is never retried: repeating AUTHENTICATE with the same AUTN gives
// DC (the SIM consumes the SQN, §72).
func wrongContext(out string) bool {
	if strings.Contains(out, "+CSIM:") {
		return false
	}
	return strings.Contains(out, "CME ERROR") ||
		strings.Contains(out, "No Service") ||
		strings.Contains(out, "ERROR")
}

// atLabel returns the command name for logs, without arguments (APDUs are not exposed).
func atLabel(cmd string) string {
	if i := strings.IndexByte(cmd, '='); i > 0 {
		cmd = cmd[:i]
	}
	if i := strings.IndexByte(cmd, '?'); i > 0 {
		cmd = cmd[:i]
	}
	return cmd
}
