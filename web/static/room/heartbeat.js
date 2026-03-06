// Heartbeat: POST /rooms/{id}/active on a 60s interval while the page is
// visible; sendBeacon /rooms/{id}/inactive immediately on hide.
const activeURL = `/rooms/${window.roomID}/active`;
const inactiveURL = `/rooms/${window.roomID}/inactive`;
let heartbeatTimer = null;

function ping() {
  fetch(activeURL, { method: 'POST' }).catch(() => {});
}

function leave() {
  navigator.sendBeacon(inactiveURL);
}

function startHeartbeat() {
  ping();
  clearInterval(heartbeatTimer);
  heartbeatTimer = setInterval(ping, 60 * 1000);
}

function stopHeartbeat() {
  clearInterval(heartbeatTimer);
  leave();
}

document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') {
    startHeartbeat();
  } else {
    stopHeartbeat();
  }
});

if (document.visibilityState === 'visible') {
  startHeartbeat();
}
