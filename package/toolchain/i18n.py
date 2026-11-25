#!/usr/bin/env python3
"""Translations of the MudiVoLTE interfaces.

One catalog per language, package/po/<lang>/mudi-volte.po, holds every text
the user sees: the LuCI page, the GL admin panel page, the command line tool
(setup included) and the errors of the panel's RPC module. The router's
screen app (MudiScreen) prints English and MudiScreen translates it with its
own catalogs: package/po/<lang>/mudi-screen-volte.tsv, in MudiScreen's format
(English<TAB>translation, {} matches any text), goes into the package as is. The template package/po/templates/mudi-volte.pot is made from the
sources by `extract`; `update` merges it into the catalogs; `check` fails on
untranslated texts or on placeholders that do not match; `build` writes the
files of the translation package for one language:

  usr/lib/lua/luci/i18n/mudi-volte.<lang>.lmo   LuCI (its own format, below)
  usr/share/mudi-volte/i18n/<lang>.json         GL panel page and RPC module
  usr/share/mudi-volte/i18n/<lang>.tsv          command line tool
  usr/share/mudi-screen/i18n/<lang>/volte.tsv   router's screen (MudiScreen)

Markers in the sources (the texts are English, the default):
  LuCI page (JavaScript)    _('text')           LuCI's own function
  GL panel page (JavaScript) T('text'), NT('text')  NT marks a text that is
                            translated later, where it is shown
  shell (mudi-volte)        T 'text' [ARG...], NT 'text'
  Lua (RPC module)          T("text", ...)
  menu JSON (LuCI)          "title": "text"
Texts are printf formats: %s and %d, in the same order in every language;
a literal percent sign is %%. A text is one line: no newline, tab or
backslash (shell and the TSV look texts up literally).

The .lmo writer follows po2lmo of LuCI (openwrt-23.05,
modules/luci-base/src/po2lmo.c and lib/lmo.c): for each translated text the
value is written padded to 4 bytes, then the index of (key hash, plural
count, offset, length) entries sorted by key hash, then the offset of the
index; all integers big-endian, hashes are Paul Hsieh's SuperFastHash of the
text with its length as the seed. LuCI looks texts up by the hash of the text
with whitespace runs collapsed, so LuCI texts must not hold such runs
(`extract` checks).

Usage:
  i18n.py extract                 sources → package/po/templates/mudi-volte.pot
  i18n.py update                  .pot → every package/po/<lang>/mudi-volte.po
  i18n.py check [LANG...]         every text translated, placeholders match
  i18n.py build LANG DESTDIR      the translation package's files
"""

import json
import os
import re
import struct
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
PKG = os.path.dirname(HERE)
FILES = os.path.join(PKG, 'files')
PO_DIR = os.path.join(PKG, 'po')
POT = os.path.join(PO_DIR, 'templates', 'mudi-volte.pot')
DOMAIN = 'mudi-volte'

# The sources and how their texts are marked.
JS_STR = r"""('(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*")"""
SOURCES = [
    ('www/luci-static/resources/view/mudi-volte/overview.js', 'luci', [r'(?<![\w.$])_\(\s*' + JS_STR]),
    ('usr/share/luci/menu.d/luci-app-mudi-volte.json', 'luci', [r'"title"\s*:\s*("(?:[^"\\]|\\.)*")']),
    ('www/views/gl-sdk4-ui-mudi-volte.common.js', 'gl', [r'(?<![\w.$])N?T\(\s*' + JS_STR]),
    ('usr/lib/oui-httpd/rpc/mudi-volte', 'lua', [r'(?<![\w.:])T\(\s*' + JS_STR]),
    ('usr/sbin/mudi-volte', 'sh', [r"(?:^|[\s;(|&`]|\$\()N?T\s+('[^'\n]*'|\"(?:[^\"\\$`\n]|\\.)*\")"]),
]

JS_ESC = {'n': '\n', 't': '\t', 'r': '\r', "'": "'", '"': '"', '\\': '\\', '0': '\0', 'b': '\b', 'f': '\f', 'v': '\v'}


def unquote(lit, kind):
    """The value of a quoted literal of the source language."""
    q, body = lit[0], lit[1:-1]
    if kind == 'sh':
        if q == "'":
            return body
        return re.sub(r'\\([$`"\\])', r'\1', body)
    out, i = [], 0
    while i < len(body):
        c = body[i]
        if c == '\\' and i + 1 < len(body):
            n = body[i + 1]
            if n == 'u' and kind != 'lua':
                out.append(chr(int(body[i + 2:i + 6], 16)))
                i += 6
                continue
            out.append(JS_ESC.get(n, n))
            i += 2
            continue
        out.append(c)
        i += 1
    return ''.join(out)


def po_quote(s):
    return '"' + s.replace('\\', '\\\\').replace('"', '\\"').replace('\n', '\\n').replace('\t', '\\t') + '"'


