document.addEventListener('DOMContentLoaded', () => {
  const agentStatus = document.getElementById('agent-status');
  const tokenStatus = document.getElementById('token-status');
  const openFlowBtn = document.getElementById('open-flow-btn');

  function update() {
    chrome.runtime.sendMessage({ type: 'GET_STATUS' }, (res) => {
      if (chrome.runtime.lastError || !res) {
        agentStatus.innerHTML = '<span class="dot red"></span>Offline';
        tokenStatus.innerHTML = '<span class="dot red"></span>Unknown';
        return;
      }
      agentStatus.innerHTML = res.connected
        ? '<span class="dot green"></span>Connected'
        : '<span class="dot red"></span>Disconnected';

      tokenStatus.innerHTML = res.hasFlowKey
        ? '<span class="dot green"></span>Ready'
        : '<span class="dot red"></span>Missing (Open Flow)';
    });
  }

  openFlowBtn.addEventListener('click', () => {
    chrome.runtime.sendMessage({ type: 'OPEN_FLOW' }, () => {
      setTimeout(update, 1000);
    });
  });

  update();
  setInterval(update, 2000);
});
