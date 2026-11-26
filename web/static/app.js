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
      : "Open Magnetar from your computer to sign in — it opens this page with a one-time link. Set a password in Settings to sign in from anywhere.";
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
  if (s.bindAddress) parts.push("bound to " + s.bindAddress);
  if (s.blocklistSize) parts.push(s.blocklistSize.toLocaleString() + " blocked ranges");
  sec.querySelector("span").textContent = parts.join(" · ");
  sec.classList.toggle("warn", s.encryption === "allow" || s.online === false);
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
  return t.category ? `${t.category} · ${s}` : s;
}
function subtitleText(t) {
  switch (t.state) {
    case "error": return t.error;
    case "downloading": case "stalled": return `${t.seeds} seeds · ${t.peers} peers` + (t.sequential ? " · sequential" : "");
    case "checking": return "Checking existing files…";
    case "moving": return "Moving files…";
    case "metadata": return t.knownPeers ? `Fetching metadata from ${t.knownPeers} peers` : "Searching for peers";
    case "seeding": return `${t.peers} peers · ratio ${t.ratio.toFixed(2)}`;
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
    item("Verify data", "refresh", () => act("recheck", targets)),
    item(t.sequential ? "Disable sequential" : "Download sequentially", "down", () => setSequential(hash, !t.sequential)),
    item("Category…", "tag", () => openCategoryDialog(targets)),
    item("Move files to…", "move", () => moveTo(targets)),
    item("Copy magnet link", "copy", () => copyMagnet(hash)),
    item("Details", "file", () => openDetail(hash)),
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
  $("#d-actions").replaceChildren(
    h("button", { class: "btn ghost sm", onclick: () => act(paused ? "resume" : "pause", [d.hash]) }, icon(paused ? "play" : "pause"), h("span", { text: paused ? "Resume" : "Pause" })),
    h("button", { class: "btn ghost sm", onclick: () => act("recheck", [d.hash]) }, icon("refresh"), h("span", { text: "Verify" })),
    h("button", { class: "btn ghost sm", onclick: () => copyMagnet(d.hash) }, icon("copy"), h("span", { text: "Magnet" })),
    h("button", { class: "btn ghost sm danger", onclick: () => confirmRemove([d.hash]) }, icon("trash"), h("span", { text: "Remove" })),
  );

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
        ["Category", d.category || "—"],
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
    const wrap = h("div", { class: "files" });
    const all = h("div", { class: "row" },
      h("button", { class: "btn ghost sm", onclick: () => setPrio(d.hash, d.files.map((f) => f.index), 1) }, h("span", { text: "Download all" })),
      h("button", { class: "btn ghost sm", onclick: () => setPrio(d.hash, d.files.map((f) => f.index), 0) }, h("span", { text: "Skip all" })),
    );
    for (const f of d.files) {
      const sel = h("select", { class: "sm", "aria-label": "Priority" },
        h("option", { value: "0", text: "Skip" }), h("option", { value: "1", text: "Normal" }), h("option", { value: "2", text: "High" }));
      sel.value = String(f.priority);
      sel.addEventListener("change", () => { setPrio(d.hash, [f.index], +sel.value); sel.blur(); });
      const bar = h("div", { class: "bar" }, h("i"));
      bar.firstChild.style.width = (f.progress * 100).toFixed(1) + "%";
      wrap.append(h("div", { class: "file" + (f.priority === 0 ? " skip" : "") },
        h("div", { class: "fp", text: f.path }), sel,
        h("div", { class: "fm", text: `${bytes(f.completed)} / ${bytes(f.size)} · ${pct(f.progress)}` }), bar));
    }
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
      d.trackers.length ? h("ul", { class: "trackers" }, d.trackers.map((t) => h("li", { text: t }))) : h("p", { class: "muted", text: "No trackers — peers are found through DHT and PEX." }),
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
async function setPrio(hash, indices, priority) {
  try { await api(`/api/torrents/${hash}/files`, { method: "POST", body: { indices, priority } }); loadDetail(true); }
  catch (e) { fail(e); }
}

/* ---------- add torrents ---------- */
async function openAdd(prefill = "") {
  if (!state.settings) { try { state.settings = await api("/api/settings"); } catch (e) { return fail(e); } }
  const dlg = $("#dlg-add");
  if (!dlg.open) {
    $("#add-magnets").value = prefill;
    $("#add-path").value = state.settings.downloadDir;
    state.pendingFiles = [];
    renderPending();
    dlg.showModal();
    (prefill ? $("#add-submit") : $("#add-magnets")).focus();
  } else if (prefill) {
    $("#add-magnets").value = ($("#add-magnets").value.trim() + "\n" + prefill).trim();
  }
  startPreview(); // no debounce when opening: show the file list as soon as possible
  updateFree(true);
}
// Magnet links opened from the browser get their own small window; close it when done.
$("#dlg-add").addEventListener("close", () => {
  if (!state.popup) return;
  state.popup = false;
  setTimeout(() => window.close(), 150);
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
    if (src.kind === "file") {
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
$("#dlg-add").addEventListener("close", () => { clearTimeout(pv.timer); dropPreview(); renderPreview(); $("#add-submit").disabled = false; });

$("#add-form").addEventListener("submit", async (ev) => {
  if (ev.submitter && ev.submitter.value === "cancel") return;
  ev.preventDefault();
  const magnets = $("#add-magnets").value.trim();
  if (!magnets && !state.pendingFiles.length) { toast("Add a magnet link or a .torrent file", "err"); return; }
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
    state.popup = true;
    openAdd(decodeURIComponent(hash.slice(11)));
  }
  else if (/^#t=[0-9a-f]{40}(:\w+)?$/i.test(hash)) {
    const [h, tab] = hash.slice(3).toLowerCase().split(":");
    if (tab && TABS[tab]) { state.tab = tab; $$(".tabs button").forEach((x) => x.classList.toggle("active", x.dataset.tab === tab)); }
    openDetail(h);
  }
  else if (hash === "#settings") openSettings();
  else if (hash.startsWith("#settings:")) openSettings().then(() => $(`.settings-nav [data-sec="${hash.slice(10)}"]`)?.click());
  else if (hash === "#speed") openSpeed();
  else if (hash === "#add") openAdd();
}

/* ---------- settings ---------- */
const NUMERIC = new Set(["listenPort", "webPort", "maxConnsPerTorrent", "maxHalfOpenConns", "downloadLimitKBps", "uploadLimitKBps", "altDownloadLimitKBps", "altUploadLimitKBps", "pieceHashers", "seedRatioLimit"]);
async function openSettings() {
  try { state.settings = await api("/api/settings"); } catch (e) { return fail(e); }
  const form = $("#settings-form");
  for (const el of form.elements) {
    if (!el.name || !(el.name in state.settings)) continue;
    if (el.type === "checkbox") el.checked = !!state.settings[el.name];
    else el.value = state.settings[el.name] ?? "";
  }
  $("#pw-state").textContent = state.settings.passwordSet ? "A password is set." : "No password set — the UI only accepts one-time links from this computer.";
  $("#pw-current").hidden = !state.settings.passwordSet;
  $("#pw-remove").hidden = !state.settings.passwordSet;
  $("#settings-err").textContent = "";
  refreshRates($("#settings-form"));
  renderCatEditor(state.settings.categories || []);
  $("#dlg-settings").showModal();
}
$("#btn-settings").addEventListener("click", openSettings);
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
    toast(res.restartRequired ? "Saved — restart Magnetar to apply web interface changes" : "Settings saved");
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
  if (u.state === "installing") return `Installing Magnetar ${u.latest} — Magnetar will restart…`;
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
    : "This installation cannot update itself — download the new version from the release page.";
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
    toast("Downloading update — Magnetar will restart when it is ready");
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
async function browse(path) {
  $("#folder-err").textContent = "";
  try {
    const d = await api("/api/fs/dirs?path=" + encodeURIComponent(path || ""));
    $("#folder-path").value = d.path;
    $("#folder-roots").replaceChildren(...d.roots.map((r) => h("button", { type: "button", title: r, text: r, onclick: () => browse(r) })));
    const items = [];
    if (d.parent) items.push(h("button", { type: "button", class: "up", onclick: () => browse(d.parent) }, icon("up"), h("span", { text: ".." })));
    for (const name of d.dirs) {
      items.push(h("button", { type: "button", onclick: () => browse(d.path + (d.path.endsWith(d.sep) ? "" : d.sep) + name) }, icon("folder"), h("span", { text: name })));
    }
    if (!d.dirs.length) items.push(h("p", { class: "muted", text: "No sub-folders. Use this folder, or type a new path — it will be created." }));
    $("#folder-list").replaceChildren(...items);
  } catch (e) { $("#folder-err").textContent = e.message; }
}
let folderCallback = null;
function pickFolder(initial, cb) {
  folderTarget = null;
  folderCallback = cb;
  const dlg = $("#dlg-folder");
  dlg.returnValue = "";
  dlg.showModal();
  browse(initial || "");
}
function bindBrowse(b) {
  b.addEventListener("click", () => {
    folderCallback = null;
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

/* ---------- connection health ---------- */
const CONN = {
  connectable: ["Connectable", "Other peers can connect to you — the best speeds are possible."],
  checking: ["Checking connection…", "Magnetar is watching whether other peers can connect to you. This becomes clear a few minutes after a download starts."],
  firewalled: ["Port not reachable", "Peers are not connecting to you, so you can only reach peers that accept incoming connections themselves. Downloads still work but are usually slower."],
  offline: ["No network", "Magnetar cannot reach the BitTorrent network. Check your internet connection, VPN or the “Bind traffic to IP” setting."],
};
function renderConnection() {
  const s = state.stats, c = s.connection || "checking";
  const pill = $("#g-conn");
  pill.className = "conn-pill " + c;
  pill.querySelector("span").textContent = CONN[c][0] + (c === "connectable" ? ` · port ${s.listenPort}` : "");
}
$("#g-conn").addEventListener("click", () => {
  const s = state.stats, c = s.connection || "checking", port = s.listenPort || "?";
  $("#conn-title").textContent = CONN[c][0];
  $("#conn-text").textContent = CONN[c][1];
  const step = (...parts) => h("li", {}, ...parts.map((p) => (typeof p === "string" ? p : h("code", { text: p.code }))));
  const steps = c === "connectable" ? [] : c === "offline" ? [
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
      toast("Category set — moving files");
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
