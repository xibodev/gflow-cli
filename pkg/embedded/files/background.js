/**
 * Flow Bridge (Go) — Chrome Extension Background Service Worker
 */

const SERVER_BASE = 'http://127.0.0.1:8001';
const WS_URL = 'ws://127.0.0.1:8001/ws';
const FLOW_URLS = [
  'https://flow.google.com/*',
  'https://*.flow.google.com/*',
  'https://flow.google/*',
  'https://*.flow.google/*',
  'https://labs.google/fx/tools/flow*',
  'https://labs.google/fx/*/tools/flow*',
];
const DEFAULT_FLOW_URL = 'https://flow.google.com';

let flowKey = null;
let callbackSecret = null;
let callbackUrl = `${SERVER_BASE}/api/ext/callback`;
let clientId = 'flow-client-1';
let ws = null;
let pollTimer = null;
let pollIntervalMs = 1000;
let isConnected = false;

// Generate or load client ID
chrome.storage.local.get(['clientId', 'flowKey', 'callbackSecret'], (data) => {
  if (data.clientId) {
    clientId = data.clientId;
  } else {
    clientId = 'client_' + Math.random().toString(36).substring(2, 9);
    chrome.storage.local.set({ clientId });
  }
  if (data.flowKey) flowKey = data.flowKey;
  if (data.callbackSecret) callbackSecret = data.callbackSecret;

  initConnection();
});

// ─── Token Capture via webRequest ───────────────────────────
chrome.webRequest.onBeforeSendHeaders.addListener(
  (details) => {
    if (!details?.requestHeaders?.length) return;
    const authHeader = details.requestHeaders.find(
      (h) => h.name?.toLowerCase() === 'authorization'
    );
    const value = authHeader?.value || '';
    if (!value.startsWith('Bearer ya29.')) return;

    const token = value.replace(/^Bearer\s+/i, '').trim();
    if (!token) return;

    flowKey = token;
    chrome.storage.local.set({ flowKey });
    console.log('[Flow Bridge] Bearer token captured successfully');

    // Notify backend
    sendCallback({
      type: 'token_captured',
      session_id: clientId,
      flowKey: token,
    });
  },
  { urls: [
    'https://aisandbox-pa.googleapis.com/*',
    'https://labs.google/*',
    'https://flow.google.com/*',
    'https://*.flow.google.com/*',
    'https://flow.google/*',
    'https://*.flow.google/*',
  ] },
  ['requestHeaders', 'extraHeaders']
);

// ─── Connection Lifecycle ───────────────────────────────────
function initConnection() {
  connectHttp();
  connectWebSocket();
}

chrome.alarms.create('keepAlive', { periodInMinutes: 0.5 });
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === 'keepAlive') {
    if (!isConnected) {
      initConnection();
    }
  }
});

// HTTP Registration and Polling
async function connectHttp() {
  try {
    const res = await fetch(`${SERVER_BASE}/api/ext/hello`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        type: 'hello',
        session_id: clientId,
        clientId: clientId,
        flowKey: flowKey,
        flowKeyPresent: !!flowKey,
        extension_version: chrome.runtime.getManifest().version,
      }),
    });

    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();
    callbackSecret = data.secret;
    if (data.callback_url) callbackUrl = new URL(data.callback_url, SERVER_BASE).toString();
    pollIntervalMs = Math.max(250, Number(data.poll_interval_ms) || 1000);
    isConnected = true;

    chrome.storage.local.set({ callbackSecret });
    startPolling();
  } catch (err) {
    console.warn('[Flow Bridge] HTTP connect failed:', err.message);
  }
}

function startPolling() {
  if (pollTimer) clearTimeout(pollTimer);
  pollTimer = setTimeout(pollCommands, pollIntervalMs);
}

async function pollCommands() {
  try {
    const res = await fetch(`${SERVER_BASE}/api/ext/poll?session_id=${encodeURIComponent(clientId)}`, {
      headers: callbackSecret ? { Authorization: `Bearer ${callbackSecret}` } : {},
    });
    if (res.ok) {
      const data = await res.json();
      const commands = data.commands || [];
      for (const cmd of commands) {
        handleCommand(cmd);
      }
    }
  } catch (err) {
    // transient network error
  }
  startPolling();
}

// WebSocket Connection (low latency fallback/channel)
function connectWebSocket() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }
  try {
    ws = new WebSocket(WS_URL);
    ws.onopen = () => {
      console.log('[Flow Bridge] WebSocket connected');
      isConnected = true;
      ws.send(JSON.stringify({
        type: 'extension_ready',
        clientId: clientId,
        flowKeyPresent: !!flowKey,
      }));
      if (flowKey) {
        ws.send(JSON.stringify({
          type: 'token_captured',
          clientId: clientId,
          flowKey: flowKey,
        }));
      }
    };
    ws.onmessage = ({ data }) => {
      try {
        const cmd = JSON.parse(data);
        handleCommand(cmd);
      } catch (e) {
        console.error('[Flow Bridge] WS message parse error:', e);
      }
    };
    ws.onclose = () => {
      ws = null;
    };
    ws.onerror = () => {
      ws = null;
    };
  } catch (e) {
    ws = null;
  }
}