def po_unquote(s):
    s = s.strip()
    assert s[0] == '"' and s[-1] == '"', s
    return re.sub(r'\\(.)', lambda m: {'n': '\n', 't': '\t'}.get(m.group(1), m.group(1)), s[1:-1])


PLACEHOLDER = re.compile(r'%[-+ 0#]*\d*(?:\.\d+)?[sdifxXc%]')


def placeholders(s):
    return [p[-1] for p in PLACEHOLDER.findall(s) if p != '%%']


def extract():
    texts = {}  # msgid -> [references]
    errors = []
    for rel, kind, patterns in SOURCES:
        path = os.path.join(FILES, rel)
        with open(path, encoding='utf-8') as f:
            lines = f.read().split('\n')
        for no, line in enumerate(lines, 1):
            stripped = line.lstrip()
            if kind == 'sh' and stripped.startswith('#'):
                continue
            if kind in ('gl', 'luci') and stripped.startswith('//'):
                continue
            if kind == 'lua' and stripped.startswith('--'):
                continue
            for pat in patterns:
                for m in re.finditer(pat, line):
                    s = unquote(m.group(1), kind)
                    where = '%s:%d' % (rel, no)
                    if not s.strip():
                        continue
                    if re.search(r'[\n\t\\]', s):
                        errors.append('%s: newline, tab or backslash in %r' % (where, s))
                    if kind == 'luci' and s != re.sub(r'\s+', ' ', s).strip():
                        errors.append('%s: LuCI looks texts up with whitespace collapsed: %r' % (where, s))
                    texts.setdefault(s, []).append(where)
    if errors:
        sys.exit('\n'.join(errors))
    os.makedirs(os.path.dirname(POT), exist_ok=True)
    with open(POT, 'w', encoding='utf-8', newline='\n') as f:
        f.write('msgid ""\nmsgstr "Content-Type: text/plain; charset=UTF-8\\n"\n')
        for s in sorted(texts, key=lambda k: (texts[k][0].split(':')[0], int(texts[k][0].split(':')[1]))):
            f.write('\n#: %s\nmsgid %s\nmsgstr ""\n' % (' '.join(texts[s]), po_quote(s)))
    print('%s: %d texts' % (os.path.relpath(POT, PKG), len(texts)))


def read_po(path):
    """[(refs, msgid, msgstr, fuzzy)] without the header."""
    entries, refs, flags, cur, field = [], [], '', {}, None
    with open(path, encoding='utf-8') as f:
        lines = f.read().split('\n') + ['']
    for line in lines:
        if line.startswith('#:'):
            refs += line[2:].split()
        elif line.startswith('#,'):
            flags += line[2:]
        elif line.startswith('msgid '):
            cur, field = {'msgid': po_unquote(line[6:])}, 'msgid'
        elif line.startswith('msgstr '):
            cur['msgstr'], field = po_unquote(line[7:]), 'msgstr'
        elif line.startswith('"') and field:
            cur[field] += po_unquote(line)
        elif not line.strip():
            if cur.get('msgid'):
                entries.append((refs, cur['msgid'], cur.get('msgstr', ''), 'fuzzy' in flags))
            refs, flags, cur, field = [], '', {}, None
    return entries


def catalogs():
    if not os.path.isdir(PO_DIR):
        return []
    return sorted(d for d in os.listdir(PO_DIR)
                  if d != 'templates' and os.path.isfile(os.path.join(PO_DIR, d, DOMAIN + '.po')))


def write_po(path, lang, entries):
    with open(path, 'w', encoding='utf-8', newline='\n') as f:
        f.write('msgid ""\nmsgstr ""\n"Content-Type: text/plain; charset=UTF-8\\n"\n"Language: %s\\n"\n' % lang)
        for refs, msgid, msgstr, fuzzy in entries:
            f.write('\n')
            if refs:
                f.write('#: %s\n' % ' '.join(refs))
            if fuzzy:
                f.write('#, fuzzy\n')
            f.write('msgid %s\nmsgstr %s\n' % (po_quote(msgid), po_quote(msgstr)))


def update():
    template = read_po(POT)
    for lang in catalogs():
        path = os.path.join(PO_DIR, lang, DOMAIN + '.po')
        old = {e[1]: e for e in read_po(path)}
        new = [(refs, msgid, old[msgid][2] if msgid in old else '', old[msgid][3] if msgid in old else False)
               for refs, msgid, _, _ in template]
        dropped = [m for m in old if m not in {e[1] for e in template}]
        write_po(path, lang, new)
        print('%s: %d texts, %d untranslated, %d dropped' % (
            lang, len(new), sum(1 for e in new if not e[2]), len(dropped)))


