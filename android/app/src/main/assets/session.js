(function (restore) {
  if (window !== window.top) return;
  const storage = window.sessionStorage;
  const set = Storage.prototype.setItem;
  const remove = Storage.prototype.removeItem;
  const clear = Storage.prototype.clear;
  // Runs before the panel constructs its API client or checks authentication.
  if (!storage.getItem('token') && restore?.token) {
    set.call(storage, 'token', restore.token);
    if (restore.refresh_token) set.call(storage, 'refresh_token', restore.refresh_token);
  }
  let pending = false;
  const publish = () => {
    pending = false;
    window.sfpanelSessionState.postMessage(JSON.stringify({
      token: storage.getItem('token'), refresh_token: storage.getItem('refresh_token')
    }));
  };
  const changed = () => {
    // A login/refresh writes the pair in one JS stack. Persist the pair together.
    if (!pending) { pending = true; queueMicrotask(publish); }
  };
  Storage.prototype.setItem = function (key, value) {
    set.call(this, key, value);
    if (this === storage && (String(key) === 'token' || String(key) === 'refresh_token')) changed();
  };
  Storage.prototype.removeItem = function (key) {
    remove.call(this, key);
    if (this === storage && (String(key) === 'token' || String(key) === 'refresh_token')) changed();
  };
  Storage.prototype.clear = function () { clear.call(this); if (this === storage) changed(); };
  window.addEventListener('pagehide', publish);
  publish();
})(__SFPANEL_SESSION__);