// ─── Dispatch & Command Handling ────────────────────────────
async function handleCommand(cmd) {
  if (!cmd || !cmd.method) return;
  const { id, method, params } = cmd;

  if (method === 'api_request') {
    await handleApiRequest(id, params);
  } else if (method === 'solve_captcha') {
    const result = await solveCaptcha(id, params?.captchaAction || 'IMAGE_GENERATION');
    sendCallback({ id, result });
  } else if (method === 'get_media_url') {
    await handleGetMediaUrl(id, params);
  } else if (method === 'open_flow_tab') {
    await openFlowTab();
    sendCallback({ id, status: 200, result: { opened: true } });
  }
}

async function handleApiRequest(id, params) {
  const { url, method = 'POST', headers = {}, body, captchaAction } = params || {};
  if (!url) {
    sendCallback({ id, status: 400, error: 'MISSING_URL' });
    return;
  }

  let finalBody = body;

  // Step 1: Solve reCAPTCHA if action requested
  if (captchaAction) {
    const capRes = await solveCaptcha(id, captchaAction);
    const token = capRes?.token;
    if (!token) {
      sendCallback({
        id,
        status: 403,
        error: `CAPTCHA_FAILED: ${capRes?.error || 'No token returned'}`,
      });
      return;
    }

    if (finalBody) {
      finalBody = JSON.parse(JSON.stringify(finalBody));
      if (finalBody.clientContext?.recaptchaContext) {
        finalBody.clientContext.recaptchaContext.token = token;
      }
      if (Array.isArray(finalBody.requests)) {
        for (const req of finalBody.requests) {
          if (req.clientContext?.recaptchaContext) {
            req.clientContext.recaptchaContext.token = token;
          }
        }
      }
    }
  }

  // Step 2: Set auth headers
  const fetchHeaders = {
    'accept': '*/*',
    'content-type': 'text/plain;charset=UTF-8',
    'origin': 'https://labs.google',
    'referer': 'https://labs.google/',
    ...headers,
  };

  if (flowKey) {
    fetchHeaders['authorization'] = `Bearer ${flowKey}`;
  }

  // Step 3: Run fetch in browser session
  try {
    const res = await fetch(url, {
      method,
      headers: fetchHeaders,
      credentials: 'include',
      body: method === 'GET' ? undefined : (typeof finalBody === 'string' ? finalBody : JSON.stringify(finalBody)),
    });

    const text = await res.text();
    let data;
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }

    sendCallback({
      id,
      status: res.status,
      data,
    });
  } catch (err) {
    sendCallback({
      id,
      status: 500,
      error: err.message || 'FETCH_ERROR',
    });
  }
}

async function handleGetMediaUrl(id, params) {
  const mediaId = params?.media_id;
  if (!mediaId) {
    sendCallback({ id, status: 400, error: 'MISSING_MEDIA_ID' });
    return;
  }
  try {
    const url = `https://labs.google/fx/api/trpc/media.getMediaUrlRedirect?name=${encodeURIComponent(mediaId)}`;
    const res = await fetch(url, { credentials: 'include', redirect: 'follow' });
    sendCallback({ id, status: res.status, result: { url: res.url } });
  } catch (err) {
    sendCallback({ id, status: 500, error: err.message });
  }
}

// ─── reCAPTCHA Helper ───────────────────────────────────────
async function solveCaptcha(requestId, pageAction) {
  const tabs = await chrome.tabs.query({ url: FLOW_URLS });

  let tab = tabs[0];
  if (!tab) {
    tab = await chrome.tabs.create({ url: DEFAULT_FLOW_URL, active: false });
    await new Promise((r) => setTimeout(r, 4000));
  }

  try {
    // Send message to content script in the Flow tab
    return await chrome.tabs.sendMessage(tab.id, {
      type: 'GET_CAPTCHA',
      requestId,
      pageAction,
    });
  } catch (err) {
    // If receiving end doesn't exist, inject content script and retry
    try {
      await chrome.scripting.executeScript({
        target: { tabId: tab.id },
        files: ['content.js'],
      });
      await new Promise((r) => setTimeout(r, 500));
      return await chrome.tabs.sendMessage(tab.id, {
        type: 'GET_CAPTCHA',
        requestId,
        pageAction,
      });
    } catch (e) {
      return { error: e.message };
    }
  }
}

async function openFlowTab() {
  const tabs = await chrome.tabs.query({ url: FLOW_URLS });
  if (tabs.length) {
    await chrome.tabs.update(tabs[0].id, { active: true });
  } else {
    await chrome.tabs.create({ url: DEFAULT_FLOW_URL, active: true });
  }
}

// ─── Send Callback to Go Agent ──────────────────────────────
async function sendCallback(msg) {
  // Try HTTP callback first
  try {
    await fetch(callbackUrl, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(callbackSecret ? { Authorization: `Bearer ${callbackSecret}` } : {}),
      },
      body: JSON.stringify({ ...msg, session_id: clientId }),
    });
    return;
  } catch (e) {
    // If HTTP fails, fallback to WS
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(msg));
    }
  }
}

// ─── Popup Messaging ────────────────────────────────────────
chrome.runtime.onMessage.addListener((msg, _, reply) => {
  if (msg.type === 'GET_STATUS') {
    reply({
      connected: isConnected,
      hasFlowKey: !!flowKey,
      clientId,
    });
  } else if (msg.type === 'OPEN_FLOW') {
    openFlowTab().then(() => reply({ ok: true }));
    return true;
  }
});
