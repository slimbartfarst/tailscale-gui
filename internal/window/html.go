// internal/window/html.go
//
// Self-contained HTML/CSS/JS status window.
// Served by window.Manager at http://127.0.0.1:<port>/
//
// Features:
//   - Live status (auto-refreshes every 5 s)
//   - Connect / Disconnect buttons
//   - Per-peer online/offline indicator, IP copy, ping button
//   - Exit node selection dropdown
//   - MagicDNS / Accept routes / Shields up toggles
//   - Ping result shown inline next to each peer
package window

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Tailscale</title>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

  :root {
    --bg:       #0f1117;
    --surface:  #1a1d27;
    --border:   #2a2d3a;
    --text:     #e2e4ec;
    --muted:    #6b7280;
    --accent:   #4f8ef7;
    --green:    #34d399;
    --red:      #f87171;
    --amber:    #fbbf24;
    --radius:   8px;
    --font:     -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  }

  body {
    background: var(--bg);
    color: var(--text);
    font-family: var(--font);
    font-size: 14px;
    line-height: 1.5;
    padding: 0;
  }

  header {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 16px 20px;
    border-bottom: 1px solid var(--border);
    background: var(--surface);
    position: sticky;
    top: 0;
    z-index: 10;
  }

  header h1 { font-size: 16px; font-weight: 600; flex: 1; }

  .badge {
    padding: 3px 10px;
    border-radius: 20px;
    font-size: 12px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: .04em;
  }
  .badge.connected    { background: #14532d; color: var(--green); }
  .badge.disconnected { background: #3b1c1c; color: var(--red); }
  .badge.connecting   { background: #3b2e0a; color: var(--amber); }
  .badge.login        { background: #3b2e0a; color: var(--amber); }

  main { padding: 20px; max-width: 860px; margin: 0 auto; }

  section { margin-bottom: 28px; }
  section h2 {
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: .08em;
    color: var(--muted);
    margin-bottom: 10px;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 14px;
    border-bottom: 1px solid var(--border);
  }
  .row:last-child { border-bottom: none; }

  .dot {
    width: 8px; height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .dot.online  { background: var(--green); box-shadow: 0 0 6px var(--green); }
  .dot.offline { background: var(--muted); }

  .peer-name  { font-weight: 500; min-width: 140px; }
  .peer-os    { color: var(--muted); font-size: 12px; min-width: 60px; }
  .peer-ip    { font-family: monospace; font-size: 12px; color: var(--accent); flex: 1; }
  .ping-result { font-size: 12px; color: var(--muted); min-width: 90px; text-align: right; }

  button {
    padding: 5px 12px;
    border-radius: 6px;
    border: 1px solid var(--border);
    background: var(--bg);
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
    white-space: nowrap;
    transition: background .15s, border-color .15s;
  }
  button:hover { background: var(--border); }
  button.primary {
    background: var(--accent);
    border-color: var(--accent);
    color: #fff;
    font-weight: 600;
  }
  button.primary:hover { filter: brightness(1.12); }
  button.danger  { border-color: var(--red); color: var(--red); }
  button.danger:hover  { background: #3b1c1c; }

  .btn-row { display: flex; gap: 8px; flex-wrap: wrap; }

  /* Toggles */
  .toggle-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 10px 14px;
    border-bottom: 1px solid var(--border);
  }
  .toggle-row:last-child { border-bottom: none; }
  .toggle-label { font-size: 13px; }
  .toggle-desc  { font-size: 11px; color: var(--muted); margin-top: 1px; }

  /* Toggle switch */
  .switch { position: relative; display: inline-block; width: 36px; height: 20px; }
  .switch input { display: none; }
  .slider {
    position: absolute; inset: 0;
    background: var(--border);
    border-radius: 20px;
    cursor: pointer;
    transition: background .2s;
  }
  .slider::before {
    content: "";
    position: absolute;
    width: 14px; height: 14px;
    left: 3px; top: 3px;
    background: #fff;
    border-radius: 50%;
    transition: transform .2s;
  }
  input:checked + .slider { background: var(--accent); }
  input:checked + .slider::before { transform: translateX(16px); }

  /* Exit node select */
  select {
    background: var(--bg);
    border: 1px solid var(--border);
    color: var(--text);
    border-radius: 6px;
    padding: 5px 10px;
    font-size: 13px;
    cursor: pointer;
  }

  .self-ip {
    font-family: monospace;
    font-size: 15px;
    color: var(--accent);
    letter-spacing: .03em;
  }

  .empty { color: var(--muted); padding: 14px; font-size: 13px; }

  footer {
    text-align: center;
    color: var(--muted);
    font-size: 11px;
    padding: 16px;
    margin-top: 10px;
  }

  #last-updated { font-size: 11px; color: var(--muted); }
</style>
</head>
<body>

<header>
  <h1>Tailscale</h1>
  <span id="badge" class="badge disconnected">—</span>
  <span id="last-updated"></span>
</header>

<main>

  <!-- This device -->
  <section>
    <h2>This device</h2>
    <div class="card">
      <div class="row">
        <div>
          <div id="self-name" style="font-weight:600">—</div>
          <div id="self-ips" class="self-ip">—</div>
        </div>
        <div style="flex:1"></div>
        <div class="btn-row">
          <button class="primary" id="btn-connect"    onclick="action('/api/connect')">Connect</button>
          <button class="danger"  id="btn-disconnect" onclick="action('/api/disconnect')" style="display:none">Disconnect</button>
        </div>
      </div>
    </div>
  </section>

  <!-- Exit nodes -->
  <section>
    <h2>Exit node</h2>
    <div class="card">
      <div class="row">
        <span style="flex:1; color:var(--muted); font-size:13px">Route all traffic through:</span>
        <select id="exit-select" onchange="setExitNode(this.value)">
          <option value="">None</option>
        </select>
      </div>
    </div>
  </section>

  <!-- Preferences -->
  <section>
    <h2>Preferences</h2>
    <div class="card">
      <div class="toggle-row">
        <div>
          <div class="toggle-label">Use Tailscale DNS</div>
          <div class="toggle-desc">MagicDNS — resolve peer hostnames</div>
        </div>
        <label class="switch">
          <input type="checkbox" id="pref-dns" onchange="setPref('dns', this.checked)">
          <span class="slider"></span>
        </label>
      </div>
      <div class="toggle-row">
        <div>
          <div class="toggle-label">Accept subnet routes</div>
          <div class="toggle-desc">Route traffic through peers' advertised subnets</div>
        </div>
        <label class="switch">
          <input type="checkbox" id="pref-routes" onchange="setPref('routes', this.checked)">
          <span class="slider"></span>
        </label>
      </div>
      <div class="toggle-row">
        <div>
          <div class="toggle-label">Shields up</div>
          <div class="toggle-desc">Block all incoming connections from peers</div>
        </div>
        <label class="switch">
          <input type="checkbox" id="pref-shields" onchange="setPref('shields', this.checked)">
          <span class="slider"></span>
        </label>
      </div>
    </div>
  </section>

  <!-- Accounts -->
  <section>
    <h2>Accounts</h2>
    <div class="card" id="accounts-list">
      <div class="empty">Loading…</div>
    </div>
    <div style="margin-top:8px; display:flex; gap:8px; flex-wrap:wrap;">
      <button onclick="addAccount()">Add account…</button>
      <button class="danger" onclick="logoutAccount()">Log out</button>
      <span id="account-msg" style="font-size:12px; color:var(--muted); margin-left:4px"></span>
    </div>
  </section>

  <!-- Advertised subnets -->
  <section>
    <h2>Advertised subnets</h2>
    <div class="card" id="routes-list">
      <div class="empty">Loading…</div>
    </div>
    <div style="margin-top:8px; display:flex; gap:8px; align-items:center; flex-wrap:wrap;">
      <input id="route-input" type="text" placeholder="192.168.1.0/24"
             style="background:var(--surface); border:1px solid var(--border); color:var(--text);
                    border-radius:6px; padding:5px 10px; font-size:13px; font-family:monospace; width:180px;"
             onkeydown="if(event.key==='Enter') addRoute()">
      <button class="primary" onclick="addRoute()">Advertise</button>
      <span id="route-msg" style="font-size:12px; color:var(--muted)"></span>
    </div>
  </section>

  <!-- Peers -->
  <section>
    <h2>Peers</h2>
    <div class="card" id="peers-list">
      <div class="empty">Loading…</div>
    </div>
  </section>

</main>

<footer>tailscale-gui · auto-refreshes every 5 s</footer>

<script>
// ── Data fetch & render ────────────────────────────────────────────────────────

let lastData = null;

async function refresh() {
  try {
    const [statusR, routesR, accountsR] = await Promise.all([
      fetch('/api/status'),
      fetch('/api/routes'),
      fetch('/api/accounts'),
    ]);
    if (!statusR.ok) throw new Error(statusR.statusText);
    const d = await statusR.json();
    lastData = d;
    render(d);
    if (routesR.ok) {
      const routes = await routesR.json();
      renderRoutes(routes);
    }
    if (accountsR.ok) {
      const accts = await accountsR.json();
      renderAccounts(accts);
    }
    document.getElementById('last-updated').textContent =
      'Updated ' + new Date().toLocaleTimeString();
  } catch(e) {
    document.getElementById('badge').textContent = 'unreachable';
    document.getElementById('badge').className = 'badge disconnected';
  }
}

function render(d) {
  // Badge + buttons
  const state = d.backend_state || '';
  const badge = document.getElementById('badge');
  badge.textContent = state;
  badge.className = 'badge ' + stateClass(state);

  const connected = state === 'Running';
  document.getElementById('btn-connect').style.display    = connected ? 'none'  : '';
  document.getElementById('btn-disconnect').style.display = connected ? ''      : 'none';

  // Self
  if (d.self) {
    document.getElementById('self-name').textContent = d.self.HostName || '—';
  }
  document.getElementById('self-ips').textContent =
    (d.tailnet_ips || []).join('  ') || '—';

  // Exit nodes
  const sel = document.getElementById('exit-select');
  const prevVal = sel.value;
  sel.innerHTML = '<option value="">None</option>';
  (d.exit_nodes || []).forEach(p => {
    const ip = p.TailscaleIPs && p.TailscaleIPs[0] ? p.TailscaleIPs[0] : '';
    const opt = document.createElement('option');
    opt.value = p.ID;
    opt.textContent = p.HostName + (ip ? '  ' + ip : '');
    if (p.ID === d.active_exit_node) opt.selected = true;
    sel.appendChild(opt);
  });
  if (!d.active_exit_node) sel.value = '';

  // Prefs
  if (d.prefs) {
    document.getElementById('pref-dns').checked    = d.prefs.accept_dns;
    document.getElementById('pref-routes').checked = d.prefs.accept_routes;
    document.getElementById('pref-shields').checked = d.prefs.shields_up;
  }

  // Peers
  const list = document.getElementById('peers-list');
  const peers = (d.peers || []).slice().sort((a,b) => {
    if (b.Online !== a.Online) return b.Online ? 1 : -1;
    return (a.HostName||'').localeCompare(b.HostName||'');
  });

  if (peers.length === 0) {
    list.innerHTML = '<div class="empty">No peers on this tailnet yet.</div>';
    return;
  }

  list.innerHTML = '';
  peers.forEach(p => {
    const ip = p.TailscaleIPs && p.TailscaleIPs[0] ? p.TailscaleIPs[0] : '';
    const row = document.createElement('div');
    row.className = 'row';
    row.id = 'peer-' + safeId(ip);
    const sendBtn = p.Online && p.ID
      ? '<button title="Send a file to ' + esc(p.HostName) + ' via Taildrop" onclick="sendFile(\'' + esc(p.ID) + '\', \'' + esc(p.HostName) + '\')">Send file&#8230;</button>'
      : '';
    const pingBtn = p.Online && ip
      ? '<button onclick="pingPeer(\'' + esc(ip) + '\')">Ping</button>'
      : '';
    const sshBtn = p.Online && p.ID && peerSupportsSSH(p)
      ? '<button title="SSH to ' + esc(p.HostName) + '" onclick="launchSSH(\'' + esc(p.ID) + '\', \'' + esc(p.HostName) + '\')">SSH&#8230;</button>'
      : '';
    row.innerHTML = ` + "`" + `
      <span class="dot ${p.Online ? 'online' : 'offline'}"></span>
      <span class="peer-name">${esc(p.HostName)}</span>
      <span class="peer-os">${esc(p.OS || '')}</span>
      <span class="peer-ip" title="Click to copy" style="cursor:pointer"
            onclick="copyIP('${esc(ip)}')">${esc(ip)}</span>
      <span class="ping-result" id="ping-${safeId(ip)}"></span>
      ${pingBtn}${sshBtn}${sendBtn}
    ` + "`" + `;
    list.appendChild(row);
  });
}

// ── Actions ────────────────────────────────────────────────────────────────────

async function action(url) {
  try {
    const r = await fetch(url, {method:'POST'});
    const d = await r.json();
    if (d.error) alert('Error: ' + d.error);
    setTimeout(refresh, 800);
  } catch(e) { alert('Request failed: ' + e); }
}

async function setExitNode(id) {
  const url = id ? '/api/set-exit-node?id=' + encodeURIComponent(id)
                 : '/api/clear-exit-node';
  await action(url);
}

async function setPref(pref, val) {
  await action('/api/set-pref?pref=' + pref + '&value=' + val);
}

async function pingPeer(ip) {
  const el = document.getElementById('ping-' + safeId(ip));
  if (el) el.textContent = 'pinging…';
  try {
    const r = await fetch('/api/ping?ip=' + encodeURIComponent(ip));
    const d = await r.json();
    if (el) {
      if (d.error) {
        el.textContent = '✗ ' + d.error;
        el.style.color = 'var(--red)';
      } else {
        const ms = d.LatencySeconds ? (d.LatencySeconds * 1000).toFixed(1) + ' ms' : 'ok';
        const via = d.Endpoint ? ' via ' + d.Endpoint : '';
        el.textContent = '✓ ' + ms + via;
        el.style.color = 'var(--green)';
      }
    }
  } catch(e) {
    if (el) { el.textContent = '✗ failed'; el.style.color = 'var(--red)'; }
  }
}

// peerSupportsSSH mirrors the Go logic in internal/ssh/ssh.go:PeerSupportsSSH.
function peerSupportsSSH(p) {
  if (!p.Online) return false;
  if (p.sshHostKeys && p.sshHostKeys.length > 0) return true;
  const linuxLike = ['linux', 'darwin', 'freebsd', 'openbsd', 'netbsd'];
  return linuxLike.includes((p.OS || '').toLowerCase()) &&
         p.TailscaleIPs && p.TailscaleIPs.length > 0;
}

async function launchSSH(peerID, hostname) {
  const btn = event.target;
  const origText = btn.textContent;
  btn.textContent = 'launching…';
  btn.disabled = true;
  try {
    const r = await fetch('/api/ssh?id=' + encodeURIComponent(peerID), {method: 'POST'});
    const d = await r.json();
    if (d.error) {
      alert('SSH error: ' + d.error);
      btn.textContent = origText;
      btn.disabled = false;
    } else {
      btn.textContent = 'launched ✓';
      setTimeout(() => { btn.textContent = origText; btn.disabled = false; }, 2500);
    }
  } catch(e) {
    alert('SSH request failed: ' + e);
    btn.textContent = origText;
    btn.disabled = false;
  }
}

async function sendFile(peerID, hostname) {
  const btn = event.target;
  const origText = btn.textContent;
  btn.textContent = 'opening…';
  btn.disabled = true;
  try {
    const r = await fetch('/api/send-file?id=' + encodeURIComponent(peerID), {method: 'POST'});
    const d = await r.json();
    if (d.error) {
      alert('Send error: ' + d.error);
    } else {
      // Picker is now open on the desktop — restore button after a moment
      btn.textContent = 'picker open';
      setTimeout(() => { btn.textContent = origText; btn.disabled = false; }, 3000);
      return;
    }
  } catch(e) {
    alert('Request failed: ' + e);
  }
  btn.textContent = origText;
  btn.disabled = false;
}

// ── Accounts ──────────────────────────────────────────────────────────────────

function renderAccounts(accounts) {
  const list = document.getElementById('accounts-list');
  if (!accounts || accounts.length === 0) {
    list.innerHTML = '<div class="empty">No profiles found (requires tailscaled v1.56+).</div>';
    return;
  }
  list.innerHTML = '';
  accounts.forEach(a => {
    const row = document.createElement('div');
    row.className = 'row';
    const check = a.active ? '✓' : ' ';
    const nameColor = a.active ? 'var(--text)' : 'var(--muted)';
    const switchBtn = a.active
      ? ''
      : '<button onclick="switchAccount(\'' + esc(a.id) + '\', \'' + esc(a.name) + '\')">Switch</button>';
    row.innerHTML = ` + "`" + `
      <span style="font-size:14px; min-width:16px; color:var(--green)">${check}</span>
      <span style="flex:1; font-size:13px; color:${nameColor}">${esc(a.name)}</span>
      ${a.active ? '<span style="font-size:11px;color:var(--muted)">active</span>' : ''}
      ${switchBtn}
    ` + "`" + `;
    list.appendChild(row);
  });
}

async function switchAccount(id, name) {
  const msg = document.getElementById('account-msg');
  msg.textContent = 'switching to ' + name + '…';
  msg.style.color = 'var(--muted)';
  try {
    const r = await fetch('/api/accounts/switch?id=' + encodeURIComponent(id), {method: 'POST'});
    const d = await r.json();
    if (d.error) {
      msg.textContent = '✗ ' + d.error;
      msg.style.color = 'var(--red)';
    } else {
      msg.textContent = '✓ switched';
      msg.style.color = 'var(--green)';
      setTimeout(() => { msg.textContent = ''; }, 3000);
      setTimeout(refresh, 1200);
    }
  } catch(e) {
    msg.textContent = '✗ request failed';
    msg.style.color = 'var(--red)';
  }
}

async function addAccount() {
  const msg = document.getElementById('account-msg');
  msg.textContent = 'opening browser for login…';
  msg.style.color = 'var(--muted)';
  try {
    const r = await fetch('/api/accounts/add', {method: 'POST'});
    const d = await r.json();
    if (d.error) {
      msg.textContent = '✗ ' + d.error;
      msg.style.color = 'var(--red)';
    } else {
      msg.textContent = '✓ browser opened — complete login there';
      msg.style.color = 'var(--green)';
      setTimeout(() => { msg.textContent = ''; }, 8000);
      setTimeout(refresh, 3000);
    }
  } catch(e) {
    msg.textContent = '✗ request failed';
    msg.style.color = 'var(--red)';
  }
}

async function logoutAccount() {
  if (!confirm('Log out of the current Tailscale account?')) return;
  const msg = document.getElementById('account-msg');
  msg.textContent = 'logging out…';
  msg.style.color = 'var(--muted)';
  try {
    const r = await fetch('/api/accounts/logout', {method: 'POST'});
    const d = await r.json();
    if (d.error) {
      msg.textContent = '✗ ' + d.error;
      msg.style.color = 'var(--red)';
    } else {
      msg.textContent = '✓ logged out';
      msg.style.color = 'var(--green)';
      setTimeout(() => { msg.textContent = ''; }, 3000);
      setTimeout(refresh, 800);
    }
  } catch(e) {
    msg.textContent = '✗ request failed';
    msg.style.color = 'var(--red)';
  }
}

// ── Routes ────────────────────────────────────────────────────────────────────

function renderRoutes(routes) {
  const list = document.getElementById('routes-list');
  if (!routes || routes.length === 0) {
    list.innerHTML = '<div class="empty">No subnets advertised. Enter a CIDR below to start.</div>';
    return;
  }
  list.innerHTML = '';
  routes.forEach(r => {
    const row = document.createElement('div');
    row.className = 'row';
    const statusColor = r.approved ? 'var(--green)' : 'var(--amber)';
    const statusText  = r.approved ? 'approved ✓'   : 'pending approval…';
    row.innerHTML = ` + "`" + `
      <span style="font-family:monospace; font-size:13px; color:var(--accent); min-width:160px">${esc(r.prefix)}</span>
      <span style="flex:1; font-size:12px; color:var(--muted)">${esc(r.label)}</span>
      <span style="font-size:12px; color:${statusColor}; min-width:130px; text-align:right">${statusText}</span>
      <button class="danger" onclick="removeRoute('${esc(r.prefix)}')">Remove</button>
    ` + "`" + `;
    list.appendChild(row);
  });
}

async function addRoute() {
  const input = document.getElementById('route-input');
  const msg   = document.getElementById('route-msg');
  const cidr  = input.value.trim();
  if (!cidr) { msg.textContent = 'Enter a CIDR prefix first.'; return; }

  msg.textContent = 'adding…';
  msg.style.color = 'var(--muted)';
  try {
    const r = await fetch('/api/routes/add?cidr=' + encodeURIComponent(cidr), {method: 'POST'});
    const d = await r.json();
    if (d.error) {
      msg.textContent = '✗ ' + d.error;
      msg.style.color = 'var(--red)';
    } else {
      msg.textContent = '✓ advertising ' + cidr;
      msg.style.color = 'var(--green)';
      input.value = '';
      setTimeout(() => { msg.textContent = ''; }, 4000);
      setTimeout(refresh, 600);
    }
  } catch(e) {
    msg.textContent = '✗ request failed';
    msg.style.color = 'var(--red)';
  }
}

async function removeRoute(cidr) {
  const msg = document.getElementById('route-msg');
  msg.textContent = 'removing ' + cidr + '…';
  msg.style.color = 'var(--muted)';
  try {
    const r = await fetch('/api/routes/remove?cidr=' + encodeURIComponent(cidr), {method: 'POST'});
    const d = await r.json();
    if (d.error) {
      msg.textContent = '✗ ' + d.error;
      msg.style.color = 'var(--red)';
    } else {
      msg.textContent = '✓ removed ' + cidr;
      msg.style.color = 'var(--green)';
      setTimeout(() => { msg.textContent = ''; }, 3000);
      setTimeout(refresh, 600);
    }
  } catch(e) {
    msg.textContent = '✗ request failed';
    msg.style.color = 'var(--red)';
  }
}

async function copyIP(ip) {
  try {
    await navigator.clipboard.writeText(ip);
    // Brief flash to confirm
    const els = document.querySelectorAll('.peer-ip');
    els.forEach(el => { if (el.textContent === ip) {
      const orig = el.style.color;
      el.style.color = 'var(--green)';
      setTimeout(() => el.style.color = orig, 600);
    }});
  } catch(e) {}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

function stateClass(s) {
  if (s === 'Running') return 'connected';
  if (s === 'Starting') return 'connecting';
  if (s === 'NeedsLogin') return 'login';
  return 'disconnected';
}

function esc(s) {
  return String(s)
    .replace(/&/g,'&amp;').replace(/</g,'&lt;')
    .replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function safeId(ip) { return ip.replace(/[.:]/g, '-'); }

// ── Bootstrap ──────────────────────────────────────────────────────────────────
refresh();
setInterval(refresh, 5000);
</script>
</body>
</html>`