def check(langs):
    template = {e[1] for e in read_po(POT)}
    bad = []
    for lang in langs or catalogs():
        for refs, msgid, msgstr, fuzzy in read_po(os.path.join(PO_DIR, lang, DOMAIN + '.po')):
            where = '%s %s' % (lang, refs[0] if refs else '')
            if msgid not in template:
                bad.append('%s: not in the template (run update): %r' % (where, msgid))
            elif not msgstr or fuzzy:
                bad.append('%s: untranslated: %r' % (where, msgid))
            elif placeholders(msgid) != placeholders(msgstr):
                bad.append('%s: placeholders differ: %r → %r' % (where, msgid, msgstr))
            elif re.search(r'[\n\t\\]', msgstr):
                bad.append('%s: newline, tab or backslash: %r' % (where, msgstr))
        missing = template - {e[1] for e in read_po(os.path.join(PO_DIR, lang, DOMAIN + '.po'))}
        bad += ['%s: missing (run update): %r' % (lang, m) for m in sorted(missing)]
    if bad:
        sys.exit('\n'.join(bad))
    print('ok')


def sfh_hash(data, init):
    """SuperFastHash as in LuCI's lib/lmo.c (little-endian 16-bit reads)."""
    M = 0xffffffff
    h = init & M
    n = len(data)
    if n == 0:
        return 0
    rem, i = n & 3, 0

    def g16(k):
        return data[k] | (data[k + 1] << 8)

    def sc(b):  # (signed char)
        return b - 256 if b > 127 else b
    for _ in range(n >> 2):
        h = (h + g16(i)) & M
        tmp = ((g16(i + 2) << 11) ^ h) & M
        h = ((h << 16) ^ tmp) & M
        i += 4
        h = (h + (h >> 11)) & M
    if rem == 3:
        h = (h + g16(i)) & M
        h ^= (h << 16) & M
        h ^= (sc(data[i + 2]) << 18) & M
        h = (h + (h >> 11)) & M
    elif rem == 2:
        h = (h + g16(i)) & M
        h ^= (h << 11) & M
        h = (h + (h >> 17)) & M
    elif rem == 1:
        h = (h + sc(data[i])) & M
        h ^= (h << 10) & M
        h = (h + (h >> 1)) & M
    h ^= (h << 3) & M
    h = (h + (h >> 5)) & M
    h ^= (h << 4) & M
    h = (h + (h >> 17)) & M
    h ^= (h << 25) & M
    h = (h + (h >> 6)) & M
    return h


def lmo(pairs):
    data, index = bytearray(), []
    for msgid, msgstr in pairs:
        k, v = msgid.encode('utf-8'), msgstr.encode('utf-8')
        key_id, val_id = sfh_hash(k, len(k)), sfh_hash(v, len(v))
        if key_id == val_id:  # same text: po2lmo leaves it out
            continue
        index.append((key_id, 1, len(data), len(v)))
        data += v + b'\0' * ((4 - len(v) % 4) % 4)
    if not index:
        return b''
    index.sort(key=lambda e: e[0])
    out = bytes(data) + b''.join(struct.pack('>IIII', *e) for e in index)
    return out + struct.pack('>I', len(data))


def build(lang, dest):
    entries = [(m, s) for refs, m, s, fuzzy in read_po(os.path.join(PO_DIR, lang, DOMAIN + '.po'))
               if s and not fuzzy]
    entries.sort()
    luci = set()
    for rel, kind, _ in SOURCES:
        if kind == 'luci':
            luci.add(rel)
    luci_ids = {e[1] for e in read_po(POT) if any(r.split(':')[0] in luci for r in e[0])}
    out = {
        'usr/lib/lua/luci/i18n/%s.%s.lmo' % (DOMAIN, lang): lmo([e for e in entries if e[0] in luci_ids]),
        'usr/share/mudi-volte/i18n/%s.json' % lang:
            (json.dumps(dict(entries), ensure_ascii=False, sort_keys=True, indent=0) + '\n').encode('utf-8'),
        'usr/share/mudi-volte/i18n/%s.tsv' % lang:
            ''.join('%s\t%s\n' % e for e in entries).encode('utf-8'),
    }
    screen = os.path.join(PO_DIR, lang, 'mudi-screen-volte.tsv')
    if os.path.isfile(screen):
        with open(screen, 'rb') as f:
            out['usr/share/mudi-screen/i18n/%s/volte.tsv' % lang] = f.read().replace(b'\r\n', b'\n')
    for rel, content in out.items():
        path = os.path.join(dest, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, 'wb') as f:
            f.write(content)
        print('%s: %d bytes' % (rel, len(content)))


def main(argv):
    if len(argv) >= 1 and argv[0] == 'extract':
        extract()
    elif len(argv) >= 1 and argv[0] == 'update':
        update()
    elif len(argv) >= 1 and argv[0] == 'check':
        check(argv[1:])
    elif len(argv) == 3 and argv[0] == 'build':
        build(argv[1], argv[2])
    else:
        sys.exit(__doc__)


if __name__ == '__main__':
    main(sys.argv[1:])
