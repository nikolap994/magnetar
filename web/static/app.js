// Magnetar web UI. No dependencies; all untrusted text is rendered via textContent.
"use strict";

const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];

/* ---------- helpers ---------- */
function h(tag, attrs = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k === "text") el.textContent = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "dataset") Object.assign(el.dataset, v);
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) if (c != null) el.append(c);
  return el;
}
function icon(name) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
  use.setAttribute("href", "#i-" + name);
  svg.append(use);
  return svg;
}
const UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
function bytes(n) {
  if (!n || n < 0) return "0 B";
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), UNITS.length - 1);
  const v = n / 1024 ** i;
  return (v >= 100 || i === 0 ? v.toFixed(0) : v.toFixed(1)) + " " + UNITS[i];
}
const rate = (n) => (n > 0 ? bytes(n) + "/s" : "–");
function eta(s) {
  if (s == null || s < 0) return "∞";
  if (s < 60) return s + "s";
  if (s < 3600) return Math.floor(s / 60) + "m " + (s % 60) + "s";
  if (s < 86400) return Math.floor(s / 3600) + "h " + Math.floor((s % 3600) / 60) + "m";
  return Math.floor(s / 86400) + "d " + Math.floor((s % 86400) / 3600) + "h";
}
const pct = (p) => (p >= 1 ? "100%" : (Math.floor(p * 1000) / 10).toFixed(1) + "%");
const fmtDate = (d) => (d ? new Date(d).toLocaleString() : "–");

/* ---------- speed-limit inputs with automatic units ---------- */
// Limits are stored in KiB/s. Each <input class="rate"> becomes a hidden field
// holding KiB/s plus a visible number + unit picker (KiB/s, MiB/s, GiB/s).
const RATE_UNITS = [["KiB/s", 1], ["MiB/s", 1024], ["GiB/s", 1024 * 1024]];
function bestRateUnit(kib) {
  if (kib >= 1024 * 1024) return 2;
  if (kib >= 1024) return 1;
  return 0;
}
const trimNum = (n) => String(Math.round(n * 100) / 100);
function enhanceRateInput(hidden) {
  hidden.type = "hidden";
  const num = h("input", { type: "number", min: "0", step: "any", placeholder: "Unlimited", "aria-label": "Limit" });
  const unit = h("select", { "aria-label": "Unit" }, RATE_UNITS.map(([label], i) => h("option", { value: String(i), text: label })));
  const sync = () => {
    const v = Math.max(0, Number(num.value || 0));
    hidden.value = String(Math.round(v * RATE_UNITS[+unit.value][1]));
  };
  num.addEventListener("input", sync);
  unit.addEventListener("change", sync);
  hidden._show = () => {
    const kib = Math.max(0, Number(hidden.value || 0));
    const u = bestRateUnit(kib);
    unit.value = String(u);
    num.value = kib ? trimNum(kib / RATE_UNITS[u][1]) : "0";
  };
  hidden.after(h("div", { class: "rate-input" }, num, unit));
}
function setRate(input, kib) { input.value = String(kib); if (input._show) input._show(); }
function refreshRates(root) { root.querySelectorAll("input.rate").forEach((i) => i._show && i._show()); }

/* ---------- API ---------- */
class ApiError extends Error { constructor(msg, status) { super(msg); this.status = status; } }
async function api(path, { method = "GET", body, form } = {}) {
  const opts = { method, credentials: "same-origin", headers: { "X-Magnetar-Request": "1" } };
  if (form) opts.body = form;
  else if (body !== undefined) { opts.body = JSON.stringify(body); opts.headers["Content-Type"] = "application/json"; }
  const res = await fetch(path, opts);
  const isJSON = (res.headers.get("Content-Type") || "").includes("json");
  const data = isJSON ? await res.json() : null;
  if (res.status === 401 && path !== "/api/login") { showLogin(); }
  if (!res.ok) throw new ApiError((data && data.error) || res.statusText, res.status);
  return data;
}

/* ---------- state ---------- */
const state = {
  torrents: [],
  stats: {},
  filter: "all",
  query: "",
  sort: JSON.parse(localStorage.getItem("mg.sort") || '{"key":"addedAt","dir":-1}'),
  selected: new Set(),
  focused: null,
  detail: null,
  tab: "overview",
  history: [],
  settings: null,
  pendingFiles: [],
};

const FILTERS = [
  { id: "all", label: "All", icon: "all", match: () => true },
  { id: "downloading", label: "Downloading", icon: "down", match: (t) => ["downloading", "metadata", "stalled", "checking"].includes(t.state) },
  { id: "seeding", label: "Seeding", icon: "up", match: (t) => t.state === "seeding" },
  { id: "completed", label: "Completed", icon: "check", match: (t) => t.progress >= 1 },
  { id: "queued", label: "Queued", icon: "clock", match: (t) => t.state === "queued" },
  { id: "paused", label: "Paused", icon: "pause", match: (t) => t.state === "paused" },
  { id: "stalled", label: "Stalled", icon: "clock", match: (t) => t.state === "stalled" },
  { id: "error", label: "Errors", icon: "alert", match: (t) => t.state === "error" },
];

/* ---------- toasts ---------- */
function toast(msg, kind = "ok") {
  const el = h("div", { class: "toast " + kind }, icon(kind === "ok" ? "check" : "alert"), h("span", { class: "t", text: msg }));
  $("#toasts").append(el);
  setTimeout(() => el.remove(), kind === "ok" ? 3500 : 6000);
}
const fail = (e) => toast(e.message || String(e), "err");

/* ---------- theme ---------- */
function applyTheme(t) {
  document.documentElement.dataset.theme = t;
  localStorage.setItem("mg.theme", t);
}
applyTheme(localStorage.getItem("mg.theme") || (matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark"));

/* ---------- auth ---------- */
async function boot() {
  let s;
  try { s = await api("/api/session"); } catch (e) { return showFatal(e); }
  if (!s.authenticated) return showLogin(s);
  $("#login").hidden = true;
  $("#app").hidden = false;
  $("#about-ver").textContent = s.version;
  connect();
  loadCategories();
  handleIncomingLinks();
}
function showFatal(e) {
  document.body.textContent = "Magnetar is not reachable: " + e.message;
}
let loginShown = false;
async function showLogin(s) {
  if (loginShown) return;
  loginShown = true;
  closeStream();
  if (!s) { try { s = await api("/api/session"); } catch { s = {}; } }
  $("#app").hidden = true;
  $("#login").hidden = false;
  const pwInput = $("#login-pw");
  const submit = $("#login-form button");
  if (s.passwordSet) {
    $("#login-msg").textContent = location.hash === "#expired" ? "That sign-in link has expired. Enter your password." : "Enter your password to continue.";
    pwInput.hidden = false; submit.hidden = false; pwInput.focus();
  } else {
    $("#login-msg").textContent = location.hash === "#expired"
      ? "That sign-in link has expired. Start Magnetar again (or run it once more) to get a fresh link."
      : "Open Magnetar from your computer to sign in - it opens this page with a one-time link. Set a password in Settings to sign in from anywhere.";
    pwInput.hidden = true; submit.hidden = true;
  }
}
$("#login-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const err = $("#login-err");
  err.hidden = true;
  try {
    await api("/api/login", { method: "POST", body: { password: $("#login-pw").value } });
    $("#login-pw").value = "";
    loginShown = false;
    history.replaceState(null, "", "/");
    boot();
  } catch (e) { err.textContent = e.message; err.hidden = false; }
});

/* ---------- live updates ---------- */
let es = null;
function connect() {
  closeStream();
  es = new EventSource("/api/events");
  es.addEventListener("snapshot", (ev) => {
    const d = JSON.parse(ev.data);
    state.torrents = d.torrents || [];
    state.stats = d.stats || {};
    if (d.update) { state.update = d.update; renderUpdate(); }
    state.history.push([state.stats.downRate || 0, state.stats.upRate || 0]);
    if (state.history.length > 90) state.history.shift();
    const known = new Set(state.torrents.map((t) => t.hash));
    for (const hsh of state.selected) if (!known.has(hsh)) state.selected.delete(hsh);
    if (state.detail && !known.has(state.detail)) closeDetail();
    render();
    if (state.detail) loadDetail();
  });
  es.addEventListener("notify", (ev) => {
    const n = JSON.parse(ev.data);
    if (n.type === "shutdown") { showStopped(); return; }
    if (n.type === "ui-add") { openAdd(n.message, true); window.focus(); return; }
    if (n.type === "ui-focus") { window.focus(); return; }
    if (n.type === "completed") {
      toast("Download complete: " + n.name); // the desktop notification comes from Magnetar itself
    } else if (n.type === "error") toast(n.name + ": " + n.message, "err");
  });
  es.onerror = async () => {
    if (es && es.readyState === EventSource.CLOSED) {
      setTimeout(async () => {
        try { const s = await api("/api/session"); if (s.authenticated) connect(); else showLogin(s); }
        catch { setTimeout(connect, 3000); }
      }, 1500);
    }
  };
}
function closeStream() { if (es) { es.close(); es = null; } }

/* ---------- rendering ---------- */
function visible() {
  const f = FILTERS.find((x) => x.id === state.filter) || FILTERS[0];
  const q = state.query.trim().toLowerCase();
  const cat = state.category;
  const list = state.torrents.filter((t) => f.match(t) && (cat == null || (t.category || "") === cat) &&
    (!q || t.name.toLowerCase().includes(q) || t.hash.includes(q)));
  const { key, dir } = state.sort;
  list.sort((a, b) => {
    let x = a[key], y = b[key];
    if (key === "eta") { x = x < 0 ? Infinity : x; y = y < 0 ? Infinity : y; }
    if (typeof x === "string") return x.localeCompare(y) * dir;
    return ((x > y) - (x < y)) * dir;
  });
  return list;
}

function render() {
  renderFilters();
  renderCategories();
  renderStats();
  renderList();
  renderBulk();
}

function renderCategories() {
  const nav = $("#cat-filters");
  const items = [{ name: null, label: "All categories" }, ...state.cats.map((c) => ({ name: c.name, label: c.name })), { name: "", label: "Uncategorized" }];
  nav.replaceChildren(...items.map((c) => {
    const count = c.name == null ? state.torrents.length : state.torrents.filter((t) => (t.category || "") === c.name).length;
    return h("button", { class: state.category === c.name ? "active" : "", title: c.name ? (state.cats.find((x) => x.name === c.name)?.savePath || "") : "",
      onclick: () => { state.category = c.name; render(); } },
      icon(c.name == null ? "all" : "tag"), h("span", { text: c.label }), h("span", { class: "count", text: count || "" }));
  }));
}

function renderFilters() {
  const nav = $("#filters");
  if (!nav.childElementCount) {
    for (const f of FILTERS) {
      nav.append(h("button", { dataset: { f: f.id }, onclick: () => { state.filter = f.id; render(); } },
        icon(f.icon), h("span", { text: f.label }), h("span", { class: "count" })));
    }
  }
  // Narrow screens hide the sidebar; the same filters are offered as a menu.
  const sel = $("#m-filter");
  if (!sel.childElementCount) {
    for (const f of FILTERS) sel.append(h("option", { value: f.id }));
    sel.addEventListener("change", () => { state.filter = sel.value; render(); });
  }
  for (const o of sel.options) {
    const f = FILTERS.find((x) => x.id === o.value);
    const n = state.torrents.filter(f.match).length;
    o.textContent = f.label + (n ? ` (${n})` : "");
  }
  sel.value = state.filter || "all";
  for (const btn of nav.children) {
    const f = FILTERS.find((x) => x.id === btn.dataset.f);
    btn.classList.toggle("active", f.id === state.filter);
    btn.querySelector(".count").textContent = state.torrents.filter(f.match).length || "";
  }
}

function renderStats() {
  const s = state.stats;
  $("#g-down").textContent = bytes(s.downRate) + "/s";
  $("#g-up").textContent = bytes(s.upRate) + "/s";
  $("#m-down").textContent = "↓ " + bytes(s.downRate) + "/s";
  $("#m-up").textContent = "↑ " + bytes(s.upRate) + "/s";
  const lim = (n) => (n ? bytes(n) + "/s" : "∞");
  $("#g-limits").textContent = (s.altSpeed ? "🐢 " : "") + "↓ " + lim(s.downLimit) + "  ↑ " + lim(s.upLimit);
  $("#alt-toggle").classList.toggle("on", !!s.altSpeed);
  showAltPanel(!!s.altSpeed);
  $("#g-peers").textContent = s.peers ?? 0;
  $("#g-dht").textContent = s.dhtNodes ?? 0;
  $("#g-port").textContent = s.listenPort || "–";
  renderConnection();
  const sec = $("#g-security");
  const parts = [];
  parts.push(s.encryption === "require" ? "Encryption required" : s.encryption === "allow" ? "Encryption optional" : "Encryption preferred");
  if (s.bindInterface) parts.push(s.killSwitch ? `${s.bindInterface} is down - traffic stopped` : `only via ${s.bindInterface}`);
  else if (s.bindAddress) parts.push("bound to " + s.bindAddress);
  if (s.proxy) parts.push(`${s.proxy === "socks5" ? "SOCKS5" : "HTTP"} proxy${s.proxyPeers ? " for all traffic" : " for trackers"}`);
  if (s.blocklistSize) parts.push(s.blocklistSize.toLocaleString() + " blocked ranges");
  sec.querySelector("span").textContent = parts.join(" · ");
  sec.classList.toggle("warn", s.encryption === "allow" || s.online === false || !!s.killSwitch);
  drawSpark();
  document.title = s.downRate > 0 ? `↓ ${bytes(s.downRate)}/s · Magnetar` : "Magnetar";
}

function drawSpark() {
  const c = $("#spark");
  const dpr = window.devicePixelRatio || 1;
  const w = c.clientWidth, hgt = c.clientHeight;
  if (c.width !== w * dpr) { c.width = w * dpr; c.height = hgt * dpr; }
  const ctx = c.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, hgt);
  const hist = state.history;
  if (hist.length < 2) return;
  const max = Math.max(1024, ...hist.flat());
  const line = (idx, color, fill) => {
    ctx.beginPath();
    hist.forEach((p, i) => {
      const x = (i / (90 - 1)) * w, y = hgt - 2 - (p[idx] / max) * (hgt - 6);
      i ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
    });
    ctx.strokeStyle = color; ctx.lineWidth = 1.6; ctx.stroke();
    ctx.lineTo(((hist.length - 1) / 89) * w, hgt); ctx.lineTo(0, hgt); ctx.closePath();
    ctx.fillStyle = fill; ctx.fill();
  };
  line(1, "#38bdf8", "rgba(56,189,248,.08)");
  line(0, "#22c55e", "rgba(34,197,94,.12)");
}

