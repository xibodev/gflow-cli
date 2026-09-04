(() => {
  if (globalThis.__FLOW_GO_CONTENT_LOADED__) return;
  globalThis.__FLOW_GO_CONTENT_LOADED__ = true;

  // Inject injected.js into the main page context
  const s = document.createElement('script');
  s.src = chrome.runtime.getURL('injected.js');
  s.onload = () => s.remove();
  (document.head || document.documentElement).appendChild(s);

  chrome.runtime.onMessage.addListener((msg, _, reply) => {
    if (msg.type !== 'GET_CAPTCHA') return;

    const { requestId, pageAction } = msg;

    const handler = (e) => {
      if (e.detail?.requestId === requestId) {
        window.removeEventListener('CAPTCHA_RESULT', handler);
        clearTimeout(timer);
        reply({ token: e.detail.token, error: e.detail.error });
      }
    };

    const timer = setTimeout(() => {
      window.removeEventListener('CAPTCHA_RESULT', handler);
      reply({ error: 'CAPTCHA_TIMEOUT' });
    }, 25000);

    window.addEventListener('CAPTCHA_RESULT', handler);

    window.dispatchEvent(new CustomEvent('GET_CAPTCHA', {
      detail: { requestId, pageAction },
    }));

    return true; // Keep message channel open for async response
  });
})();
