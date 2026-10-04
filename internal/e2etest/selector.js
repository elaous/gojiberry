// Playwright-style selectors for the chromedp driver: CSS plus :has-text("x")
// (case-insensitive substring of the element's text) and :text-is("x") (the whole
// text, trimmed). Each custom pseudo-class is turned into an attribute that is set
// on the matching elements for the duration of the query, so it works anywhere in a
// selector, inside :not() too.
window.__e2e = window.__e2e || (() => {
  const norm = (s) => s.replace(/\s+/g, ' ').trim();
  const all = (sel, root) => {
    const marks = [];
    let i = 0;
    const css = sel.replace(/:(has-text|text-is)\("((?:[^"\\]|\\.)*)"\)/g, (_, kind, raw) => {
      const want = norm(raw.replace(/\\(.)/g, '$1'));
      const name = 'data-e2e-m' + i++;
      for (const el of (root || document).querySelectorAll('*')) {
        const t = norm(el.textContent || '');
        if (kind === 'has-text' ? t.toLowerCase().includes(want.toLowerCase()) : t === want) { el.setAttribute(name, ''); marks.push([el, name]); }
      }
      return '[' + name + ']';
    });
    try { return Array.from((root || document).querySelectorAll(css)); }
    finally { for (const [el, n] of marks) el.removeAttribute(n); }
  };
  const visible = (el) => {
    const r = el.getBoundingClientRect();
    const st = getComputedStyle(el);
    return r.width > 0 && r.height > 0 && st.visibility !== 'hidden' && st.display !== 'none';
  };
  return { all, visible, norm,
    first: (sel) => all(sel)[0] || null,
    // The centre of the first match, scrolled into view; null while none is visible.
    centre: (sel) => {
      const el = all(sel).find(visible) || null;
      if (!el) return null;
      const spot = () => {
        const r = el.getBoundingClientRect();
        return [r.x + r.width / 2, r.y + r.height / 2];
      };
      // What a pointer there would hit: the element, or something in it.
      const hit = ([x, y]) => { const e = document.elementFromPoint(x, y); return !!e && el.contains(e); };
      let c = spot();
      if (!hit(c)) { el.scrollIntoView({ block: 'center', inline: 'center' }); c = spot(); }
      return hit(c) ? c : null;
    },
  };
})();