const rowCache = new Map();
function renderList() {
  const list = $("#list");
  const items = visible();
  $("#empty").hidden = state.torrents.length > 0;
  $(".list-head").hidden = state.torrents.length === 0;
  const seen = new Set();
  let prev = null;
  for (const t of items) {
    seen.add(t.hash);
    let row = rowCache.get(t.hash);
    if (!row) { row = buildRow(t.hash); rowCache.set(t.hash, row); }
    updateRow(row, t);
    const next = prev ? prev.nextSibling : list.firstChild;
    if (next !== row) list.insertBefore(row, next);
    prev = row;
  }
  for (const [hash, row] of rowCache) {
    if (!seen.has(hash)) { row.remove(); if (!state.torrents.some((t) => t.hash === hash)) rowCache.delete(hash); }
  }
  for (const b of $$(".list-head .col[data-sort]")) {
    b.classList.toggle("sorted", b.dataset.sort === state.sort.key);
    b.classList.toggle("asc", state.sort.dir === 1);
  }
  const all = $("#select-all");
  const vis = items.map((t) => t.hash);
  const n = vis.filter((x) => state.selected.has(x)).length;
  all.checked = n > 0 && n === vis.length;
  all.indeterminate = n > 0 && n < vis.length;
}

function buildRow(hash) {
  const cb = h("input", { type: "checkbox", "aria-label": "Select" });
  cb.addEventListener("click", (e) => { e.stopPropagation(); toggleSelect(hash, cb.checked); });
  const r = {
    cb,
    name: h("div", { class: "n" }),
    pill: h("span", { class: "pill" }),
    sub: h("span"),
    size: h("div", { class: "cell c-size" }),
    bar: h("div", { class: "bar" }, h("i")),
    pct: h("div", { class: "pct" }),
    down: h("div", { class: "cell down c-rate" }),
    up: h("div", { class: "cell up c-rate" }),
    eta: h("div", { class: "cell c-eta" }),
    peers: h("div", { class: "cell c-peers" }),
    ratio: h("div", { class: "cell c-ratio" }),
    toggle: h("button", { class: "btn icon", title: "Pause" }),
  };
  r.toggle.addEventListener("click", (e) => { e.stopPropagation(); const t = byHash(hash); act(t.state === "paused" ? "resume" : "pause", [hash]); });
  const rm = h("button", { class: "btn icon", title: "Remove", onclick: (e) => { e.stopPropagation(); confirmRemove([hash]); } }, icon("trash"));
  const row = h("div", { class: "row-t", role: "listitem", dataset: { hash } },
    h("label", { class: "check", onclick: (e) => e.stopPropagation() }, cb, h("span")),
    h("div", { class: "t-name" }, r.name, h("div", { class: "s" }, r.pill, r.sub)),
    r.size,
    h("div", { class: "progress" }, r.bar, r.pct),
    r.down, r.up, r.eta, r.peers, r.ratio,
    h("div", { class: "row-actions" }, r.toggle, rm),
  );
  row._r = r;
  row.addEventListener("click", (e) => {
    if (e.ctrlKey || e.metaKey) return toggleSelect(hash, !state.selected.has(hash));
    if (e.shiftKey && state.focused) return rangeSelect(state.focused, hash);
    openDetail(hash);
  });
  row.addEventListener("dblclick", () => { const t = byHash(hash); act(t.state === "paused" ? "resume" : "pause", [hash]); });
  row.addEventListener("contextmenu", (e) => { e.preventDefault(); contextMenu(e, hash); });
  return row;
}

function updateRow(row, t) {
  const r = row._r;
  set(r.name, t.name);
  r.name.title = t.name;
  r.pill.className = "pill " + t.state;
  set(r.pill, t.state === "metadata" ? "fetching metadata" : t.state);
  set(r.sub, subtitle(t));
  set(r.size, t.size ? bytes(t.size) : "–");
  r.bar.className = "bar " + t.state;
  r.bar.firstChild.style.width = (t.progress * 100).toFixed(2) + "%";
  set(r.pct, t.state === "metadata" ? "Waiting for peers…" : pct(t.progress) + (t.size && t.progress < 1 ? " of " + bytes(t.size) : ""));
  set(r.down, rate(t.downRate));
  set(r.up, rate(t.upRate));
  set(r.eta, t.state === "downloading" ? eta(t.eta) : "–");
  set(r.peers, t.peers + (t.knownPeers > t.peers ? " / " + t.knownPeers : ""));
  set(r.ratio, t.ratio.toFixed(2));
  const paused = t.state === "paused";
  if (r.toggle.dataset.p !== String(paused)) {
    r.toggle.replaceChildren(icon(paused ? "play" : "pause"));
    r.toggle.title = paused ? "Resume" : "Pause";
    r.toggle.dataset.p = String(paused);
  }
  r.cb.checked = state.selected.has(t.hash);
  row.classList.toggle("selected", state.selected.has(t.hash));
  row.classList.toggle("focused", state.detail === t.hash);
}
function subtitle(t) {
  const s = subtitleText(t);
  const f = t.forced && t.state !== "paused" ? "Forced · " : "";
  return f + (t.category ? `${t.category} · ${s}` : s);
}
function subtitleText(t) {
  switch (t.state) {
    case "error": return t.error;
    case "downloading": case "stalled": return `${t.seeds} seeds · ${t.peers} peers` + (t.sequential ? " · sequential" : "");
    case "checking": return "Checking existing files…";
    case "moving": return "Moving files…";
    case "metadata": return t.knownPeers ? `Fetching metadata from ${t.knownPeers} peers` : "Searching for peers";
    case "seeding": return `${t.peers} peers · ratio ${t.ratio.toFixed(2)}`;
    case "queued": return `#${t.queuePos} in queue` + (t.size ? ` · ${bytes(t.completed)} of ${bytes(t.size)}` : " · waiting for a free slot");
    case "paused": return t.size ? `${bytes(t.completed)} of ${bytes(t.size)}` : "Paused";
    default: return t.completedAt ? "Finished " + new Date(t.completedAt).toLocaleDateString() : "";
  }
}
function set(el, text) { if (el.textContent !== String(text)) el.textContent = text; }
const byHash = (hash) => state.torrents.find((t) => t.hash === hash);

/* ---------- selection ---------- */
function toggleSelect(hash, on) {
  on ? state.selected.add(hash) : state.selected.delete(hash);
  state.focused = hash;
  render();
}
function rangeSelect(a, b) {
  const items = visible().map((t) => t.hash);
  let i = items.indexOf(a), j = items.indexOf(b);
  if (i < 0 || j < 0) return;
  if (i > j) [i, j] = [j, i];
  items.slice(i, j + 1).forEach((x) => state.selected.add(x));
  render();
}
function renderBulk() {
  const n = state.selected.size;
  $("#bulk").hidden = n === 0;
  $("#bulk-count").textContent = n + " selected";
}
$("#select-all").addEventListener("change", (e) => {
  const vis = visible().map((t) => t.hash);
  if (e.target.checked) vis.forEach((x) => state.selected.add(x));
  else vis.forEach((x) => state.selected.delete(x));
  render();
});
for (const b of $$("#bulk [data-bulk]")) {
  b.addEventListener("click", () => {
    const hashes = [...state.selected];
    if (b.dataset.bulk === "remove") confirmRemove(hashes);
    else if (b.dataset.bulk === "category") openCategoryDialog(hashes);
    else if (b.dataset.bulk === "move") moveTo(hashes);
    else act(b.dataset.bulk, hashes);
  });
}
for (const b of $$(".list-head .col[data-sort]")) {
  b.addEventListener("click", () => {
    const key = b.dataset.sort;
    state.sort = state.sort.key === key ? { key, dir: -state.sort.dir } : { key, dir: key === "name" ? 1 : -1 };
    localStorage.setItem("mg.sort", JSON.stringify(state.sort));
    renderList();
  });
}
$("#search").addEventListener("input", (e) => { state.query = e.target.value; renderList(); });

/* ---------- actions ---------- */
async function act(action, hashes, extra = {}) {
  try {
    await api("/api/actions", { method: "POST", body: { action, hashes, ...extra } });
    if (action === "recheck") toast("Verifying data…");
  } catch (e) { fail(e); }
}
function confirmRemove(hashes) {
  const dlg = $("#dlg-remove");
  const names = hashes.map((x) => byHash(x)?.name).filter(Boolean);
  $("#rm-title").textContent = hashes.length > 1 ? `Remove ${hashes.length} torrents?` : "Remove torrent?";
  $("#rm-text").textContent = names.length === 1 ? names[0] : names.slice(0, 3).join(", ") + (names.length > 3 ? "…" : "");
  $("#rm-data").checked = false;
  dlg.returnValue = "";
  dlg.onclose = async () => {
    if (dlg.returnValue !== "ok") return;
    await act("remove", hashes, { deleteData: $("#rm-data").checked });
    hashes.forEach((x) => state.selected.delete(x));
    toast(hashes.length > 1 ? `Removed ${hashes.length} torrents` : "Torrent removed");
  };
  dlg.showModal();
}

function contextMenu(e, hash) {
  const t = byHash(hash);
  const menu = $("#ctx");
  const targets = state.selected.has(hash) ? [...state.selected] : [hash];
  const item = (label, ic, fn, cls) => h("button", { class: cls, onclick: () => { hideMenu(); fn(); } }, icon(ic), h("span", { text: label }));
  menu.replaceChildren(
    t.state === "paused" ? item("Resume", "play", () => act("resume", targets)) : item("Pause", "pause", () => act("pause", targets)),
    t.forced ? item("Stop forcing (follow the queue)", "clock", () => act("unforce", targets))
      : item("Force start", "bolt", () => act("forceStart", targets)),
    item("Verify data", "refresh", () => act("recheck", targets)),
    item(t.sequential ? "Disable sequential" : "Download sequentially", "down", () => setSequential(hash, !t.sequential)),
    item("Category…", "tag", () => openCategoryDialog(targets)),
    item("Play", "play", () => playTorrent(hash)),
    item("Open folder", "folder", () => openFolder(hash)),
    item("Move files to…", "move", () => moveTo(targets)),
    item("Copy magnet link", "copy", () => copyMagnet(hash)),
    item("Details", "file", () => openDetail(hash)),
    h("hr"),
    h("div", { class: "ctx-row" },
      h("span", { class: "muted", text: "Queue" }),
      ...[["queueTop", "Move to top", "⤒"], ["queueUp", "Move up", "↑"], ["queueDown", "Move down", "↓"], ["queueBottom", "Move to bottom", "⤓"]]
        .map(([a, title, glyph]) => h("button", { class: "btn icon sm", title, "aria-label": title, text: glyph, onclick: () => { hideMenu(); act(a, targets); } }))),
    h("hr"),
    item("Remove…", "trash", () => confirmRemove(targets), "danger"),
  );
  menu.hidden = false;
  const { innerWidth: W, innerHeight: H } = window;
  const r = menu.getBoundingClientRect();
  menu.style.left = Math.min(e.clientX, W - r.width - 8) + "px";
  menu.style.top = Math.min(e.clientY, H - r.height - 8) + "px";
}
function hideMenu() { $("#ctx").hidden = true; }
document.addEventListener("click", (e) => { if (!$("#ctx").contains(e.target)) hideMenu(); });
window.addEventListener("blur", hideMenu);

async function setSequential(hash, enabled) {
  try { await api(`/api/torrents/${hash}/sequential`, { method: "POST", body: { enabled } }); toast(enabled ? "Sequential download on" : "Sequential download off"); }
  catch (e) { fail(e); }
}
async function copyMagnet(hash) {
  try {
    const d = await api(`/api/torrents/${hash}`);
    await navigator.clipboard.writeText(d.magnet || `magnet:?xt=urn:btih:${hash}`);
    toast("Magnet link copied");
  } catch (e) { fail(e); }
}

/* ---------- detail panel ---------- */
function openDetail(hash) {
  state.detail = hash;
  state.focused = hash;
  $("#detail").hidden = false;
  $("#app").classList.add("with-detail");
  renderList();
  loadDetail(true);
}
function closeDetail() {
  state.detail = null;
  $("#detail").hidden = true;
  $("#app").classList.remove("with-detail");
  renderList();
}
$("#d-close").addEventListener("click", closeDetail);
for (const b of $$(".tabs button")) {
  b.addEventListener("click", () => {
    state.tab = b.dataset.tab;
    $$(".tabs button").forEach((x) => x.classList.toggle("active", x === b));
    loadDetail(true);
  });
}

let detailBusy = false;
async function loadDetail(force) {
  if (detailBusy && !force) return;
  const hash = state.detail;
  if (!hash) return;
  detailBusy = true;
  try {
    const d = await api(`/api/torrents/${hash}`);
    if (state.detail !== hash) return;
    renderDetail(d, force);
  } catch (e) { if (e.status === 404) closeDetail(); }
  finally { detailBusy = false; }
}

function renderDetail(d, force) {
  set($("#d-name"), d.name);
  set($("#d-sub"), `${d.size ? bytes(d.size) : "Size unknown"} · ${pct(d.progress)} · ${d.state}`);
  const pm = $("#d-pieces");
  pm.replaceChildren(...(d.pieceMap || []).map((v) => {
    const i = h("i");
    i.style.background = v === 255 ? "var(--amber)" : v === 0 ? "transparent" : `color-mix(in srgb, var(--accent) ${Math.max(25, v)}%, transparent)`;
    return i;
  }));
  pm.hidden = !(d.pieceMap && d.pieceMap.length);

  const paused = d.state === "paused";
  $("#d-actions").replaceChildren(...[
    h("button", { class: "btn ghost sm", onclick: () => act(paused ? "resume" : "pause", [d.hash]) }, icon(paused ? "play" : "pause"), h("span", { text: paused ? "Resume" : "Pause" })),
    h("button", { class: "btn ghost sm", onclick: () => act("recheck", [d.hash]) }, icon("refresh"), h("span", { text: "Verify" })),
    bestMediaFile(d) ? h("button", { class: "btn ghost sm", onclick: () => openPreview(d, bestMediaFile(d)) }, icon("play"), h("span", { text: "Play" })) : null,
    d.files.length ? h("button", { class: "btn ghost sm", onclick: () => openFolder(d.hash) }, icon("folder"), h("span", { text: "Open folder" })) : null,
    h("button", { class: "btn ghost sm", onclick: () => copyMagnet(d.hash) }, icon("copy"), h("span", { text: "Magnet" })),
    h("button", { class: "btn ghost sm danger", onclick: () => confirmRemove([d.hash]) }, icon("trash"), h("span", { text: "Remove" })),
  ].filter(Boolean));

  const body = $("#d-body");
  // Avoid clobbering focused controls (e.g. a select being changed) during live refresh.
  if (!force && body.contains(document.activeElement) && document.activeElement !== body) return;
  const scroll = body.scrollTop;
  body.replaceChildren(TABS[state.tab](d));
  body.scrollTop = scroll;
}

