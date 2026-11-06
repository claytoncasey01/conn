// Paints the last applied look before the app loads: no network, no writes, never throws. Keys and shapes
// mirror src/lib/theme/storage.ts (a test holds them together); 'default' is DEFAULT_ID.
// It also paints the cached theme stylesheet's text and links the fonts (the loader adopts them by id). Everything read is
// untrusted: the stylesheet is painted only beside an address of exactly the CSS_HREF shape naming this theme, and only up to
// SHEET_CHARS_MAX characters; a link is made only for an href of exactly the FONTS_HREF shape.
// A file, not an inline script, so the page's Content-Security-Policy allows no inline script. Loaded at the top of the body so
// what it adds lands after the build's app stylesheet (Neon wins ties with the calm base by coming later).
(function () {
  var ID = /^[a-z0-9][a-z0-9-]{0,39}$/, MODES = ['light', 'dark', 'system'], q = {}, c = null, e = null, id = 'default', mode = 'system'
  var CSS = /^\/api\/themes\/[a-z0-9-]+\/theme\.css\?v=[0-9a-f]{64}$/
  var FONTS = /^https:\/\/fonts\.googleapis\.com\/css2\?family=(?:Chakra\+Petch|JetBrains\+Mono|IBM\+Plex\+Sans|IBM\+Plex\+Mono|Share\+Tech\+Mono):wght@[0-9;]{1,40}(&family=(?:Chakra\+Petch|JetBrains\+Mono|IBM\+Plex\+Sans|IBM\+Plex\+Mono|Share\+Tech\+Mono):wght@[0-9;]{1,40}){0,7}&display=swap$/
  var A = { motion: ['calm', 'power'], led: ['circle', 'diamond', 'square'], glass: ['on', 'off'] }
  function get(k) { try { return JSON.parse(localStorage.getItem(k)) } catch (x) { return null } }
  function link(i, h) { var l = document.createElement('link'); l.id = i; l.rel = 'stylesheet'; l.href = h; document.head.appendChild(l); return l }
  function ok(v, list) { return typeof v === 'string' && list.indexOf(v) >= 0 }
  try { location.search.slice(1).split('&').forEach(function (p) { var kv = p.split('='); q[kv[0]] = kv[1] }) } catch (x) {}
  c = get('conn.theme') || {}
  if (typeof c.id === 'string' && ID.test(c.id)) id = c.id
  if (ok(c.mode, MODES)) mode = c.mode
  if (typeof q.theme === 'string' && ID.test(q.theme)) id = q.theme
  if (ok(q.mode, MODES)) mode = q.mode
  e = get('conn.theme.css.' + id)
  var a = e && e.attrs
  if (!(a && typeof e.css === 'string' && !/url\(|@import|expression/i.test(e.css) && ok(a.motion, A.motion) && ok(a.led, A.led) && ok(a.glass, A.glass) && (e.only === null || ok(e.only, ['light', 'dark'])))) e = null
  try {
    var dark = false, less = false, root = document.documentElement
    try { dark = matchMedia('(prefers-color-scheme: dark)').matches; less = matchMedia('(prefers-reduced-motion: reduce)').matches } catch (x) {}
    root.setAttribute('data-theme', id)
    root.setAttribute('data-mode', (e && e.only) || (mode === 'system' ? (dark ? 'dark' : 'light') : mode))
    root.setAttribute('data-motion', e ? a.motion : 'calm')
    root.setAttribute('data-led', e ? a.led : 'circle')
    root.setAttribute('data-glass', e ? a.glass : 'off'); root.setAttribute('data-fx', c.effects === 'on' || (c.effects !== 'off' && !less) ? 'on' : 'off')
    if (e) { var st = document.createElement('style'); st.id = 'conn-theme'; st.textContent = e.css; document.head.appendChild(st) }
    if (e && typeof e.fontsUrl === 'string' && FONTS.test(e.fontsUrl)) {
      var f = link('conn-theme-fonts', e.fontsUrl)
      f.media = 'print'; f.onload = function () { f.media = 'all' } // fonts swap in when they arrive: first paint never waits on Google
    }
    if (e && typeof e.cssUrl === 'string' && e.cssUrl.indexOf('/api/themes/' + id + '/') === 0 && CSS.test(e.cssUrl) && typeof e.sheet === 'string' && e.sheet.length <= 262144) {
      var sh = document.createElement('style'); sh.id = 'conn-theme-css'; sh.textContent = e.sheet; document.head.appendChild(sh)
    }
  } catch (x) {}
})()
