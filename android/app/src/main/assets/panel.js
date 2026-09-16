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