const kv = (pairs) => h("dl", { class: "kv" }, pairs.filter(Boolean).flatMap(([k, v, cls]) => [h("dt", { text: k }), h("dd", { class: cls, text: v ?? "–" })]));
const stat = (v, label) => h("div", { class: "stat" }, h("b", { text: v }), h("span", { text: label }));

const TABS = {
  overview(d) {
    return h("div", {},
      h("div", { class: "stat-grid" },
        stat(rate(d.downRate), "Download"), stat(rate(d.upRate), "Upload"), stat(d.state === "downloading" ? eta(d.eta) : "–", "ETA"),
        stat(bytes(d.downloaded), "Downloaded"), stat(bytes(d.uploaded), "Uploaded"), stat(d.ratio.toFixed(2), "Ratio"),
      ),
      h("label", { class: "switch" },
        h("input", { type: "checkbox", checked: d.sequential, onchange: (e) => setSequential(d.hash, e.target.checked) }), h("span"),
        "Sequential download (preview while downloading)"),
      kv([
        ["Save path", d.savePath, "mono"],
        ["Category", d.category || "-"],
        ["Info hash", d.hash, "mono"],
        ["Pieces", d.numPieces ? `${d.numPieces.toLocaleString()} × ${bytes(d.pieceSize)}` : "–"],
        ["Peers", `${d.peers} connected · ${d.seeds} seeds · ${d.knownPeers} known`],
        ["Added", fmtDate(d.addedAt)],
        ["Completed", fmtDate(d.completedAt)],
        d.createdAt && ["Created", fmtDate(d.createdAt)],
        d.createdBy && ["Created by", d.createdBy],
        d.comment && ["Comment", d.comment],
        ["Private", d.private ? "Yes (DHT/PEX disabled)" : "No"],
        d.error && ["Error", d.error],
      ]),
    );
  },
  files(d) {
    if (!d.files.length) return h("p", { class: "muted", text: d.state === "paused" ? "Resume the torrent to see its files." : "Waiting for metadata…" });
    const ui = fileTreeState(d);
    const filter = h("input", { type: "search", class: "files-filter", placeholder: "Filter files", value: ui.filter, "aria-label": "Filter files" });
    filter.addEventListener("input", () => { ui.filter = filter.value; renderTree(); });
    const all = h("div", { class: "row files-head" },
      h("button", { class: "btn ghost sm", onclick: () => setPrio(d.hash, d.files.map((f) => f.index), 1) }, h("span", { text: "Download all" })),
      h("button", { class: "btn ghost sm", onclick: () => setPrio(d.hash, d.files.map((f) => f.index), 0) }, h("span", { text: "Skip all" })),
      d.files.some((f) => f.path.includes("/")) ? h("button", { class: "btn ghost sm", onclick: () => { ui.collapsed = new Set(); renderTree(); } }, h("span", { text: "Expand all" })) : null,
      d.files.length > 8 ? filter : null,
    );
    const wrap = h("div", { class: "files", role: "tree" });
    const tree = buildFileTree(d.files);
    function renderTree() {
      const q = ui.filter.trim().toLowerCase();
      const rows = [];
      const walk = (node, depth) => {
        for (const dir of node.dirs) {
          const files = dir.allFiles.filter((f) => !q || f.path.toLowerCase().includes(q));
          if (!files.length) continue;
          const open = q || !ui.collapsed.has(dir.path);
          rows.push(folderRow(d, dir, depth, open, () => {
            ui.collapsed.has(dir.path) ? ui.collapsed.delete(dir.path) : ui.collapsed.add(dir.path);
            renderTree();
          }));
          if (open) walk(dir, depth + 1);
        }
        for (const f of node.files) if (!q || f.path.toLowerCase().includes(q)) rows.push(fileRow(d, f, depth));
      };
      walk(tree, 0);
      wrap.replaceChildren(...(rows.length ? rows : [h("p", { class: "muted", text: "No files match." })]));
    }
    renderTree();
    return h("div", {}, all, h("div", { class: "spacer-8" }), wrap);
  },
  peers(d) {
    if (!d.peerList.length) return h("p", { class: "muted", text: d.state === "paused" ? "Torrent is paused." : "No connected peers yet." });
    return h("table", { class: "table" },
      h("thead", {}, h("tr", {}, ["Address", "Client", "Progress", "Down", "Up", "Via"].map((x) => h("th", { text: x })))),
      h("tbody", {}, d.peerList.map((p) => h("tr", {},
        h("td", { class: "mono", text: p.addr, title: p.addr + " (" + p.network + ")" }),
        h("td", { text: p.client || "–", title: p.client || "" }),
        h("td", { text: pct(p.progress) }),
        h("td", { text: rate(p.downRate) }),
        h("td", { text: rate(p.upRate) }),
        h("td", { text: p.source + (p.network.startsWith("udp") ? " · µTP" : "") }),
      ))));
  },
  trackers(d) {
    const ta = h("textarea", { rows: 3, placeholder: "udp://tracker.example:1337/announce", spellcheck: "false" });
    return h("div", {},
      d.trackers.length ? h("ul", { class: "trackers" }, d.trackers.map((t) => h("li", { text: t }))) : h("p", { class: "muted", text: "No trackers - peers are found through DHT and PEX." }),
      h("label", { class: "field" }, h("span", { text: "Add trackers (one per line)" }), ta),
      h("button", {
        class: "btn ghost sm", onclick: async () => {
          const urls = ta.value.split("\n").map((s) => s.trim()).filter(Boolean);
          if (!urls.length) return;
          try { await api(`/api/torrents/${d.hash}/trackers`, { method: "POST", body: { urls } }); toast("Trackers added"); loadDetail(true); }
          catch (e) { fail(e); }
        },
      }, h("span", { text: "Add trackers" })),
    );
  },
};
/* ---------- file tree ---------- */
// Collapsed folders and the filter survive the detail panel's live refresh.
const fileTrees = new Map();
function fileTreeState(d) {
  let ui = fileTrees.get(d.hash);
  if (!ui) {
    ui = { filter: "", collapsed: new Set() };
    // Big torrents start with their sub-folders collapsed.
    if (d.files.length > 60) {
      for (const f of d.files) {
        const parts = f.path.split("/");
        for (let i = 2; i < parts.length; i++) ui.collapsed.add(parts.slice(0, i).join("/"));
      }
    }
    fileTrees.set(d.hash, ui);
  }
  return ui;
}
function buildFileTree(files) {
  const root = { path: "", name: "", dirs: [], files: [], allFiles: [], byName: new Map() };
  for (const f of files) {
    const parts = f.path.split("/");
    let node = root;
    root.allFiles.push(f);
    for (let i = 0; i < parts.length - 1; i++) {
      let next = node.byName.get(parts[i]);
      if (!next) {
        next = { path: parts.slice(0, i + 1).join("/"), name: parts[i], dirs: [], files: [], allFiles: [], byName: new Map() };
        node.byName.set(parts[i], next);
        node.dirs.push(next);
      }
      next.allFiles.push(f);
      node = next;
    }
    node.files.push(f);
  }
  const sort = (n) => {
    n.dirs.sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
    n.files.sort((a, b) => a.path.localeCompare(b.path, undefined, { numeric: true }));
    n.dirs.forEach(sort);
  };
  sort(root);
  return root;
}
function prioSelect(value, onchange, mixed) {
  const sel = h("select", { class: "sm", "aria-label": "Priority" },
    mixed ? h("option", { value: "", text: "Mixed", disabled: true }) : null,
    h("option", { value: "0", text: "Skip" }), h("option", { value: "1", text: "Normal" }), h("option", { value: "2", text: "High" }));
  sel.value = mixed ? "" : String(value);
  sel.addEventListener("change", () => { onchange(+sel.value); sel.blur(); });
  return sel;
}
function progressBar(p) {
  const bar = h("div", { class: "bar" }, h("i"));
  bar.firstChild.style.width = (p * 100).toFixed(1) + "%";
  return bar;
}
function folderRow(d, dir, depth, open, toggle) {
  const files = dir.allFiles;
  const size = files.reduce((a, f) => a + f.size, 0), done = files.reduce((a, f) => a + f.completed, 0);
  const prios = new Set(files.map((f) => f.priority));
  const prio = prios.size === 1 ? [...prios][0] : null;
  const name = h("button", { class: "fold", "aria-expanded": String(open), onclick: toggle },
    h("span", { class: "chev" + (open ? " open" : ""), text: "›" }), icon("folder"), h("span", { text: dir.name }),
    h("span", { class: "muted", text: ` · ${files.length} file${files.length === 1 ? "" : "s"}` }));
  return indent(h("div", { class: "file dir" + (prio === 0 ? " skip" : ""), role: "treeitem" },
    h("div", { class: "fp" }, name),
    h("div", { class: "file-actions" }),
    prioSelect(prio, (v) => setPrio(d.hash, files.map((f) => f.index), v), prio === null),
    h("div", { class: "fm", text: `${bytes(done)} / ${bytes(size)} · ${pct(size ? done / size : 0)}` }), progressBar(size ? done / size : 0)), depth);
}
// Inline style attributes are blocked by the CSP; CSSOM is not.
function indent(el, depth) { el.style.setProperty("--depth", depth); return el; }
function fileRow(d, f, depth) {
  const kind = fileKind(f.path);
  const blocked = BLOCKED_EXT.has(extOf(f.path));
  const acts = h("div", { class: "file-actions" },
    kind === "video" || kind === "audio" ? h("button", { class: "btn icon", title: "Play", onclick: () => openPreview(d, f) }, icon("play")) : null,
    kind === "image" || kind === "text" ? h("button", { class: "btn icon", title: "Quick look", onclick: () => openPreview(d, f) }, icon("eye")) : null,
    f.progress >= 1 && !blocked ? h("button", { class: "btn icon", title: "Open", onclick: () => fileAction(d.hash, f.index, "open") }, icon("external")) : null,
    f.completed > 0 ? h("button", { class: "btn icon", title: "Show in folder", onclick: () => fileAction(d.hash, f.index, "reveal") }, icon("folder")) : null);
  return indent(h("div", { class: "file" + (f.priority === 0 ? " skip" : ""), role: "treeitem" },
    h("div", { class: "fp", text: f.path.split("/").pop(), title: f.path }), acts,
    prioSelect(f.priority, (v) => setPrio(d.hash, [f.index], v)),
    h("div", { class: "fm", text: `${bytes(f.completed)} / ${bytes(f.size)} · ${pct(f.progress)}` }), progressBar(f.progress)), depth);
}
async function setPrio(hash, indices, priority) {
  try { await api(`/api/torrents/${hash}/files`, { method: "POST", body: { indices, priority } }); loadDetail(true); }
  catch (e) { fail(e); }
}

/* ---------- add torrents ---------- */
// A .torrent file opened from the desktop arrives as "preview:<hash>": the
// server has already read it, so the dialog shows its files and asks first.
const PREVIEW_RE = /^preview:([0-9a-f]{40})$/i;
async function openAdd(prefill = "", fromLink = false) {
  if (!state.settings) { try { state.settings = await api("/api/settings"); } catch (e) { return fail(e); } }
  const dlg = $("#dlg-add");
  const pm = PREVIEW_RE.exec(prefill);
  if (pm) {
    if (dlg.open) dlg.close();
    state.filePreview = pm[1].toLowerCase();
    setLinkMode(true, "", true);
    $("#add-magnets").value = "";
    $("#add-path").value = state.settings.downloadDir;
    state.pendingFiles = [];
    renderPending();
    dlg.showModal();
    $("#add-submit").focus();
    startPreview();
    updateFree(true);
    return;
  }
  if (!dlg.open) {
    setLinkMode(fromLink && /^magnet:\?/i.test(prefill), prefill);
    $("#add-magnets").value = prefill;
    $("#add-path").value = state.settings.downloadDir;
    state.pendingFiles = [];
    renderPending();
    dlg.showModal();
    (prefill ? $("#add-submit") : $("#add-magnets")).focus();
  } else if (prefill) {
    setLinkMode(false);
    $("#add-magnets").value = ($("#add-magnets").value.trim() + "\n" + prefill).trim();
  }
  startPreview(); // no debounce when opening: show the file list as soon as possible
  updateFree(true);
}
/* ---------- add dialog opened from a magnet link ---------- */
// Like qBittorrent: show what will be downloaded, not the raw magnet link.
function magnetName(link) {
  try { return new URLSearchParams(link.replace(/^magnet:\?/i, "")).get("dn") || ""; } catch { return ""; }
}
function setLinkMode(on, link, fromFile = false) {
  const dlg = $("#dlg-add");
  $("#as-show-link").hidden = fromFile;
  dlg.classList.toggle("link-mode", on);
  dlg.classList.remove("show-link");
  $("#add-summary").hidden = !on;
  $("#add-title").textContent = on ? "Add torrent" : "Add torrents";
  $("#as-show-link").textContent = "Show magnet link";
  if (on) {
    $("#as-name").textContent = (link && magnetName(link)) || "Torrent";
    $("#as-meta").textContent = "Loading details…";
  }
}
function updateLinkSummary() {
  if (!$("#dlg-add").classList.contains("link-mode")) return;
  const i = pv.info;
  if (!i) return;
  if (i.name) $("#as-name").textContent = i.name;
  $("#as-meta").textContent = i.ready
    ? `${bytes(i.size)} in ${i.files.length} file${i.files.length === 1 ? "" : "s"}`
    : i.error ? i.error : (i.peers ? `Fetching details from ${i.peers} peers…` : "Looking for peers…");
}
$("#as-show-link").addEventListener("click", () => {
  const dlg = $("#dlg-add");
  dlg.classList.toggle("show-link");
  $("#as-show-link").textContent = dlg.classList.contains("show-link") ? "Hide magnet link" : "Show magnet link";
});

function recentPaths() { try { return JSON.parse(localStorage.getItem("mg.paths") || "[]"); } catch { return []; } }
function rememberPath(p) {
  if (!p) return;
  const list = [p, ...recentPaths().filter((x) => x !== p)].slice(0, 8);
  localStorage.setItem("mg.paths", JSON.stringify(list));
  fillRecent();
}
function fillRecent() {
  $("#recent-paths").replaceChildren(...recentPaths().map((p) => h("option", { value: p })));
}
fillRecent();

