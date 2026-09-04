(() => {
  if (window.__FLOW_GO_INJECTED__) return;
  window.__FLOW_GO_INJECTED__ = true;

  const SITE_KEY = '6LdsFiUsAAAAAIjVDZcuLhaHiDn5nnHVXVRQGeMV';

  function waitForGrecaptcha(timeout = 10000) {
    return new Promise((resolve, reject) => {
      const start = Date.now();
      const check = () => {
        if (window.grecaptcha?.enterprise?.execute) return resolve();
        if (Date.now() - start > timeout) return reject(new Error('grecaptcha not available'));
        setTimeout(check, 200);
      };
      check();
    });
  }

  window.addEventListener('GET_CAPTCHA', async ({ detail }) => {
    const { requestId, pageAction } = detail || {};
    try {
      await waitForGrecaptcha();
      const token = await window.grecaptcha.enterprise.execute(SITE_KEY, {
        action: pageAction || 'IMAGE_GENERATION',
      });
      window.dispatchEvent(new CustomEvent('CAPTCHA_RESULT', {
        detail: { requestId, token },
      }));
    } catch (e) {
      window.dispatchEvent(new CustomEvent('CAPTCHA_RESULT', {
        detail: { requestId, error: e.message || 'execute failed' },
      }));
    }
  });
})();
