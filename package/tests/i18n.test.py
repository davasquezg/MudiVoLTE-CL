#!/usr/bin/env python3
"""Tests of package/toolchain/i18n.py: the check of the screen app's catalog
(package/po/<lang>/mudi-screen-volte.tsv) against the texts of app.sh.

  python3 package/tests/i18n.test.py
"""

import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('i18n', ROOT / 'package' / 'toolchain' / 'i18n.py')
i18n = importlib.util.module_from_spec(spec)
spec.loader.exec_module(i18n)

REAL_FILES, REAL_PO = i18n.FILES, i18n.PO_DIR


class ScreenTexts(unittest.TestCase):
    def test_app_texts(self):
        texts = i18n.screen_texts()
        for want in ['Calls are on', 'online', 'missed: {}', 'waiting for the modem and LTE', 'under a minute',
                     '{} d {} h', '{} min', 'In a call · {}', 'Missed calls ({})', 'hidden number', 'none',
                     'Clear the missed calls?', 'Clear', 'Cleared', 'Incoming from', 'Talking']:
            self.assertIn(want, texts)
        # Variables alone, names, colours, keys and what goes to stderr are no texts.
        for not_text in ['{}', '{} ({})', 'VoLTE · {}', 'green', 'dim', 'section', 'phone',
                         'unknown action: {}', 'usage: {} status | action clear']:
            self.assertNotIn(not_text, texts)

    def test_real_catalogs_cover_the_app(self):
        for lang in i18n.catalogs():
            self.assertEqual(i18n.check_screen(lang), [], lang)


class CheckScreen(unittest.TestCase):
    """check_screen on a made-up app.sh and catalog."""

    APP = '\n'.join([
        '#!/bin/sh',
        '# json_add_string title "A comment"',
        'ago() {',
        '\tif [ "$1" -lt 60 ]; then echo "under a minute"; else echo "$(($1 / 60)) min"; fi',
        '}',
        'title="Calls are on" color=green state=online',
        'peer=${cpeer:-hidden number}',
        'json_add_string title "$title"',
        'json_add_string color "$color"',
        'info "Status" "$state" "$color"',
        'info "Caller" "$peer"',
        'json_add_string label "Missed calls ($missed)"',
        'echo "unknown action: $2" >&2',
        '',
    ])

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        base = Path(self.tmp.name)
        app = base / 'files' / i18n.SCREEN_APP
        app.parent.mkdir(parents=True)
        app.write_text(self.APP, encoding='utf-8', newline='\n')
        (base / 'po' / 'xx').mkdir(parents=True)
        i18n.FILES, i18n.PO_DIR = str(base / 'files'), str(base / 'po')
        self.tsv = base / 'po' / 'xx' / 'mudi-screen-volte.tsv'

    def tearDown(self):
        i18n.FILES, i18n.PO_DIR = REAL_FILES, REAL_PO
        self.tmp.cleanup()

    def write(self, *lines):
        self.tsv.write_text('# a comment\r\n' + ''.join(l + '\r\n' for l in lines), encoding='utf-8', newline='')

    def test_texts(self):
        self.assertEqual(sorted(i18n.screen_texts()), sorted([
            'under a minute', '{} min', 'Calls are on', 'online', 'hidden number', 'Status', 'Caller',
            'Missed calls ({})']))

    def test_complete(self):
        # {} in the English column matches any text, "{} min" among them.
        self.write('under a minute\tx', '{} min\tx {}', 'Calls are on\tx', 'online\tx', 'hidden number\tx',
                   'Status\tx', 'Caller\tx', 'Missed calls ({})\tx ({})', 'Not printed\tx')
        self.assertEqual(i18n.check_screen('xx'), [])

    def test_pattern_covers(self):
        self.write('{}\tx')
        self.assertEqual(i18n.check_screen('xx'), [])

    def test_uncovered(self):
        self.write('under a minute\tx', '{} min\tx {}', 'Calls are on\tx', 'hidden number\tx',
                   'Status\tx', 'Caller\tx', 'Missed calls\tx')
        bad = i18n.check_screen('xx')
        self.assertEqual(len(bad), 2, bad)
        self.assertIn("'online'", bad[0])
        self.assertIn(i18n.SCREEN_APP + ':6', bad[0])
        self.assertIn("'Missed calls ({})'", bad[1])

    def test_bad_line(self):
        self.write('{}\tx', 'no tab here', 'empty\t')
        bad = i18n.check_screen('xx')
        self.assertEqual(len(bad), 2, bad)
        self.assertIn('mudi-screen-volte.tsv:3', bad[0])
        self.assertIn('mudi-screen-volte.tsv:4', bad[1])

    def test_missing_catalog(self):
        bad = i18n.check_screen('xx')
        self.assertEqual(len(bad), 1)
        self.assertIn('missing', bad[0])


if __name__ == '__main__':
    unittest.main()