function renderPending() {
  $("#add-file-list").replaceChildren(...state.pendingFiles.map((f) => h("li", { text: "📄 " + f.name })));
}
function addFiles(files) {
  for (const f of files) {
    if (f.name.toLowerCase().endsWith(".torrent") || f.type === "application/x-bittorrent") state.pendingFiles.push(f);
  }
  renderPending();
  schedulePreview();
}
$("#btn-add").addEventListener("click", () => openAdd());
$("#btn-add-empty").addEventListener("click", () => openAdd());
const dz = $("#dropzone");
dz.addEventListener("click", () => $("#add-files").click());
dz.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); $("#add-files").click(); } });
$("#add-files").addEventListener("change", (e) => { addFiles(e.target.files); e.target.value = ""; });
dz.addEventListener("dragover", (e) => { e.preventDefault(); dz.classList.add("over"); });
dz.addEventListener("dragleave", () => dz.classList.remove("over"));
dz.addEventListener("drop", (e) => { e.preventDefault(); e.stopPropagation(); dz.classList.remove("over"); addFiles(e.dataTransfer.files); });

/* ---------- add dialog: preview & file selection ---------- */
// With exactly one magnet or one .torrent file, fetch its file list so the
// user can choose files and see whether it fits before adding.
const pv = { key: null, hash: null, info: null, skip: new Set(), timer: null, poll: null, used: false, free: null };

function previewSource() {
  if (state.filePreview) return { kind: "prepared", key: "prepared:" + state.filePreview, hash: state.filePreview };
  const mags = $("#add-magnets").value.split("\n").map((s) => s.trim()).filter(Boolean);
  if (mags.length === 1 && !state.pendingFiles.length) return { kind: "magnet", key: mags[0], magnet: mags[0] };
  if (!mags.length && state.pendingFiles.length === 1) {
    const f = state.pendingFiles[0];
    return { kind: "file", key: `file:${f.name}:${f.size}:${f.lastModified}`, file: f };
  }
  return null;
}
function schedulePreview() {
  clearTimeout(pv.timer);
  pv.timer = setTimeout(startPreview, 450);
}
function dropPreview() {
  clearInterval(pv.poll);
  if (pv.hash && !pv.used) api(`/api/preview/${pv.hash}`, { method: "DELETE" }).catch(() => {});
  Object.assign(pv, { key: null, hash: null, info: null, skip: new Set(), poll: null, used: false });
}
async function startPreview() {
  const src = previewSource();
  if (!src) { dropPreview(); renderPreview(); return; }
  if (src.key === pv.key) return;
  dropPreview();
  pv.key = src.key;
  renderPreview("loading");
  try {
    let info;
    if (src.kind === "prepared") {
      info = await api(`/api/preview/${src.hash}`);
    } else if (src.kind === "file") {
      const fd = new FormData();
      fd.append("torrent", src.file, src.file.name);
      info = await api("/api/preview", { method: "POST", form: fd });
    } else {
      info = await api("/api/preview", { method: "POST", body: { magnet: src.magnet } });
    }
    if (pv.key !== src.key) return;
    pv.hash = info.hash;
    pv.info = info;
    renderPreview();
    if (!info.ready) {
      pv.poll = setInterval(async () => {
        try {
          const i = await api(`/api/preview/${pv.hash}`);
          if (pv.key !== src.key) return;
          pv.info = i;
          if (i.ready || i.error) clearInterval(pv.poll);
          renderPreview();
        } catch { clearInterval(pv.poll); }
      }, 1000);
    }
  } catch (e) {
    if (pv.key !== src.key) return;
    renderPreview(e.status === 409 ? "exists" : "error", e.message);
  }
}
function renderPreview(mode, msg) {
  const box = $("#add-preview"), list = $("#ap-list");
  if (!pv.key) { box.hidden = true; updateFree(); return; }
  box.hidden = false;
  const i = pv.info;
  const waiting = (text) => list.replaceChildren(h("div", { class: "ap-wait" }, h("span", { class: "spinner" }), h("span", { text })));
  $("#ap-all").hidden = $("#ap-none").hidden = !(i && i.ready);
  if (mode === "loading") { $("#ap-summary").textContent = ""; return waiting("Preparing…"); }
  if (mode === "exists") { $("#ap-summary").textContent = ""; return list.replaceChildren(h("div", { class: "ap-wait", text: "This torrent is already in your list." })); }
  if (mode === "error" || (i && i.error)) { $("#ap-summary").textContent = ""; return list.replaceChildren(h("div", { class: "ap-wait error", text: msg || i.error })); }
  updateLinkSummary();
  if (!i.ready) {
    $("#ap-summary").textContent = i.name || "";
    return waiting(i.peers ? `Fetching the file list from ${i.peers} peers… You can also add it right away.` : "Looking for peers to fetch the file list… You can also add it right away.");
  }
  const files = [...i.files].sort((a, b) => a.path.localeCompare(b.path));
  list.replaceChildren(...files.slice(0, 3000).map((f) => {
    const on = !pv.skip.has(f.index);
    const cb = h("input", { type: "checkbox", checked: on, "aria-label": f.path });
    const slash = f.path.lastIndexOf("/");
    const row = h("label", { class: "ap-row" + (on ? "" : " off"), title: f.path },
      cb,
      h("span", { class: "ap-path" }, slash >= 0 ? h("span", { class: "ap-dir", text: f.path.slice(0, slash + 1) }) : null, f.path.slice(slash + 1)),
      h("span", { class: "ap-size", text: bytes(f.size) }));
    cb.addEventListener("change", () => {
      cb.checked ? pv.skip.delete(f.index) : pv.skip.add(f.index);
      row.classList.toggle("off", !cb.checked);
      previewSummary();
    });
    return row;
  }));
  previewSummary();
}
function selectedSize() {
  const i = pv.info;
  if (!i || !i.ready) return null;
  return i.files.filter((f) => !pv.skip.has(f.index)).reduce((a, f) => a + f.size, 0);
}
function previewSummary() {
  const i = pv.info;
  const n = i.files.length - [...pv.skip].filter((x) => x < i.files.length).length;
  $("#ap-summary").textContent = `${n} of ${i.files.length} files · ${bytes(selectedSize())} of ${bytes(i.size)}`;
  $("#add-submit").disabled = n === 0;
  updateFree(true);
}
let freeTimer = null;
function updateFree(now) {
  clearTimeout(freeTimer);
  const run = async () => {
    const path = $("#add-path").value.trim();
    const el = $("#add-free");
    if (!path) { el.textContent = ""; return; }
    try {
      const { free } = await api("/api/fs/free?path=" + encodeURIComponent(path));
      const need = selectedSize();
      el.textContent = `${bytes(free)} free` + (need != null ? ` · needs ${bytes(need)}` : "");
      el.classList.toggle("low", need != null && need > free);
    } catch { el.textContent = ""; }
  };
  if (now === true) run(); else freeTimer = setTimeout(run, 250);
}
$("#ap-all").addEventListener("click", () => { pv.skip.clear(); renderPreview(); });
$("#ap-none").addEventListener("click", () => { pv.info.files.forEach((f) => pv.skip.add(f.index)); renderPreview(); });
$("#add-magnets").addEventListener("input", schedulePreview);
$("#add-path").addEventListener("input", () => updateFree());
$("#dlg-add").addEventListener("close", () => { state.filePreview = null; clearTimeout(pv.timer); dropPreview(); renderPreview(); $("#add-submit").disabled = false; setLinkMode(false); });

$("#add-form").addEventListener("submit", async (ev) => {
  if (ev.submitter && ev.submitter.value === "cancel") return;
  ev.preventDefault();
  const magnets = $("#add-magnets").value.trim();
  if (!magnets && !state.pendingFiles.length && !state.filePreview) { toast("Add a magnet link or a .torrent file", "err"); return; }
  const src = previewSource();
  if (pv.hash && src && src.key === pv.key) {
    $("#add-submit").disabled = true;
    try {
      const res = await api("/api/torrents", { method: "POST", body: {
        previewHash: pv.hash, skipFiles: [...pv.skip], savePath: $("#add-path").value.trim(), category: $("#add-cat").value,
        paused: $("#add-paused").checked, sequential: $("#add-seq").checked } });
      pv.used = true;
      const r = res[0] || {};
      if (r.error) toast(r.error.includes("already") ? "Already added" : r.error, "err");
      else toast("Torrent added");
      rememberPath($("#add-path").value.trim());
      $("#dlg-add").close();
    } catch (e) { fail(e); }
    finally { $("#add-submit").disabled = false; }
    return;
  }
  const fd = new FormData();
  fd.append("magnets", magnets);
  fd.append("savePath", $("#add-path").value.trim());
  fd.append("paused", $("#add-paused").checked);
  fd.append("sequential", $("#add-seq").checked);
  fd.append("category", $("#add-cat").value);
  state.pendingFiles.forEach((f) => fd.append("torrents", f, f.name));
  $("#add-submit").disabled = true;
  try {
    const res = await api("/api/torrents", { method: "POST", form: fd });
    const ok = res.filter((r) => !r.error).length;
    res.filter((r) => r.error).forEach((r) => toast(r.error.includes("already") ? "Already added" : r.error, "err"));
    if (ok) toast(ok === 1 ? "Torrent added" : `${ok} torrents added`);
    rememberPath($("#add-path").value.trim());
    $("#dlg-add").close();
  } catch (e) { fail(e); }
  finally { $("#add-submit").disabled = false; }
});

// Paste a magnet anywhere.
document.addEventListener("paste", (e) => {
  const tag = (e.target.tagName || "").toLowerCase();
  if (tag === "input" || tag === "textarea") return;
  const text = e.clipboardData.getData("text").trim();
  if (/^magnet:\?/i.test(text) || /^[a-f0-9]{40}$/i.test(text)) { e.preventDefault(); openAdd(text); }
});

// Drop .torrent files anywhere.
let dragDepth = 0;
const hasFiles = (e) => [...(e.dataTransfer?.types || [])].includes("Files");
window.addEventListener("dragenter", (e) => { if (!hasFiles(e) || $("#dlg-add").open) return; dragDepth++; $("#drop-overlay").hidden = false; });
window.addEventListener("dragleave", () => { if (--dragDepth <= 0) { dragDepth = 0; $("#drop-overlay").hidden = true; } });
window.addEventListener("dragover", (e) => { if (hasFiles(e)) e.preventDefault(); });
window.addEventListener("drop", (e) => {
  if (!hasFiles(e)) return;
  e.preventDefault(); dragDepth = 0; $("#drop-overlay").hidden = true;
  openAdd().then(() => addFiles(e.dataTransfer.files));
});

