(() => {
  if (window.__sfpanelAndroidEnhanced) return;
  window.__sfpanelAndroidEnhanced = true;
  document.documentElement.dataset.sfpanelAndroid = 'true';
  const style = document.createElement('style');
  style.textContent = `
    html[data-sfpanel-android] { --muted-foreground:#526278; --primary:#1264cc; }
    html.dark[data-sfpanel-android] { --muted-foreground:#b0bdcb; --primary:#75b5ff; --primary-foreground:#101923; }
    [data-sfpanel-android] :is(button,[role=button],[role=tab],[role=menuitem],[role=menuitemcheckbox],[role=menuitemradio],[role=option],input:not([type=checkbox]):not([type=radio]),select) { min-height:48px; }
    [data-sfpanel-android] :is(button,[role=button],[role=tab]) { min-width:48px; }
    [data-sfpanel-android] :is(input,textarea,select) { font-size:max(16px,1em); }
    [data-sfpanel-android] .xterm-helper-textarea { min-height:0 !important; font-size:inherit; }
    [data-sfpanel-android] :focus-visible { outline:3px solid var(--primary); outline-offset:2px; }
    [data-sfpanel-android] nav span { font-size:12px; }
    /* Prevent keyboard focus from scrolling the session frame itself. */
    [data-sfpanel-android] [data-ai-workspace] > .overflow-hidden { overflow:clip; }
    [data-sfpanel-android] [data-ai-workspace] { gap:0 !important; }
    [data-sfpanel-android]:not([data-ai-tools-open]) [data-ai-workspace] > :first-child { display:none; }
    [data-sfpanel-android] [data-ai-workspace] > .rounded-2xl { border-radius:0; }
    [data-sfpanel-android] [data-ai-workspace] [role=tablist] { padding-block:0; }
    @media(prefers-reduced-motion:reduce) { *,*::before,*::after { animation-duration:.01ms !important; transition-duration:.01ms !important; scroll-behavior:auto !important; } }
  `;
  document.head.append(style);
  // Keep object URLs alive long enough for Android's native Save dialog. The
  // server currently revokes them in the same stack as anchor.click().
  const revoke = URL.revokeObjectURL.bind(URL);
  URL.revokeObjectURL = url => setTimeout(() => revoke(url), 120000);
  document.addEventListener('click', event => {
    const link = event.target instanceof Element ? event.target.closest('a[download]') : null;
    if (link?.href.startsWith('blob:')) window.__sfpanelAndroidDownload = { url: link.href, name: link.download };
  }, true);
  // xterm's normal buffer has scrollback. Full-screen CLIs use an alternate
  // buffer and need terminal mouse-wheel (or xterm's arrow fallback) input.
  window.__sfpanelScrollTerminal = (el, pixels, x, y) => {
    const term = el?.__termRef?.current, screen = el?.querySelector('.xterm-screen');
    if (!term || !screen) return false;
    const cell = Math.max(1, screen.clientHeight / Math.max(1, term.rows));
    if (term.buffer.active.type === 'alternate' || (term.modes?.mouseTrackingMode && term.modes.mouseTrackingMode !== 'none')) {
      const rect = screen.getBoundingClientRect();
      screen.dispatchEvent(new WheelEvent('wheel', { bubbles:true, cancelable:true, deltaY:pixels,
        clientX:Math.max(rect.left + 1, Math.min(rect.right - 1, x ?? rect.left + rect.width / 2)),
        clientY:Math.max(rect.top + 1, Math.min(rect.bottom - 1, y ?? rect.top + rect.height / 2)) }));
    } else term.scrollLines(Math.trunc(pixels / cell));
    return true;
  };
  let drag = null;
  document.addEventListener('touchstart', event => {
    if (event.touches.length !== 1) { drag = null; return; }
    const el = event.target instanceof Element ? event.target.closest('[data-terminal-session="active"]') : null;
    if (!el?.__termRef?.current) return;
    const point = event.touches[0];
    drag = { el, startX:point.clientX, startY:point.clientY, y:point.clientY, remainder:0, moved:false };
  }, { capture:true, passive:true });
  document.addEventListener('touchmove', event => {
    if (!drag || event.touches.length !== 1 || !drag.el.isConnected) { drag = null; return; }
    const point = event.touches[0];
    if (!drag.moved && Math.abs(point.clientY - drag.startY) < 6) return;
    if (!drag.moved && Math.abs(point.clientX - drag.startX) > Math.abs(point.clientY - drag.startY)) { drag = null; return; }
    drag.moved = true;
    // Own the gesture before old server helpers and xterm can process it twice.
    event.preventDefault(); event.stopImmediatePropagation();
    drag.remainder += drag.y - point.clientY; drag.y = point.clientY;
    const term = drag.el.__termRef.current, screen = drag.el.querySelector('.xterm-screen');
    const cell = Math.max(1, (screen?.clientHeight || 17 * term.rows) / Math.max(1, term.rows));
    const lines = Math.trunc(drag.remainder / cell);
    if (lines) { window.__sfpanelScrollTerminal(drag.el, lines * cell, point.clientX, point.clientY); drag.remainder -= lines * cell; }
  }, { capture:true, passive:false });
  document.addEventListener('touchend', event => {
    if (drag?.moved) { event.preventDefault(); event.stopImmediatePropagation(); }
    drag = null;
  }, { capture:true, passive:false });
  document.addEventListener('touchcancel', () => { drag = null; }, { capture:true, passive:true });
  // Native key rows stay usable on older SFPanel releases as well. Hide the
  // duplicate web key bar, but leave every other navigation surface intact.
  const adapt = () => {
    // v0.73.0 predates the explicit marker. Compact only the AI overview
    // header when the keyboard leaves little height; session tabs stay visible.
    if (location.pathname === '/ai') document.querySelector('main > div')?.setAttribute('data-ai-workspace', '');
    document.querySelectorAll('[data-terminal-session]').forEach(el => {
      const term = el.__termRef?.current;
      if (term && term.options.screenReaderMode !== !!window.__sfpanelAndroidScreenReader) term.options.screenReaderMode = !!window.__sfpanelAndroidScreenReader;
    });
    document.querySelectorAll('[data-mobile-terminal-bar]').forEach(el => { el.style.display = 'none'; });
    document.querySelectorAll('button').forEach(el => {
      if (el.textContent.trim() !== 'Esc') return;
      const row = el.parentElement;
      const keys = [...row.querySelectorAll('button')].map(b => b.textContent.trim());
      if (keys.includes('Tab') && keys.includes('Ctrl') && row.parentElement?.classList.contains('md:hidden')) row.parentElement.style.display = 'none';
    });
  };
  let queued = false;
  new MutationObserver(records => {
    if (records.every(record => record.target instanceof Element && record.target.closest('.xterm'))) return;
    if (queued) return;
    queued = true;
    requestAnimationFrame(() => { queued = false; adapt(); });
  }).observe(document.body, { childList:true, subtree:true });
  adapt();
  setTimeout(adapt, 300);
})();
