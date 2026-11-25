package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
)

// TestParseQENGANICells covers the cell formats (#7) and the MNC length (#12).
// The NR5G-SA lines have the field order of Quectel's own example
// (QConnectManager atc.c) and of a live RG650V capture; the cells are
// fictitious (MCC 001, the 3GPP test network).
func TestParseQENGANICells(t *testing.T) {
	const (
		lte  = "+QENG: \"servingcell\",\"NOCONN\",\"LTE\",\"FDD\",001,%s,1A2B03,123,1300,3,5,5,3C0D,-102,-14,-67,12,0,-,25\r\nOK\r\n"
		saF  = "+QENG: \"servingcell\",\"NOCONN\",\"NR5G-SA\",\"FDD\",001,01,1A2B3C4D5,721,3C0D12,127490,71,2,-99,-13,4,0,-\r\n\r\nOK\r\n"
		saT  = "+QENG: \"servingcell\",\"NOCONN\",\"NR5G-SA\",\"TDD\", 001,01,0,21,4ED,636576,78,3,-85,-11,32,0,5184\r\nOK\r\n"
		sa3  = "+QENG: \"servingcell\",\"CONNECT\",\"NR5G-SA\",\"TDD\",001,001,C9A82340B,249,C94B00,627264,78,12,-86,-11,30,1,-\r\nOK\r\n"
		nsa  = "\r\n+QENG: \"servingcell\",\"NOCONN\"\r\n+QENG: \"LTE\",\"FDD\",001,01,DF30C,61,1700,3,5,5,41,-97,-7,-69,18,12,150,-\r\n+QENG: \"NR5G-NSA\",001,01,61,-96,24,-10,425090,1,4,0\r\n\r\nOK\r\n"
		lteW = "3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=001%s3C0D01A2B03"
	)
	sp := fmt.Sprintf
	cases := []struct {
		name, out, homeMCC string
		homeMNCLen         int
		want               string // "" = an error
	}{
		{"LTE", sp(lte, "01"), "001", 2, sp(lteW, "01")},
		{"LTE without the SIM", sp(lte, "01"), "", 0, sp(lteW, "01")},
		{"NR5G-SA FDD", saF, "001", 2, "3GPP-NR-FDD;utran-cell-id-3gpp=001013C0D121A2B3C4D5"},
		{"NR5G-SA TDD, short fields, space before the MCC", saT, "001", 2, "3GPP-NR-TDD;utran-cell-id-3gpp=001010004ED000000000"},
		{"NR5G-SA, 3-digit MNC", sa3, "001", 3, "3GPP-NR-TDD;utran-cell-id-3gpp=001001C94B00C9A82340B"},
		{"EN-DC: the LTE anchor", nsa, "001", 2, "3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=00101004100DF30C"},
		// MNC 1 / 01 / 001 (#12): padded to the SIM's length in the home
		// country, to 2 digits otherwise; never shortened.
		{"MNC 1, SIM 2 digits", sp(lte, "1"), "001", 2, sp(lteW, "01")},
		{"MNC 1, SIM 3 digits", sp(lte, "1"), "001", 3, sp(lteW, "001")},
		{"MNC 1, no SIM length", sp(lte, "1"), "001", 0, sp(lteW, "01")},
		{"MNC 1, roaming", sp(lte, "1"), "002", 3, sp(lteW, "01")},
		{"MNC 01, SIM 3 digits", sp(lte, "01"), "001", 3, sp(lteW, "001")},
		{"MNC 001, SIM 2 digits", sp(lte, "001"), "001", 2, sp(lteW, "001")},
		{"MNC 001, SIM 3 digits", sp(lte, "001"), "001", 3, sp(lteW, "001")},
		{"NR5G-SA, MNC 1, SIM 2 digits", strings.Replace(saF, "001,01,", "001,1,", 1), "001", 2,
			"3GPP-NR-FDD;utran-cell-id-3gpp=001013C0D121A2B3C4D5"},
		// Replies that cannot be parsed.
		{"no cell", "+QENG: \"servingcell\",\"SEARCH\"\r\nOK\r\n", "001", 2, ""},
		{"NR5G-NSA line alone", "+QENG: \"NR5G-NSA\",001,01,61,-96,24,-10,425090,1,4,0\r\nOK\r\n", "001", 2, ""},
		{"NR5G-SA cut short", "+QENG: \"servingcell\",\"NOCONN\",\"NR5G-SA\",\"FDD\",001,01,1A2B3C4D5,721\r\nOK\r\n", "001", 2, ""},
		{"NR5G-SA TAC over 6 hex", "+QENG: \"servingcell\",\"NOCONN\",\"NR5G-SA\",\"FDD\",001,01,1A2B3C4D5,721,3C0D123,127490,71\r\nOK\r\n", "001", 2, ""},
		{"NR5G-SA NCI over 9 hex", "+QENG: \"servingcell\",\"NOCONN\",\"NR5G-SA\",\"FDD\",001,01,11A2B3C4D5,721,3C0D12,127490,71\r\nOK\r\n", "001", 2, ""},
		{"NR5G-SA bad duplex", "+QENG: \"servingcell\",\"NOCONN\",\"NR5G-SA\",\"-\",001,01,1A2B3C4D5,721,3C0D12,127490,71\r\nOK\r\n", "001", 2, ""},
		{"MNC not digits", sp(lte, "-"), "001", 2, ""},
		{"MNC of 4 digits", sp(lte, "0001"), "001", 2, ""},
		{"no +QENG", "OK\r\n", "001", 2, ""},
	}
	for _, c := range cases {
		got, err := parseQENGANI(c.out, c.homeMCC, c.homeMNCLen)
		if c.want == "" {
			if err == nil {
				t.Errorf("%s: want an error, got %q", c.name, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: ani = %q, err = %v, want %q", c.name, got, err, c.want)
		}
	}
}

// TestResolveANI: a cell that cannot be read no longer stops the line (#7).
func TestResolveANI(t *testing.T) {
	reset := func() {
		lastANI.Lock()
		lastANI.v = ""
		lastANI.Unlock()
	}
	reset()
	t.Cleanup(reset)
	var log strings.Builder
	logger := slog.New(slog.NewTextHandler(&log, nil))
	bad := errors.New("no LTE or NR5G-SA entry")

	// No cell read yet: no header, and a warning.
	if got := resolveANI("", bad, logger); got != "" {
		t.Errorf("first failure: ani = %q, want none", got)
	}
	if !strings.Contains(log.String(), "registering without P-Access-Network-Info") {
		t.Errorf("no warning in the log:\n%s", log.String())
	}
	// A cell read is used and remembered.
	const nr = "3GPP-NR-FDD;utran-cell-id-3gpp=001013C0D121A2B3C4D5"
	if got := resolveANI(nr, nil, logger); got != nr {
		t.Errorf("success: ani = %q, want %q", got, nr)
	}
	// The next failure falls back to it.
	log.Reset()
	if got := resolveANI("", bad, logger); got != nr {
		t.Errorf("fallback: ani = %q, want %q", got, nr)
	}
	if !strings.Contains(log.String(), "using the last P-Access-Network-Info") {
		t.Errorf("no warning in the log:\n%s", log.String())
	}
	// The stack is built with what came out: no header when it is empty
	// (volte/reg and volte/call add it only when set).
	cfg := &Config{}
	if rc := regConfig(cfg, identities{}, "", netip.Addr{}, nil, "", logger); rc.PANI != "" {
		t.Errorf("reg.Config.PANI = %q, want none", rc.PANI)
	}
	if cc := callConfig(cfg, "", logger); cc.PANI != "" {
		t.Errorf("call.Config.PANI = %q, want none", cc.PANI)
	}
}
