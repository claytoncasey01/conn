"""Build 'Conn Matrix Code' from M+ 1 Code (SIL OFL 1.1): half-width katakana mirrored, plus upright digits.

usage: python build.py MPLUS1Code[wght].ttf out.woff2
The kana are the full-width outlines, squeezed to X_SCALE and mapped to U+FF66..U+FF9D.
"""
import io, sys, unicodedata
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer
from fontTools.subset import Subsetter, Options
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.pens.transformPen import TransformPen

WGHT, X_SCALE = 300, 0.75
src, out = sys.argv[1], sys.argv[2]

inst = instancer.instantiateVariableFont(TTFont(src), {'wght': WGHT})
buf = io.BytesIO(); inst.save(buf); data = buf.getvalue()
orig, work = TTFont(io.BytesIO(data)), TTFont(io.BytesIO(data))  # orig is never edited, so composites are read unflipped

kana = [(cp, unicodedata.normalize('NFKC', chr(cp))) for cp in range(0xFF66, 0xFF9E)]
assert all(len(full) == 1 for _, full in kana)
digits = list('0123456789')

opts = Options()
opts.layout_features = []
opts.name_IDs = [0, 1, 2, 3, 4, 5, 6, 13, 14]
opts.notdef_outline = True
opts.drop_tables += ['GPOS', 'GSUB', 'GDEF', 'BASE', 'STAT', 'MVAR', 'HVAR', 'avar', 'fvar', 'gvar', 'DSIG']
sub = Subsetter(opts); sub.populate(text=''.join(full for _, full in kana) + ''.join(digits)); sub.subset(work)

ocmap, wcmap = orig.getBestCmap(), work.getBestCmap()
gs, glyf, hmtx = orig.getGlyphSet(), work['glyf'], work['hmtx']
kana_glyphs = {wcmap[ord(full)] for _, full in kana}
for name in work.getGlyphOrder():
    if name == '.notdef':
        continue
    adv = hmtx[name][0]
    mirror = name in kana_glyphs
    m = (-X_SCALE, 0, 0, 1, adv * X_SCALE, 0) if mirror else (1, 0, 0, 1, 0, 0)
    pen = TTGlyphPen(gs)
    gs[name].draw(TransformPen(pen, m))  # draws composites decomposed
    g = pen.glyph(); glyf[name] = g; g.recalcBounds(glyf)
    nadv = round(adv * X_SCALE) if mirror else adv
    hmtx[name] = (nadv, getattr(g, 'xMin', 0))

for t in work['cmap'].tables:
    t.cmap = {cp: n for cp, n in t.cmap.items() if chr(cp) in digits}
    for cp, full in kana:
        t.cmap[cp] = wcmap[ord(full)]
# second pass drops glyphs nothing maps to (components of the old composites)
sub2 = Subsetter(opts); sub2.populate(unicodes=[cp for cp, _ in kana] + [ord(d) for d in digits]); sub2.subset(work)

for rec in list(work['name'].names):
    if rec.nameID == 1: rec.string = 'Conn Matrix Code'
    elif rec.nameID == 4: rec.string = 'Conn Matrix Code'
    elif rec.nameID == 6: rec.string = 'ConnMatrixCode-Regular'
    elif rec.nameID == 3: rec.string = 'ConnMatrixCode-1.0'
work['name'].names = [r for r in work['name'].names if r.nameID not in (16, 17, 25)]
ids = sorted({r.nameID for r in work['name'].names})
assert {0, 13, 14} <= set(ids), ids  # copyright and license notices survive
work.flavor = 'woff2'; work.save(out)
print('saved', out, 'glyphs', len(work.getGlyphOrder()), 'name ids', ids)