// Magnet links handed over via URL (?add= from protocol handler, #add= from launch).
function handleIncomingLinks() {
  const q = new URLSearchParams(location.search).get("add");
  const hash = location.hash;
  const hm = hash.startsWith("#add=") ? decodeURIComponent(hash.slice(5)) : null;
  const link = q || hm;
  history.replaceState(null, "", "/");
  if (link && /^magnet:\?/i.test(link)) openAdd(link);
  else if (hash.startsWith("#popup-add=")) {
    openAdd(decodeURIComponent(hash.slice(11)), true);
  }
  else if (/^#t=[0-9a-f]{40}(:\w+)?$/i.test(hash)) {
    const [h, tab] = hash.slice(3).toLowerCase().split(":");
    if (tab && TABS[tab]) { state.tab = tab; $$(".tabs button").forEach((x) => x.classList.toggle("active", x.dataset.tab === tab)); }
    openDetail(h);
  }
  else if (hash === "#settings") openSettings();
  else if (hash.startsWith("#settings:")) openSettings().then(() => $(`.settings-nav [data-sec="${hash.slice(10)}"]`)?.click());
  else if (hash === "#speed") openSpeed();
  else if (hash === "#stats") openStatistics();
  else if (hash === "#rss") openRSS();
  else if (hash === "#create") openCreate();
  else if (hash === "#import") openImport();
  else if (hash === "#rss:rules") openRSS("rules");
  else if (/^#play=[0-9a-f]{40}$/i.test(hash)) playTorrent(hash.slice(6).toLowerCase());
  else if (hash === "#add") openAdd();
}

/* ---------- settings ---------- */
const NUMERIC = new Set(["altScheduleDays", "seedTimeLimitMin", "listenPort", "webPort", "maxConnsPerTorrent", "maxHalfOpenConns", "downloadLimitKBps", "uploadLimitKBps", "altDownloadLimitKBps", "altUploadLimitKBps", "pieceHashers", "seedRatioLimit", "proxyPort", "maxActiveDownloads", "maxActiveUploads", "maxActiveTorrents"]);
async function openSettings() {
  try { state.settings = await api("/api/settings"); } catch (e) { return fail(e); }
  state.settings.proxyPassword = "";
  $("#watch-cat").replaceChildren(h("option", { value: "", text: "No category" }),
    ...(state.settings.categories || []).map((c) => h("option", { value: c.name, text: c.name })));
  await fillInterfaces(state.settings.bindInterface);
  const form = $("#settings-form");
  for (const el of form.elements) {
    if (!el.name || !(el.name in state.settings)) continue;
    if (el.type === "checkbox") el.checked = !!state.settings[el.name];
    else el.value = state.settings[el.name] ?? "";
  }
  $("#pw-state").textContent = state.settings.passwordSet ? "A password is set." : "No password set - the UI only accepts one-time links from this computer.";
  $("#pw-current").hidden = !state.settings.passwordSet;
  $("#pw-remove").hidden = !state.settings.passwordSet;
  $("#settings-err").textContent = "";
  refreshRates($("#settings-form"));
  renderCatEditor(state.settings.categories || []);
  renderDays();
  renderRemote();
  $("#qbit-port").textContent = location.port || state.settings.webPort;
  $('#settings-form [name="qbitAPI"]').disabled = !state.settings.passwordSet;
  $("#proxy-pass").placeholder = state.settings.proxyPasswordSet ? "Saved - leave empty to keep" : "";
  syncProxy();
  $("#dlg-settings").showModal();
}
$("#btn-settings").addEventListener("click", openSettings);
async function fillInterfaces(current) {
  let ifs = [];
  try { ifs = await api("/api/net/interfaces"); } catch { /* keep the list empty */ }
  const sel = $("#bind-iface");
  const opts = [h("option", { value: "", text: "Any connection" })];
  for (const i of ifs) opts.push(h("option", { value: i.name, text: `${i.name} - ${i.addresses.join(", ")}${i.up ? "" : " (down)"}` }));
  if (current && !ifs.some((i) => i.name === current)) opts.push(h("option", { value: current, text: `${current} (not connected)` }));
  sel.replaceChildren(...opts);
}
function syncProxy() {
  const t = $("#proxy-type").value;
  $("#proxy-fields").hidden = !t;
  $("#proxy-peers-row").hidden = t !== "socks5";
  const peers = t === "socks5" && $('#settings-form [name="proxyPeers"]').checked;
  $("#proxy-note").textContent = !t ? "" : peers
    ? "Trackers and peers go through the proxy. DHT, µTP and UDP trackers are turned off because they cannot be proxied, and incoming connections will mostly fail."
    : "Trackers and web seeds go through the proxy. Peers still connect directly" + (t === "socks5" ? " unless you turn on the switch above." : " (HTTP proxies cannot carry peer connections).");
}
$("#proxy-type").addEventListener("change", syncProxy);
$('#settings-form [name="proxyPeers"]').addEventListener("change", syncProxy);
for (const b of $$(".settings-nav button")) {
  b.addEventListener("click", () => {
    $$(".settings-nav button").forEach((x) => x.classList.toggle("active", x === b));
    $$(".settings-body section").forEach((s) => s.classList.toggle("active", s.dataset.sec === b.dataset.sec));
  });
}
$("#settings-form").addEventListener("submit", async (ev) => {
  if (ev.submitter && ev.submitter.value === "cancel") return;
  ev.preventDefault();
  const body = {};
  for (const el of ev.target.elements) {
    if (!el.name || !(el.name in state.settings)) continue;
    body[el.name] = el.type === "checkbox" ? el.checked : NUMERIC.has(el.name) ? Number(el.value || 0) : el.value.trim();
  }
  body.categories = readCatEditor();
  try {
    const res = await api("/api/settings", { method: "PUT", body });
    $("#dlg-settings").close();
    state.settings = null;
    loadCategories();
    toast(res.restartRequired ? "Saved - restart Magnetar to apply web interface changes" : "Settings saved");
  } catch (e) { $("#settings-err").textContent = e.message; }
});
$("#pw-save").addEventListener("click", async () => {
  try {
    await api("/api/password", { method: "POST", body: { current: $("#pw-current").value, new: $("#pw-new").value } });
    $("#pw-current").value = $("#pw-new").value = "";
    toast("Password saved");
    openSettings();
  } catch (e) { fail(e); }
});
$("#pw-remove").addEventListener("click", async () => {
  try {
    await api("/api/password", { method: "POST", body: { current: $("#pw-current").value, remove: true } });
    $("#pw-current").value = "";
    toast("Password removed");
    openSettings();
  } catch (e) { fail(e); }
});
$("#btn-handler").addEventListener("click", () => {
  try {
    navigator.registerProtocolHandler("magnet", location.origin + "/?add=%s");
    toast("Your browser will ask to confirm");
  } catch (e) { fail(e); }
});
/* ---------- updates ---------- */
function updateStatusText(u) {
  if (!u) return "Update information is not available.";
  if (u.state === "checking") return "Checking for updates…";
  if (u.state === "downloading") return `Downloading Magnetar ${u.latest} and verifying its signature…`;
  if (u.state === "installing") return `Installing Magnetar ${u.latest} - Magnetar will restart…`;
  if (u.state === "error") return "Update failed: " + u.error;
  if (u.available) return `Magnetar ${u.latest} is available (you have ${u.current}).`;
  if (u.checkedAt) return `You have the latest version (${u.current}). Last checked ${new Date(u.checkedAt).toLocaleString()}.`;
  return `You have Magnetar ${u.current}.`;
}
function renderUpdate() {
  const u = state.update;
  const pill = $("#update-pill");
  const busy = u && (u.state === "downloading" || u.state === "installing");
  pill.hidden = !(u && (u.available || busy));
  pill.classList.toggle("busy", !!busy);
  if (u) {
    pill.querySelector("span").textContent = busy ? (u.state === "downloading" ? "Downloading update…" : "Installing…") : `Update to ${u.latest}`;
    $("#update-status").textContent = updateStatusText(u);
    if ($("#dlg-update").open) fillUpdateDialog();
  }
}
function fillUpdateDialog() {
  const u = state.update;
  $("#upd-title").textContent = `Magnetar ${u.latest} is available`;
  $("#upd-sub").textContent = `You have ${u.current}.` + (u.publishedAt ? ` Released ${new Date(u.publishedAt).toLocaleDateString()}.` : "");
  $("#upd-notes").textContent = (u.notes || "No release notes.").replace(/\r/g, "").trim();
  $("#upd-link").href = u.url || "https://github.com/nikolap994/magnetar/releases/latest";
  const busy = u.state === "downloading" || u.state === "installing";
  $("#upd-install").disabled = !u.canInstall || busy;
  $("#upd-hint").textContent = busy ? updateStatusText(u)
    : u.canInstall ? "The download is verified against Magnetar's signing key before it is installed. Transfers resume after the restart."
    : "This installation cannot update itself - download the new version from the release page.";
  $("#upd-err").textContent = u.state === "error" ? u.error : "";
}
$("#update-pill").addEventListener("click", () => {
  const u = state.update;
  if (!u || !u.available || u.state === "downloading" || u.state === "installing") return;
  fillUpdateDialog();
  $("#dlg-update").returnValue = "";
  $("#dlg-update").showModal();
});
$("#update-form").addEventListener("submit", async (ev) => {
  if (!ev.submitter || ev.submitter.value !== "install") return;
  ev.preventDefault();
  try {
    await api("/api/update/install", { method: "POST" });
    $("#dlg-update").close();
    toast("Downloading update - Magnetar will restart when it is ready");
  } catch (e) { $("#upd-err").textContent = e.message; }
});
$("#btn-check-update").addEventListener("click", async () => {
  $("#update-status").textContent = "Checking for updates…";
  try {
    state.update = await api("/api/update/check", { method: "POST" });
    renderUpdate();
    toast(state.update.available ? `Magnetar ${state.update.latest} is available` : "You have the latest version");
  } catch (e) { $("#update-status").textContent = e.message; fail(e); }
});

$("#btn-register").addEventListener("click", async () => {
  try { await api("/api/system/register", { method: "POST" }); toast("Magnetar now opens magnet links and .torrent files"); }
  catch (e) { fail(e); }
});

/* ---------- folder picker ---------- */
let folderTarget = null;
let folderFiles = false; // also list files (choosing what to share in a new torrent)
async function browse(path) {
  $("#folder-err").textContent = "";
  try {
    const d = await api("/api/fs/dirs?path=" + encodeURIComponent(path || "") + (folderFiles ? "&files=1" : ""));
    $("#folder-path").value = d.path;
    $("#folder-roots").replaceChildren(...d.roots.map((r) => h("button", { type: "button", title: r, text: r, onclick: () => browse(r) })));
    const items = [];
    if (d.parent) items.push(h("button", { type: "button", class: "up", onclick: () => browse(d.parent) }, icon("up"), h("span", { text: ".." })));
    for (const name of d.dirs) {
      items.push(h("button", { type: "button", onclick: () => browse(d.path + (d.path.endsWith(d.sep) ? "" : d.sep) + name) }, icon("folder"), h("span", { text: name })));
    }
    for (const name of d.files || []) {
      const full = d.path + (d.path.endsWith(d.sep) ? "" : d.sep) + name;
      items.push(h("button", { type: "button", class: "file-pick", onclick: (e) => {
        $$("#folder-list .picked").forEach((x) => x.classList.toggle("picked", false));
        e.currentTarget.classList.add("picked");
        $("#folder-path").value = full;
      } }, icon("file"), h("span", { text: name })));
    }
    if (!d.dirs.length && !(d.files || []).length) items.push(h("p", { class: "muted", text: folderFiles ? "This folder is empty." : "No sub-folders. Use this folder, or type a new path - it will be created." }));
    $("#folder-list").replaceChildren(...items);
  } catch (e) { $("#folder-err").textContent = e.message; }
}
let folderCallback = null;
function pickFolder(initial, cb, withFiles = false) {
  folderTarget = null;
  folderCallback = cb;
  folderFiles = withFiles;
  $("#folder-title").textContent = withFiles ? "Choose a file or folder" : "Choose folder";
  $("#folder-ok").textContent = withFiles ? "Use selected" : "Use this folder";
  const dlg = $("#dlg-folder");
  dlg.returnValue = "";
  dlg.showModal();
  browse(initial || "");
}
function bindBrowse(b) {
  b.addEventListener("click", () => {
    folderCallback = null;
    folderFiles = false;
    $("#folder-title").textContent = "Choose folder";
    $("#folder-ok").textContent = "Use this folder";
    folderTarget = $(b.dataset.browse) || b.closest(".path-row")?.querySelector("input");
    const dlg = $("#dlg-folder");
    dlg.returnValue = "";
    dlg.showModal();
    browse(folderTarget.value.trim());
  });
}
$$("[data-browse]").forEach(bindBrowse);
$("#folder-go").addEventListener("click", () => browse($("#folder-path").value.trim()));
$("#folder-path").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); browse(e.target.value.trim()); } });
$("#dlg-folder").addEventListener("close", () => {
  if ($("#dlg-folder").returnValue !== "ok") return;
  const picked = $("#folder-path").value.trim();
  if (folderCallback) { const cb = folderCallback; folderCallback = null; cb(picked); return; }
  if (folderTarget) { folderTarget.value = picked; folderTarget.dispatchEvent(new Event("input")); }
});

/* ---------- speed schedule days ---------- */
const dayButtons = () => document.querySelectorAll("#sched-days button");
function renderDays() {
  const mask = Number(document.getElementById("sched-days-value").value || 0);
  dayButtons().forEach((b) => b.classList.toggle("on", (mask & (1 << Number(b.dataset.day))) !== 0));
}
dayButtons().forEach((b) => b.addEventListener("click", () => {
  const v = document.getElementById("sched-days-value");
  v.value = String(Number(v.value || 0) ^ (1 << Number(b.dataset.day)));
  renderDays();
}));

/* ---------- previews: play, quick look, open ---------- */
const MEDIA_EXT = { video: ["mp4", "m4v", "webm", "mkv", "mov", "ogv"], audio: ["mp3", "m4a", "aac", "flac", "ogg", "oga", "opus", "wav"],
  image: ["png", "jpg", "jpeg", "gif", "webp", "avif", "bmp"], text: ["txt", "nfo", "md", "log", "srt", "vtt", "ass", "ssa", "cue", "sfv", "md5", "sha256"] };
const BLOCKED_EXT = new Set(["exe", "com", "bat", "cmd", "msi", "msp", "msix", "appx", "scr", "pif", "cpl", "ps1", "psm1", "vbs", "vbe", "js", "jse", "wsf", "wsh", "hta", "lnk", "url", "reg", "jar", "dll", "sys", "inf", "sh", "bash", "zsh", "run", "bin", "appimage", "desktop", "deb", "rpm", "py", "pl", "rb", "command", "app", "dmg", "pkg", "iso", "img", "vhd", "vhdx", "html", "htm", "svg", "xhtml", "mht", "chm"]);
const extOf = (p) => (p.split("/").pop().split(".").length > 1 ? p.split(".").pop().toLowerCase() : "");
function fileKind(p) {
  const e = extOf(p);
  return Object.keys(MEDIA_EXT).find((k) => MEDIA_EXT[k].includes(e)) || "";
}
const contentURL = (hash, i, extra = "") => `/api/torrents/${hash}/files/${i}/content${extra}`;
let playerCtx = null;

function bestMediaFile(d) {
  const media = d.files.filter((f) => ["video", "audio"].includes(fileKind(f.path)) && f.priority !== 0);
  return media.sort((a, b) => b.size - a.size)[0];
}
function stopPlayer() {
  const el = $("#pl-stage").querySelector("video, audio");
  if (el) { el.pause(); el.removeAttribute("src"); el.load(); }
  $("#pl-stage").replaceChildren();
}
function playerMessage(text) {
  $("#pl-stage").replaceChildren(h("div", { class: "player-msg", text }));
}
function openPreview(d, f) {
  const kind = fileKind(f.path);
  if (!kind) return;
  if (d.state === "paused" || d.state === "metadata") { toast("Resume the torrent to preview its files", "err"); return; }
  playerCtx = { hash: d.hash, file: f };
  stopPlayer();
  $("#pl-title").textContent = f.path.split("/").pop();
  $("#pl-open").hidden = f.progress < 1 || BLOCKED_EXT.has(extOf(f.path));
  const src = contentURL(d.hash, f.index);
  if (kind === "video" || kind === "audio") {
    const el = h(kind, { controls: true, autoplay: true, preload: "auto" });
    if (kind === "video") {
      // Subtitles next to the video (same folder) become selectable tracks.
      const dir = f.path.includes("/") ? f.path.slice(0, f.path.lastIndexOf("/") + 1) : "";
      const subs = d.files.filter((s) => ["srt", "vtt"].includes(extOf(s.path)) && s.path.startsWith(dir) && s.progress === 1).slice(0, 12);
      subs.forEach((s, i) => {
        const base = s.path.split("/").pop();
        const lang = (base.match(/\.([a-z]{2,3})\.(srt|vtt)$/i) || [])[1] || base;
        el.append(h("track", { kind: "subtitles", label: lang, srclang: lang.length <= 3 ? lang : undefined,
          src: contentURL(d.hash, s.index, extOf(s.path) === "srt" ? "?format=vtt" : "") }));
      });
    }
    el.addEventListener("error", () => playerMessage(
      "This file can't be played inside Magnetar (its format or codec isn't supported by the built-in player)." +
      (f.progress < 1 ? " Wait until it finishes, then open it in your system player." : " Open it in your system player instead.")));
    el.src = src;
    $("#pl-stage").replaceChildren(el);
    $("#pl-status").textContent = f.progress < 1 ? `Streaming while downloading - ${pct(f.progress)} of this file is ready; playing fetches the next parts first.` : "";
  } else if (kind === "image") {
    const img = h("img", { alt: f.path });
    img.addEventListener("error", () => playerMessage("The image is not ready yet."));
    img.src = src;
    $("#pl-stage").replaceChildren(img);
    $("#pl-status").textContent = f.progress < 1 ? "The image appears once its pieces have downloaded." : "";
  } else {
    playerMessage("Loading…");
    fetch(src, { headers: { Range: "bytes=0-262143" }, credentials: "same-origin" })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(r.statusText))))
      .then((txt) => $("#pl-stage").replaceChildren(h("pre", { text: txt + (f.size > 262144 ? "\n\n… (showing the first 256 KiB)" : "") })))
      .catch(() => playerMessage("The text isn't available yet."));
    $("#pl-status").textContent = "";
  }
  $("#dlg-player").returnValue = "";
  $("#dlg-player").showModal();
}
async function fileAction(hash, index, action) {
  try { await api(`/api/torrents/${hash}/files/${index}/${action}`, { method: "POST" }); }
  catch (e) { fail(e); }
}
async function openFolder(hash) {
  try { await api(`/api/torrents/${hash}/open-folder`, { method: "POST" }); } catch (e) { fail(e); }
}
async function playTorrent(hash) {
  try {
    const d = await api(`/api/torrents/${hash}`);
    const f = bestMediaFile(d);
    if (!f) { toast("This torrent has no video or audio files to play", "err"); return; }
    openPreview(d, f);
  } catch (e) { fail(e); }
}
$("#dlg-player").addEventListener("close", () => { stopPlayer(); playerCtx = null; });
$("#pl-open").addEventListener("click", () => playerCtx && fileAction(playerCtx.hash, playerCtx.file.index, "open"));
$("#pl-reveal").addEventListener("click", () => playerCtx && fileAction(playerCtx.hash, playerCtx.file.index, "reveal"));

/* ---------- narrow screens: overflow menu ---------- */
$("#btn-more").addEventListener("click", (e) => {
  e.stopPropagation();
  const menu = $("#ctx");
  const item = (label, ic, fn) => h("button", { onclick: () => { hideMenu(); fn(); } }, icon(ic), h("span", { text: label }));
  menu.replaceChildren(
    item("Create torrent", "plus-file", openCreate),
    item("RSS feeds", "rss", () => openRSS()),
    item("Statistics", "chart", openStatistics),
    item("Import from another app", "down", openImport),
    item("Light / dark theme", "moon", () => $("#btn-theme").click()),
  );
  menu.hidden = false;
  const b = e.currentTarget.getBoundingClientRect(), r = menu.getBoundingClientRect();
  menu.style.left = Math.max(8, Math.min(b.right - r.width, innerWidth - r.width - 8)) + "px";
  menu.style.top = b.bottom + 6 + "px";
});

/* ---------- phone / remote access ---------- */
async function renderRemote() {
  const box = $("#remote-box");
  let rs;
  try { rs = await api("/api/remote"); } catch (e) { box.replaceChildren(h("p", { class: "error", text: e.message })); return; }
  const kids = [];
  if (!rs.enabled) {
    kids.push(h("ol", { class: "remote-steps" },
      h("li", { class: rs.passwordSet ? "done" : "", text: "Set a password below, so only you can get in." }),
      h("li", { text: "Turn on phone access. Magnetar restarts once to open it." }),
      h("li", { text: "Scan the QR code with your phone's camera." })));
    kids.push(h("button", { type: "button", class: "btn primary", disabled: !rs.passwordSet, onclick: () => setRemote(true) },
      h("span", { text: "Turn on phone access" })));
    if (!rs.passwordSet) kids.push(h("p", { class: "muted small", text: "Set a password first (see Password below)." }));
  } else {
    kids.push(h("p", { class: "muted small", text: "Only use this on networks you trust, such as your home Wi-Fi. Your computer's firewall may ask to allow Magnetar the first time." }));
    if (rs.restartRequired) {
      kids.push(h("p", { class: "notice", text: "Restart Magnetar to start accepting connections from your phone." }),
        rs.canRestart ? h("button", { type: "button", class: "btn primary", onclick: () => setRemote(true) }, h("span", { text: "Restart now" })) : null);
    }
    kids.push(h("div", { class: "remote-urls" }, ...(rs.urls.length ? rs.urls.map((u) =>
      h("div", { class: "remote-url" }, u.qr ? h("img", { src: u.qr, alt: "QR code for " + u.url }) : null, h("code", { text: u.url })))
      : [h("p", { class: "muted", text: "This computer has no home-network address right now. Connect it to your Wi-Fi or network." })])));
    kids.push(h("div", { class: "row" }, h("button", { type: "button", class: "btn ghost", onclick: () => setRemote(false) }, h("span", { text: "Turn off phone access" }))));
  }
  box.replaceChildren(...kids.filter(Boolean));
}
async function setRemote(enabled) {
  try {
    const rs = await api("/api/remote", { method: "POST", body: { enabled, restart: true } });
    if (rs.restartRequired && rs.canRestart) {
      toast("Magnetar is restarting…");
      setTimeout(() => location.reload(), 4000);
      return;
    }
    renderRemote();
  } catch (e) { fail(e); }
}

/* ---------- import from other clients ---------- */
const imp = { client: "", dir: "", list: [], names: {} };
async function openImport() {
  $("#im-err").textContent = "";
  showImportStep(1);
  $("#im-sources").replaceChildren(h("p", { class: "muted", text: "Looking for other apps…" }));
  $("#dlg-import").showModal();
  try {
    const r = await api("/api/import/sources");
    imp.names = r.clients;
    $("#im-client").replaceChildren(...Object.entries(r.clients).map(([k, v]) => h("option", { value: k, text: v })));
    $("#im-sources").replaceChildren(...(r.sources.length ? r.sources.map((s) => h("button", { type: "button", class: "im-source", onclick: () => scanImport(s.client, s.dir) },
      h("b", { text: s.name }), h("span", { text: `${s.count} torrent${s.count === 1 ? "" : "s"}` }), h("span", { text: s.dir })))
      : [h("p", { class: "muted", text: "No other BitTorrent app was found in its usual place. Choose its data folder below." })]));
    $("#im-manual").open = !r.sources.length;
  } catch (e) { $("#im-err").textContent = e.message; }
}
function showImportStep(n) {
  $("#im-step1").hidden = n !== 1;
  $("#im-step2").hidden = n !== 2;
  $("#im-back").hidden = n !== 2;
  $("#im-go").hidden = n !== 2;
}
async function scanImport(client, dir) {
  $("#im-err").textContent = "";
  try {
    imp.list = await api("/api/import/scan", { method: "POST", body: { client, dir, hashes: [] } });
    imp.client = client;
    imp.dir = dir;
  } catch (e) { $("#im-err").textContent = e.message; return; }
  const name = imp.names[client] || client;
  $("#im-warn").textContent = `Quit ${name} before importing, so the two apps don't write to the same files at once. Your torrents stay in ${name}; remove them there once Magnetar has taken over.`;
  const fresh = imp.list.filter((t) => !t.exists);
  $("#im-list").replaceChildren(...(imp.list.length ? imp.list.map((t) => {
    const cb = h("input", { type: "checkbox", checked: !t.exists, disabled: t.exists, dataset: { hash: t.hash } });
    cb.addEventListener("change", importCount);
    return h("label", { class: "im-row" + (t.exists ? " done" : "") }, cb,
      h("span", { text: t.name }),
      h("span", { class: "m", text: t.exists ? "Already in Magnetar" : [t.savePath, t.category, t.paused ? "paused" : ""].filter(Boolean).join(" · ") }),
      h("span", { class: "sz", text: bytes(t.size) }));
  }) : [h("p", { class: "rss-empty", text: "No torrents found in this folder." })]));
  $("#im-all").checked = fresh.length > 0;
  importCount();
  showImportStep(2);
}
function importCount() {
  const boxes = $$("#im-list input:not(:disabled)");
  const n = boxes.filter((b) => b.checked).length;
  $("#im-count").textContent = `${n} of ${boxes.length} torrents selected`;
  $("#im-go").disabled = n === 0;
  $("#im-go").textContent = n ? `Import ${n} torrent${n === 1 ? "" : "s"}` : "Import";
}
$("#im-all").addEventListener("change", () => { $$("#im-list input:not(:disabled)").forEach((b) => { b.checked = $("#im-all").checked; }); importCount(); });
$("#im-back").addEventListener("click", () => showImportStep(1));
$("#im-scan").addEventListener("click", () => scanImport($("#im-client").value, $("#im-dir").value.trim()));
$("#im-browse").addEventListener("click", () => pickFolder($("#im-dir").value.trim(), (p) => { $("#im-dir").value = p; }));
$("#import-form").addEventListener("submit", async (ev) => {
  if (ev.submitter && ev.submitter.value === "cancel") return;
  ev.preventDefault();
  const hashes = $$("#im-list input:checked:not(:disabled)").map((b) => b.dataset.hash);
  if (!hashes.length) return;
  $("#im-go").disabled = true;
  try {
    const r = await api("/api/import", { method: "POST", body: { client: imp.client, dir: imp.dir, hashes } });
    $("#dlg-import").close();
    toast(`Imported ${r.added} torrent${r.added === 1 ? "" : "s"}` + (r.existing ? ` (${r.existing} already here)` : "") + ". Existing files are being checked.");
    r.failed.slice(0, 5).forEach((f) => toast(f, "err"));
    loadCategories();
  } catch (e) { $("#im-err").textContent = e.message; }
  finally { $("#im-go").disabled = false; }
});
$("#btn-import").addEventListener("click", openImport);
$("#btn-import-empty").addEventListener("click", openImport);

/* ---------- create torrent ---------- */
const creating = { id: null, poll: null };
function openCreate() {
  $("#create-setup").hidden = false;
  $("#create-progress").hidden = true;
  $("#cr-start").hidden = false;
  $("#cr-cancel").textContent = "Cancel";
  $("#cr-err").textContent = "";
  creating.id = null;
  $("#dlg-create").showModal();
  $("#cr-path").focus();
}
$("#btn-create").addEventListener("click", openCreate);
$("#cr-browse").addEventListener("click", () => pickFolder($("#cr-path").value.trim(), (p) => { $("#cr-path").value = p; }, true));
const lines = (id) => $(id).value.split("\n").map((s) => s.trim()).filter(Boolean);
$("#create-form").addEventListener("submit", async (ev) => {
  if (ev.submitter && ev.submitter.value === "cancel") return;
  ev.preventDefault();
  const path = $("#cr-path").value.trim();
  if (!path) { $("#cr-err").textContent = "Choose a file or folder to share."; return; }
  $("#cr-err").textContent = "";
  $("#cr-start").disabled = true;
  try {
    const job = await api("/api/create", { method: "POST", body: {
      path, trackers: lines("#cr-trackers"), webSeeds: lines("#cr-webseeds"), pieceLength: Number($("#cr-piece").value),
      private: $("#cr-private").checked, comment: $("#cr-comment").value.trim(), source: $("#cr-source").value.trim(),
      startSeeding: $("#cr-seed").checked } });
    creating.id = job.id;
    $("#create-setup").hidden = true;
    $("#create-progress").hidden = false;
    $("#cr-result").hidden = true;
    $("#cr-start").hidden = true;
    showCreate(job);
    clearInterval(creating.poll);
    creating.poll = setInterval(async () => {
      try { showCreate(await api(`/api/create/${creating.id}`)); } catch (e) { clearInterval(creating.poll); $("#cr-err").textContent = e.message; }
    }, 400);
  } catch (e) { $("#cr-err").textContent = e.message; }
  finally { $("#cr-start").disabled = false; }
});
function showCreate(j) {
  $("#cr-bar").style.width = (j.progress * 100).toFixed(1) + "%";
  if (j.state === "hashing") {
    $("#cr-status").textContent = `Reading ${j.files} file${j.files === 1 ? "" : "s"} (${bytes(j.size)}) - ${pct(j.progress)}`;
    return;
  }
  clearInterval(creating.poll);
  if (j.state === "error") { $("#cr-status").textContent = "Could not create the torrent."; $("#cr-err").textContent = j.error; return; }
  $("#cr-status").textContent = `“${j.name}” is ready - ${bytes(j.size)} in ${j.files} file${j.files === 1 ? "" : "s"}.` + (j.seeding ? " Magnetar is seeding it." : "");
  if (j.error) $("#cr-err").textContent = j.error;
  $("#cr-magnet").value = j.magnet;
  $("#cr-download").href = `/api/create/${j.id}/torrent`;
  $("#cr-result").hidden = false;
  $("#cr-cancel").textContent = "Close";
}
$("#cr-copy").addEventListener("click", async () => {
  try { await navigator.clipboard.writeText($("#cr-magnet").value); toast("Magnet link copied"); } catch { $("#cr-magnet").select(); }
});
$("#dlg-create").addEventListener("close", () => {
  clearInterval(creating.poll);
  if (creating.id && $("#cr-result").hidden) api(`/api/create/${creating.id}`, { method: "DELETE" }).catch(() => {});
});

/* ---------- RSS ---------- */
const rss = { data: { feeds: [], rules: [] }, feed: null, rule: null, rules: [], testTimer: null };
const rssErr = (e) => { $("#rss-err").textContent = e ? e.message || String(e) : ""; };
async function openRSS(tab = "feeds") {
  if (!state.settings) { try { state.settings = await api("/api/settings"); } catch (e) { return fail(e); } }
  try { rss.data = await api("/api/rss"); } catch (e) { return fail(e); }
  rss.rules = structuredClone(rss.data.rules);
  if (!rss.data.feeds.some((f) => f.id === rss.feed)) rss.feed = rss.data.feeds[0]?.id || null;
  if (!rss.rules.some((r) => r.id === rss.rule)) rss.rule = rss.rules[0]?.id || null;
  rssErr();
  rssTab(tab);
  renderFeeds();
  renderRules();
  if (!$("#dlg-rss").open) $("#dlg-rss").showModal();
}
function rssTab(name) {
  $$("#dlg-rss [data-rss]").forEach((b) => b.setAttribute("aria-selected", String(b.dataset.rss === name)));
  $("#rss-feeds").hidden = name !== "feeds";
  $("#rss-rules").hidden = name !== "rules";
}
$$("#dlg-rss [data-rss]").forEach((b) => b.addEventListener("click", () => rssTab(b.dataset.rss)));
$("#btn-rss").addEventListener("click", () => openRSS());

function ago(t) {
  if (!t || t.startsWith("0001")) return "never";
  const s = Math.max(0, (Date.now() - new Date(t)) / 1000);
  return s < 60 ? "just now" : s < 3600 ? `${Math.round(s / 60)} min ago` : s < 86400 ? `${Math.round(s / 3600)} h ago` : new Date(t).toLocaleDateString();
}
function renderFeeds() {
  const list = $("#rss-feed-list");
  if (!rss.data.feeds.length) {
    list.replaceChildren(h("li", { class: "rss-empty", text: "No feeds yet. Paste a feed address above." }));
    $("#rss-items").replaceChildren();
    return;
  }
  list.replaceChildren(...rss.data.feeds.map((f) => h("li", { class: (f.id === rss.feed ? "active" : "") + (f.enabled ? "" : " off"), onclick: () => { rss.feed = f.id; renderFeeds(); } },
    icon("rss"),
    h("span", { class: "grow" }, h("span", { class: "t", text: f.title || f.url, title: f.url }),
      h("span", { class: "s" + (f.lastError ? " err" : ""), text: f.lastError ? f.lastError : `${f.items.length} articles · checked ${ago(f.lastChecked)}` })),
    h("button", { type: "button", class: "btn icon sm", title: "Remove feed", onclick: (e) => { e.stopPropagation(); removeFeed(f); } }, icon("trash")))));
  renderItems();
}
function renderItems() {
  const f = rss.data.feeds.find((x) => x.id === rss.feed);
  const box = $("#rss-items");
  if (!f) { box.replaceChildren(); return; }
  if (!f.items.length) { box.replaceChildren(h("div", { class: "rss-empty", text: f.lastError ? "This feed could not be loaded." : "No torrent articles in this feed." })); return; }
  box.replaceChildren(...f.items.map((it) => h("div", { class: "rss-item" },
    h("span", { text: it.title }),
    h("span", { class: "m", text: [it.size ? bytes(it.size) : "", it.published && !it.published.startsWith("0001") ? new Date(it.published).toLocaleString() : ""].filter(Boolean).join(" · ") || " " }),
    h("button", { type: "button", class: "btn ghost sm", onclick: async (e) => {
      e.currentTarget.disabled = true;
      try { await api("/api/rss/download", { method: "POST", body: { feed: f.id, key: it.key } }); toast("Torrent added"); }
      catch (err) { fail(err); }
      finally { e.currentTarget.disabled = false; }
    } }, icon("down"), h("span", { text: "Download" })))));
}
$("#rss-add").addEventListener("click", async () => {
  const url = $("#rss-url").value.trim();
  if (!url) return;
  $("#rss-add").disabled = true;
  rssErr();
  try {
    const f = await api("/api/rss/feeds", { method: "POST", body: { url } });
    $("#rss-url").value = "";
    rss.feed = f.id;
    await openRSS("feeds");
    if (f.lastError) rssErr(new Error("Added, but the feed could not be loaded: " + f.lastError));
  } catch (e) { rssErr(e); }
  finally { $("#rss-add").disabled = false; }
});
$("#rss-url").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); $("#rss-add").click(); } });
$("#rss-refresh").addEventListener("click", async () => {
  $("#rss-refresh").disabled = true;
  try { rss.data = await api("/api/rss/refresh", { method: "POST", body: {} }); renderFeeds(); } catch (e) { rssErr(e); }
  finally { $("#rss-refresh").disabled = false; }
});
async function removeFeed(f) {
  if (!confirm(`Remove the feed “${f.title || f.url}”?`)) return;
  try { await api(`/api/rss/feeds/${f.id}`, { method: "DELETE" }); await openRSS("feeds"); } catch (e) { rssErr(e); }
}

function renderRules() {
  const list = $("#rss-rule-list");
  list.replaceChildren(...rss.rules.map((r) => h("li", { class: (r.id === rss.rule ? "active" : "") + (r.enabled ? "" : " off"), onclick: () => { rss.rule = r.id; renderRules(); } },
    h("span", { class: "grow" }, h("span", { class: "t", text: r.name || "New rule" }),
      h("span", { class: "s", text: r.enabled ? (r.mustContain || "no filter yet") : "Off" })))));
  renderRuleEditor();
}
$("#rss-rule-new").addEventListener("click", () => {
  const r = { id: "new-" + Date.now(), name: "New rule", enabled: true, mustContain: "", mustNotContain: "", useRegex: false, smartEpisodes: true, feeds: [], category: "", savePath: "", paused: false };
  rss.rules.push(r);
  rss.rule = r.id;
  renderRules();
  $("#rr-name")?.select();
});
function renderRuleEditor() {
  const box = $("#rss-editor");
  const r = rss.rules.find((x) => x.id === rss.rule);
  if (!r) {
    box.replaceChildren(h("p", { class: "muted", text: "Rules download new articles automatically. Create one to get started, for example “my show 1080p” to get every new episode of a show." }));
    return;
  }
  const field = (label, input, hint) => h("label", { class: "field" }, h("span", {}, label, hint ? h("em", { text: ` (${hint})` }) : null), input);
  const text = (key, attrs = {}) => {
    const el = h("input", { type: "text", value: r[key] || "", spellcheck: "false", ...attrs });
    el.addEventListener("input", () => { r[key] = el.value; scheduleRuleTest(r); if (key === "name" || key === "mustContain") renderRuleListOnly(); });
    return el;
  };
  const check = (key, label) => {
    const el = h("input", { type: "checkbox", checked: !!r[key] });
    el.addEventListener("change", () => { r[key] = el.checked; scheduleRuleTest(r); renderRuleListOnly(); });
    return h("label", { class: "switch" }, el, h("span"), label);
  };
  const cat = h("select", {}, h("option", { value: "", text: "No category" }),
    ...(state.settings.categories || []).map((c) => h("option", { value: c.name, text: c.name })));
  cat.value = r.category || "";
  cat.addEventListener("change", () => { r.category = cat.value; });
  const feeds = h("div", { class: "feeds-pick" }, ...rss.data.feeds.map((f) => {
    const cb = h("input", { type: "checkbox", checked: (r.feeds || []).includes(f.id) });
    cb.addEventListener("change", () => {
      r.feeds = (r.feeds || []).filter((x) => x !== f.id);
      if (cb.checked) r.feeds.push(f.id);
      scheduleRuleTest(r);
    });
    return h("label", {}, cb, " ", f.title || f.url);
  }));
  box.replaceChildren(
    field("Name", text("name", { id: "rr-name" })),
    check("enabled", "Use this rule"),
    field("Must contain", text("mustContain", { placeholder: r.useRegex ? "my[ .]show.*1080p" : "my show 1080p" }), r.useRegex ? "regular expression" : "all words; | separates alternatives"),
    field("Must not contain", text("mustNotContain", { placeholder: r.useRegex ? "720p|cam" : "720p cam" }), r.useRegex ? "regular expression" : "any of these words"),
    check("useRegex", "Use regular expressions"),
    check("smartEpisodes", "Smart episode filter - download each episode only once (S01E02, 1x02)"),
    rss.data.feeds.length ? h("div", { class: "field" }, h("span", {}, "Feeds ", h("em", { text: "(none ticked = all feeds)" })), feeds) : null,
    h("div", { class: "two" }, field("Category", cat), field("Save to", text("savePath", { placeholder: state.settings.downloadDir }), "optional")),
    check("paused", "Add paused"),
    h("div", { class: "field" }, h("span", { text: "Matching articles" }), h("ul", { class: "rss-matches", id: "rr-matches" })),
    h("div", { class: "row" },
      h("button", { type: "button", class: "btn primary", onclick: saveRules }, h("span", { text: "Save rules" })),
      h("button", { type: "button", class: "btn ghost danger", onclick: () => {
        rss.rules = rss.rules.filter((x) => x.id !== r.id);
        rss.rule = rss.rules[0]?.id || null;
        saveRules();
      } }, icon("trash"), h("span", { text: "Delete rule" }))),
  );
  testRule(r);
}
function renderRuleListOnly() {
  const ed = document.activeElement;
  const list = $("#rss-rule-list");
  list.replaceChildren(...rss.rules.map((r) => h("li", { class: (r.id === rss.rule ? "active" : "") + (r.enabled ? "" : " off"), onclick: () => { rss.rule = r.id; renderRules(); } },
    h("span", { class: "grow" }, h("span", { class: "t", text: r.name || "New rule" }),
      h("span", { class: "s", text: r.enabled ? (r.mustContain || "no filter yet") : "Off" })))));
  ed?.focus?.();
}
function ruleBody(r) {
  const { id, name, enabled, mustContain, mustNotContain, useRegex, smartEpisodes, feeds, category, savePath, paused, episodes } = r;
  return { id: id.startsWith("new-") ? "" : id, name, enabled, mustContain, mustNotContain, useRegex, smartEpisodes, feeds: feeds || [], category, savePath, paused, episodes: episodes || [] };
}
function scheduleRuleTest(r) { clearTimeout(rss.testTimer); rss.testTimer = setTimeout(() => testRule(r), 300); }
async function testRule(r) {
  const ul = $("#rr-matches");
  if (!ul) return;
  if (!r.mustContain.trim()) { ul.replaceChildren(h("li", { class: "muted", text: "Enter what titles must contain." })); return; }
  try {
    const titles = await api("/api/rss/test", { method: "POST", body: { ...ruleBody(r), name: r.name || "rule" } });
    ul.replaceChildren(...(titles.length ? titles.map((t) => h("li", { text: t })) : [h("li", { class: "muted", text: "No current articles match. New ones will be checked as they arrive." })]));
  } catch (e) { ul.replaceChildren(h("li", { class: "error", text: e.message })); }
}
async function saveRules() {
  rssErr();
  try {
    const sel = rss.rules.findIndex((x) => x.id === rss.rule);
    rss.data = await api("/api/rss/rules", { method: "PUT", body: rss.rules.map(ruleBody) });
    rss.rules = structuredClone(rss.data.rules);
    rss.rule = rss.rules[Math.max(0, Math.min(sel, rss.rules.length - 1))]?.id || null;
    renderRules();
    toast("Rules saved");
  } catch (e) { rssErr(e); }
}

/* ---------- statistics ---------- */
const SVG_NS = "http://www.w3.org/2000/svg";
function svgEl(tag, attrs = {}) {
  const el = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  return el;
}
const hhmm = (sec) => new Date(sec * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
function niceMax(v) {
  if (v <= 0) return 1024;
  const p = Math.pow(1024, Math.floor(Math.log(v) / Math.log(1024)));
  const n = v / p;
  const step = [1, 2, 2.5, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000].find((s) => s >= n) || 1024;
  return step * p;
}
async function openStatistics() {
  let s;
  try { s = await api("/api/statistics"); } catch (e) { return fail(e); }
  const uptime = Math.max(0, Math.floor((Date.now() - new Date(s.sessionStart)) / 1000));
  const tile = (value, label, sub) => h("div", { class: "tile" }, h("b", { text: value }), h("span", { text: label }), sub ? h("small", { text: sub }) : null);
  $("#st-tiles").replaceChildren(
    tile(bytes(s.allTimeDown), "Downloaded (all time)", `since ${new Date(s.firstStarted).toLocaleDateString()}`),
    tile(bytes(s.allTimeUp), "Uploaded (all time)", `ratio ${s.allTimeRatio.toFixed(2)}`),
    tile(`${bytes(s.sessionDown)} / ${bytes(s.sessionUp)}`, "This session ↓ / ↑", `running for ${eta(uptime)}`),
    tile(`${s.completed} / ${s.torrents}`, "Completed / torrents", `${s.sessions} session${s.sessions === 1 ? "" : "s"}`),
  );
  state.statsData = s;
  $("#st-table").hidden = true;
  $("#st-chart").hidden = false;
  $("#st-table-btn").textContent = "Show table";
  $("#dlg-stats").showModal();
  drawBandwidthChart(s.history || []);
}
function drawBandwidthChart(history) {
  const wrap = $("#st-chart");
  const now = Math.floor(Date.now() / 1000);
  const t0 = now - 24 * 3600;
  const pts = history.filter((p) => p.t >= t0);
  if (!pts.some((p) => p.down > 0 || p.up > 0)) {
    wrap.replaceChildren(h("div", { class: "chart-empty", text: "No transfers recorded in the last 24 hours yet." }));
    return;
  }
  const W = Math.max(wrap.clientWidth, 480), H = 240, L = 64, R = 76, T = 10, B = 26;
  const x = (t) => L + ((t - t0) / (now - t0)) * (W - L - R);
  const ymax = niceMax(Math.max(...pts.map((p) => Math.max(p.down, p.up))));
  const y = (v) => T + (1 - v / ymax) * (H - T - B);
  const svg = svgEl("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": "Download and upload speed over the last 24 hours" });
  // Recessive grid and y labels.
  for (let i = 0; i <= 4; i++) {
    const v = (ymax / 4) * i, yy = y(v);
    svg.append(svgEl("line", { class: "grid", x1: L, x2: W - R, y1: yy, y2: yy }));
    const lab = svgEl("text", { class: "axis-label", x: L - 8, y: yy + 4, "text-anchor": "end" });
    lab.textContent = i === 0 ? "0" : bytes(v) + "/s";
    svg.append(lab);
  }
  // Hour ticks every 4 hours.
  for (let k = 0; k <= 6; k++) {
    const t = t0 + k * 4 * 3600;
    const lab = svgEl("text", { class: "axis-label", x: x(t), y: H - 6, "text-anchor": k === 0 ? "start" : k === 6 ? "end" : "middle" });
    lab.textContent = k === 6 ? "now" : hhmm(t);
    svg.append(lab);
  }
  const path = (key) => pts.map((p, i) => `${i ? "L" : "M"}${x(p.t).toFixed(1)},${y(p[key]).toFixed(1)}`).join("");
  const base = y(0).toFixed(1);
  svg.append(svgEl("path", { class: "area dl", d: `${path("down")}L${x(pts[pts.length - 1].t).toFixed(1)},${base}L${x(pts[0].t).toFixed(1)},${base}Z` }));
  svg.append(svgEl("path", { class: "line ul", d: path("up") }));
  svg.append(svgEl("path", { class: "line dl", d: path("down") }));
  // Direct labels at the line ends (secondary encoding besides colour).
  const last = pts[pts.length - 1];
  let ld = y(last.down), lu = y(last.up);
  if (Math.abs(ld - lu) < 14) { if (ld <= lu) { ld -= 7; lu += 7; } else { ld += 7; lu -= 7; } }
  for (const [txt, yy] of [["Download", ld], ["Upload", lu]]) {
    const t = svgEl("text", { class: "end-label", x: x(last.t) + 8, y: yy + 4 });
    t.textContent = txt;
    svg.append(t);
  }
  // Hover: crosshair, dots and tooltip.
  const cross = svgEl("line", { class: "cross", y1: T, y2: H - B, visibility: "hidden" });
  const dotD = svgEl("circle", { class: "dot dl", r: 5, visibility: "hidden" });
  const dotU = svgEl("circle", { class: "dot ul", r: 5, visibility: "hidden" });
  const hit = svgEl("rect", { x: L, y: T, width: W - L - R, height: H - T - B, fill: "transparent" });
  svg.append(cross, dotU, dotD, hit);
  const tip = h("div", { class: "chart-tip", hidden: true });
  const show = (ev) => {
    const r = svg.getBoundingClientRect();
    const px = ((ev.clientX - r.left) / r.width) * W;
    const t = t0 + ((px - L) / (W - L - R)) * (now - t0);
    let best = pts[0];
    for (const p of pts) if (Math.abs(p.t - t) < Math.abs(best.t - t)) best = p;
    const cx = x(best.t);
    cross.setAttribute("x1", cx); cross.setAttribute("x2", cx); cross.setAttribute("visibility", "visible");
    dotD.setAttribute("cx", cx); dotD.setAttribute("cy", y(best.down)); dotD.setAttribute("visibility", "visible");
    dotU.setAttribute("cx", cx); dotU.setAttribute("cy", y(best.up)); dotU.setAttribute("visibility", "visible");
    tip.replaceChildren(
      h("div", { class: "t", text: hhmm(best.t) }),
      h("div", {}, h("i", { class: "sw dl" }), `Download ${rate(best.down)}`),
      h("div", {}, h("i", { class: "sw ul" }), `Upload ${rate(best.up)}`));
    tip.hidden = false;
    const left = (cx / W) * r.width;
    tip.style.left = Math.min(left + 12, r.width - 170) + "px";
    tip.style.top = "8px";
  };
  hit.addEventListener("mousemove", show);
  hit.addEventListener("mouseleave", () => {
    tip.hidden = true;
    [cross, dotD, dotU].forEach((e) => e.setAttribute("visibility", "hidden"));
  });
  wrap.replaceChildren(svg, tip);
}
function renderStatsTable(history) {
  const hours = new Map();
  for (const p of history) {
    const hr = Math.floor(p.t / 3600) * 3600;
    const v = hours.get(hr) || { down: 0, up: 0 };
    v.down += p.down * 60; v.up += p.up * 60;
    hours.set(hr, v);
  }
  const rows = [...hours.entries()].sort((a, b) => b[0] - a[0]);
  $("#st-table").replaceChildren(rows.length ? h("table", { class: "table" },
    h("thead", {}, h("tr", {}, ["Hour", "Downloaded", "Uploaded", "Avg ↓", "Avg ↑"].map((c) => h("th", { text: c })))),
    h("tbody", {}, rows.map(([hr, v]) => h("tr", {},
      h("td", { text: `${hhmm(hr)}–${hhmm(hr + 3600)}` }), h("td", { text: bytes(v.down) }), h("td", { text: bytes(v.up) }),
      h("td", { text: rate(Math.round(v.down / 3600)) }), h("td", { text: rate(Math.round(v.up / 3600)) })))))
    : h("p", { class: "muted", text: "No data yet." }));
}
$("#btn-stats").addEventListener("click", openStatistics);
$("#st-table-btn").addEventListener("click", () => {
  const showTable = $("#st-table").hidden;
  $("#st-table").hidden = !showTable;
  $("#st-chart").hidden = showTable;
  $("#st-table-btn").textContent = showTable ? "Show chart" : "Show table";
  if (showTable) renderStatsTable(state.statsData.history || []);
  else drawBandwidthChart(state.statsData.history || []);
});

/* ---------- connection health ---------- */
const CONN = {
  connectable: ["Connectable", "Other peers can connect to you - the best speeds are possible."],
  idle: ["Ready", "No torrent is exchanging data right now, so there is nothing to measure yet. As soon as a download or upload is active, Magnetar checks whether other peers can connect to you - usually within a few minutes."],
  checking: ["Checking connection…", "A torrent is active and Magnetar is waiting to see whether other peers connect to you. If none do within about three minutes, it will tell you how to fix it."],
  firewalled: ["Port not reachable", "Peers are not connecting to you, so you can only reach peers that accept incoming connections themselves. Downloads still work but are usually slower."],
  "vpn-down": ["VPN disconnected", "The network connection Magnetar is bound to (your VPN) is down, so all torrent traffic is stopped to keep your real address hidden. Transfers resume automatically when it reconnects."],
  offline: ["No network", "Magnetar cannot reach the BitTorrent network. Check your internet connection, VPN or the “Bind traffic to IP” setting."],
};
function renderConnection() {
  const s = state.stats, c = s.connection || "checking";
  const pill = $("#g-conn");
  pill.className = "conn-pill " + c;
  pill.querySelector("span").textContent = CONN[c][0] + (c === "connectable" || c === "idle" ? ` · port ${s.listenPort}` : "");
}
$("#g-conn").addEventListener("click", () => {
  const s = state.stats, c = s.connection || "checking", port = s.listenPort || "?";
  $("#conn-title").textContent = CONN[c][0];
  $("#conn-text").textContent = CONN[c][1];
  const step = (...parts) => h("li", {}, ...parts.map((p) => (typeof p === "string" ? p : h("code", { text: p.code }))));
  const steps = c === "connectable" || c === "idle" ? [] : c === "vpn-down" ? [
    step("Reconnect your VPN. Magnetar notices within a few seconds and resumes."),
    step("To stop using the kill switch, choose “Any connection” under Privacy & security."),
  ] : c === "offline" ? [
    step("Check that this computer is online and the VPN (if any) is connected."),
    step("If “Bind traffic to IP address” is set in Privacy & security, make sure that address still exists."),
    step("Turn on DHT in Connection settings so peers can be found without trackers."),
  ] : [
    step(s.upnp ? "UPnP is on. Make sure UPnP/NAT-PMP is enabled in your router." : "Turn on “Forward port with UPnP / NAT-PMP” in Connection settings."),
    step("Or forward port ", { code: `${port}` }, " (TCP and UDP) in your router to this computer."),
    step("Allow Magnetar through your firewall (on Windows: allow it when Windows Defender Firewall asks, or in “Allow an app through firewall”)."),
    step("Using a VPN? Many VPNs need port forwarding enabled in their app; then set that port as the listening port."),
  ];
  $("#conn-steps").replaceChildren(...steps);
  $("#conn-steps").hidden = !steps.length;
  $("#dlg-conn").showModal();
});
$("#conn-settings").addEventListener("click", async () => {
  $("#dlg-conn").close();
  await openSettings();
  $('.settings-nav [data-sec="connection"]').click();
});

/* ---------- categories & moving ---------- */
state.cats = [];
state.category = null; // null = all, "" = uncategorized
async function loadCategories() {
  try {
    const s = await api("/api/settings");
    state.cats = s.categories || [];
    fillCategorySelects();
    render();
  } catch { /* not signed in yet */ }
}
function fillCategorySelects() {
  const opts = () => [h("option", { value: "", text: "No category" }), ...state.cats.map((c) => h("option", { value: c.name, text: c.name }))];
  const keep = $("#add-cat").value;
  $("#add-cat").replaceChildren(...opts());
  $("#add-cat").value = state.cats.some((c) => c.name === keep) ? keep : "";
  $("#cat-select").replaceChildren(...opts());
}
$("#add-cat").addEventListener("change", () => {
  const c = state.cats.find((x) => x.name === $("#add-cat").value);
  $("#add-path").value = (c && c.savePath) || (state.settings && state.settings.downloadDir) || $("#add-path").value;
  updateFree(true);
});
let catTargets = [];
function openCategoryDialog(hashes) {
  if (!state.cats.length) { toast("Create a category first in Settings → Downloads", "err"); openSettings(); return; }
  catTargets = hashes;
  const current = hashes.length === 1 ? (byHash(hashes[0])?.category || "") : "";
  fillCategorySelects();
  $("#cat-select").value = current;
  $("#cat-title").textContent = hashes.length > 1 ? `Set category for ${hashes.length} torrents` : "Set category";
  $("#cat-move").checked = false;
  $("#dlg-cat").returnValue = "";
  $("#dlg-cat").showModal();
}
$("#dlg-cat").addEventListener("close", async () => {
  if ($("#dlg-cat").returnValue !== "ok") return;
  const name = $("#cat-select").value;
  try {
    await api("/api/actions", { method: "POST", body: { action: "category", hashes: catTargets, category: name } });
    const c = state.cats.find((x) => x.name === name);
    if ($("#cat-move").checked && c && c.savePath) {
      await api("/api/actions", { method: "POST", body: { action: "move", hashes: catTargets, path: c.savePath } });
      toast("Category set - moving files");
    } else toast(name ? `Category set to ${name}` : "Category removed");
  } catch (e) { fail(e); }
});
function moveTo(hashes) {
  const first = byHash(hashes[0]);
  pickFolder(first ? first.savePath : "", async (dest) => {
    try {
      await api("/api/actions", { method: "POST", body: { action: "move", hashes, path: dest } });
      toast(hashes.length > 1 ? `Moving ${hashes.length} torrents…` : "Moving files…");
    } catch (e) { fail(e); }
  });
}
$("#btn-manage-cats").addEventListener("click", async () => {
  await openSettings();
  $('.settings-nav [data-sec="downloads"]').click();
  if (!$("#cat-editor").children.length) addCatRow({ name: "", savePath: "" }, true);
});
function addCatRow(c, focus) {
  const name = h("input", { type: "text", placeholder: "Name", value: c.name || "", maxlength: "40", "aria-label": "Category name" });
  const path = h("input", { type: "text", placeholder: "Folder (optional)", value: c.savePath || "", spellcheck: "false", "aria-label": "Category folder" });
  const browseBtn = h("button", { type: "button", class: "btn ghost sm", title: "Browse" }, icon("folder"));
  browseBtn.addEventListener("click", () => pickFolder(path.value.trim(), (p) => { path.value = p; }));
  const row = h("div", { class: "cat-row" }, name, path, browseBtn,
    h("button", { type: "button", class: "btn ghost sm danger", title: "Remove", onclick: () => row.remove() }, icon("trash")));
  $("#cat-editor").append(row);
  if (focus) name.focus();
}
function renderCatEditor(cats) {
  $("#cat-editor").replaceChildren();
  cats.forEach((c) => addCatRow(c));
}
function readCatEditor() {
  return $$("#cat-editor .cat-row").map((r) => {
    const [name, path] = r.querySelectorAll("input");
    return { name: name.value.trim(), savePath: path.value.trim() };
  }).filter((c) => c.name);
}
$("#cat-add").addEventListener("click", () => addCatRow({ name: "", savePath: "" }, true));

/* ---------- speed limits ---------- */
let speedMode = "normal";
async function openSpeed() {
  try { state.settings = await api("/api/settings"); } catch (e) { return fail(e); }
  setSpeedMode(state.settings.altSpeedEnabled ? "alt" : "normal");
  $("#dlg-speed").returnValue = "";
  $("#dlg-speed").showModal();
}
function setSpeedMode(mode) {
  speedMode = mode;
  const s = state.settings;
  $$("#speed-mode button").forEach((b) => b.classList.toggle("active", b.dataset.mode === mode));
  setRate($("#sp-down"), mode === "alt" ? s.altDownloadLimitKBps : s.downloadLimitKBps);
  setRate($("#sp-up"), mode === "alt" ? s.altUploadLimitKBps : s.uploadLimitKBps);
}
$$("#speed-mode button").forEach((b) => b.addEventListener("click", () => setSpeedMode(b.dataset.mode)));
const PRESETS = [["Unlimited", 0, 0], ["10 MiB/s ↓", 10240, null], ["5 MiB/s ↓", 5120, null], ["1 MiB/s ↓", 1024, null], ["500 KiB/s ↑", null, 500], ["100 KiB/s ↑", null, 100]];
$("#sp-presets").replaceChildren(...PRESETS.map(([label, d, u]) => h("button", {
  type: "button", text: label, onclick: () => { if (d !== null) setRate($("#sp-down"), d); if (u !== null) setRate($("#sp-up"), u); },
})));
$("#speed-form").addEventListener("submit", async (ev) => {
  if (ev.submitter && ev.submitter.value === "cancel") return;
  ev.preventDefault();
  const down = Math.max(0, Number($("#sp-down").value || 0)), up = Math.max(0, Number($("#sp-up").value || 0));
  const body = speedMode === "alt"
    ? { altSpeedEnabled: true, altDownloadLimitKBps: down, altUploadLimitKBps: up }
    : { altSpeedEnabled: false, downloadLimitKBps: down, uploadLimitKBps: up };
  try { await api("/api/settings", { method: "PUT", body }); $("#dlg-speed").close(); altLoaded = false; if (!$("#alt-panel").hidden) { $("#alt-panel").hidden = true; showAltPanel(true); } toast("Speed limits updated"); }
  catch (e) { fail(e); }
});
$("#speed-card").addEventListener("click", openSpeed);
$("#speed-card").addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); openSpeed(); } });
// Alternative limits can be edited right under the turtle switch.
let altLoaded = false, altSaveTimer = null;
async function showAltPanel(on) {
  const panel = $("#alt-panel");
  if (panel.hidden === !on) return;
  panel.hidden = !on;
  if (on && !altLoaded) {
    try {
      const s = await api("/api/settings");
      setRate($("#alt-down"), s.altDownloadLimitKBps);
      setRate($("#alt-up"), s.altUploadLimitKBps);
      altLoaded = true;
    } catch { /* shown on next open */ }
  }
}
function saveAltLimits() {
  clearTimeout(altSaveTimer);
  altSaveTimer = setTimeout(async () => {
    try {
      await api("/api/settings", { method: "PUT", body: {
        altDownloadLimitKBps: Number($("#alt-down").value || 0), altUploadLimitKBps: Number($("#alt-up").value || 0) } });
      state.settings = null;
    } catch (e) { fail(e); }
  }, 600);
}
$("#alt-panel").addEventListener("input", saveAltLimits);
$("#alt-panel").addEventListener("change", saveAltLimits);

$("#alt-toggle").addEventListener("click", async () => {
  const on = !state.stats.altSpeed;
  try { await api("/api/settings", { method: "PUT", body: { altSpeedEnabled: on } }); toast(on ? "Alternative speed limits on" : "Normal speed limits"); }
  catch (e) { fail(e); }
});

$("#btn-quit").addEventListener("click", async () => {
  if (!confirm("Quit Magnetar? All transfers will stop.")) return;
  try { await api("/api/shutdown", { method: "POST" }); } catch { /* server exits */ }
  showStopped();
});
// Magnetar quit (from the tray or here): close this window, or say so if the
// browser does not allow it.
function showStopped() {
  closeStream();
  document.body.replaceChildren(h("div", { class: "login" }, h("div", { class: "login-card" },
    h("h1", { text: "Magnetar has stopped" }), h("p", { class: "muted", text: "You can close this window." }))));
  setTimeout(() => window.close(), 100);
}
$("#btn-theme").addEventListener("click", () => applyTheme(document.documentElement.dataset.theme === "light" ? "dark" : "light"));

/* ---------- keyboard ---------- */
document.addEventListener("keydown", (e) => {
  const tag = (e.target.tagName || "").toLowerCase();
  if (tag === "input" || tag === "textarea" || tag === "select" || document.querySelector("dialog[open]")) return;
  const items = visible();
  if (e.key === "/") { e.preventDefault(); $("#search").focus(); }
  else if (e.key === "n" || e.key === "N") { e.preventDefault(); openAdd(); }
  else if (e.key === "Escape") { if (state.selected.size) { state.selected.clear(); render(); } else closeDetail(); }
  else if ((e.ctrlKey || e.metaKey) && e.key === "a") { e.preventDefault(); items.forEach((t) => state.selected.add(t.hash)); render(); }
  else if (e.key === "Delete") { const t = state.selected.size ? [...state.selected] : state.detail ? [state.detail] : []; if (t.length) confirmRemove(t); }
  else if (e.key === " " && state.detail) { e.preventDefault(); const t = byHash(state.detail); act(t.state === "paused" ? "resume" : "pause", [t.hash]); }
  else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
    e.preventDefault();
    const idx = items.findIndex((t) => t.hash === state.detail);
    const next = items[Math.max(0, Math.min(items.length - 1, idx + (e.key === "ArrowDown" ? 1 : -1)))];
    if (next) { openDetail(next.hash); rowCache.get(next.hash)?.scrollIntoView({ block: "nearest" }); }
  }
});

document.querySelectorAll("input.rate").forEach(enhanceRateInput);
boot();
