"use strict";

// ---------- token & API ----------

const TOKEN_KEY = "fattrace.token";

function takeToken() {
  const h = location.hash;
  if (h.startsWith("#t=")) {
    const rest = h.slice(3);
    const amp = rest.indexOf("&");
    const tok = decodeURIComponent(amp < 0 ? rest : rest.slice(0, amp));
    const route = amp < 0 ? "" : rest.slice(amp + 1);
    try { sessionStorage.setItem(TOKEN_KEY, tok); } catch { /* private mode */ }
    const target = route && route.startsWith("/") ? "#" + route : "#/overview";
    history.replaceState(null, "", location.pathname + target);
    return tok;
  }
  try { return sessionStorage.getItem(TOKEN_KEY) || ""; } catch { return ""; }
}

const token = takeToken();

class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

async function api(name, params = {}) {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") q.set(k, String(v));
  }
  const url = "/api/" + name + (q.size ? "?" + q.toString() : "");
  let res;
  try {
    res = await fetch(url, {
      headers: { "X-FatTrace-Token": token },
      cache: "no-store",
    });
  } catch (err) {
    throw new ApiError("无法连接本地服务：" + err.message, 0);
  }
  let body = null;
  try { body = await res.json(); } catch { /* non-JSON error page */ }
  if (!res.ok) {
    const msg = body && body.error ? body.error : "HTTP " + res.status;
    throw new ApiError(msg, res.status);
  }
  return body;
}

// ---------- formatting ----------

function fmtBytes(n, signed = false) {
  if (n === null || n === undefined || Number.isNaN(n)) return "—";
  const neg = n < 0;
  let v = Math.abs(n);
  let s;
  if (v < 1024) {
    s = v + " B";
  } else {
    const units = ["KiB", "MiB", "GiB", "TiB", "PiB"];
    let u = -1;
    do { v /= 1024; u++; } while (v >= 1024 && u < units.length - 1);
    s = (v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2)) + " " + units[u];
  }
  if (neg) return "-" + s;
  return signed && n > 0 ? "+" + s : s;
}

function fmtCount(n) {
  if (n === null || n === undefined) return "—";
  return Number(n).toLocaleString("en-US");
}

function fmtPct(n) {
  return (Math.round(n * 100) / 100).toFixed(2) + "%";
}

function fmtDate(ms) {
  if (!ms) return "—";
  return new Date(ms).toLocaleString("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit",
  });
}

function fmtDuration(ms) {
  if (!ms) return "—";
  if (ms < 1000) return ms + " ms";
  const s = ms / 1000;
  if (s < 90) return s.toFixed(1) + " s";
  return (s / 60).toFixed(1) + " min";
}

function signClass(n) { return n > 0 ? "pos" : n < 0 ? "neg" : "zero"; }
function signedBytes(n) { return fmtBytes(n, true); }

// ---------- DOM helpers ----------

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined && text !== null) e.textContent = text;
  return e;
}

function td(text, cls) {
  const c = el("td", cls || "");
  c.textContent = text === null || text === undefined ? "—" : String(text);
  return c;
}

function tdNode(node, cls) {
  const c = el("td", cls || "");
  c.appendChild(node);
  return c;
}

function dataTable(headers, rows) {
  const t = el("table", "data");
  const thead = el("thead");
  const hr = el("tr");
  for (const h of headers) {
    const th = el("th", h.num ? "num" : "", h.label);
    hr.appendChild(th);
  }
  thead.appendChild(hr);
  t.appendChild(thead);
  const tbody = el("tbody");
  for (const row of rows) {
    const tr = el("tr");
    for (const cell of row) {
      if (cell instanceof Node) tr.appendChild(cell);
      else tr.appendChild(td(cell));
    }
    tbody.appendChild(tr);
  }
  t.appendChild(tbody);
  return t;
}

function panel(title, hint) {
  const p = el("section", "panel");
  const head = el("div", "panel-head");
  head.appendChild(el("h2", null, title));
  if (hint) head.appendChild(el("span", "hint", hint));
  p.appendChild(head);
  return p;
}

// ---------- banners ----------

const bannerZone = document.getElementById("banners");

function clearBanners() { bannerZone.textContent = ""; }

function addBanner(kind, title, detail) {
  const b = el("div", "banner " + kind);
  b.appendChild(el("b", null, title));
  if (detail) b.appendChild(el("span", null, detail));
  bannerZone.appendChild(b);
}

// ---------- redraw registry ----------

const redraws = [];

function registerRedraw(fn) { redraws.push(fn); }

let resizeTimer = 0;
window.addEventListener("resize", () => {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(() => {
    for (const fn of redraws) {
      try { fn(); } catch { /* a dead canvas */ }
    }
  }, 100);
});

// ---------- treemap ----------

const palette = [
  "#f0a33c", "#e2842f", "#d96a3a", "#cd5a56", "#b9557e",
  "#9760ab", "#7469c9", "#4f83c9", "#3f9fb8", "#3fa98e",
];

const UI_FONT = '"Segoe UI", "Microsoft YaHei UI", "Microsoft YaHei", system-ui, sans-serif';

const tooltip = document.getElementById("tooltip");

function hideTooltip() { tooltip.style.opacity = "0"; }

function startTooltip() { tooltip.textContent = ""; }

function fillTooltip(title, meta) {
  tooltip.textContent = "";
  tooltip.appendChild(el("div", "tt-title", title));
  if (meta) tooltip.appendChild(el("div", "tt-meta", meta));
}

function moveTooltip(clientX, clientY) {
  const pad = 14;
  const box = tooltip.getBoundingClientRect();
  let x = clientX + pad;
  let y = clientY + pad;
  if (x + box.width > window.innerWidth - 8) x = Math.max(8, clientX - box.width - pad);
  if (y + box.height > window.innerHeight - 8) y = Math.max(8, clientY - box.height - pad);
  tooltip.style.left = x + "px";
  tooltip.style.top = y + "px";
  tooltip.style.opacity = "1";
}

function worstRow(areas, side) {
  let sum = 0, max = 0, min = Infinity;
  for (const a of areas) {
    sum += a;
    if (a > max) max = a;
    if (a < min) min = a;
  }
  if (!(sum > 0) || min === Infinity) return Infinity;
  const s2 = sum * sum, side2 = side * side;
  return Math.max((side2 * max) / s2, s2 / (side2 * min));
}

function squarify(items, x, y, w, h) {
  const list = items
    .filter((i) => i.value > 0)
    .map((i) => ({ item: i, value: i.value }))
    .sort((a, b) => b.value - a.value);
  const total = list.reduce((s, i) => s + i.value, 0);
  if (!(total > 0) || w <= 0 || h <= 0) return [];
  const scale = (w * h) / total;
  const out = [];
  let rx = x, ry = y, rw = w, rh = h, idx = 0;
  while (idx < list.length && rw > 0.5 && rh > 0.5) {
    const side = Math.min(rw, rh);
    let areas = [], row = [], best = Infinity, j = idx;
    while (j < list.length) {
      const area = list[j].value * scale;
      const trial = areas.concat(area);
      const ratio = worstRow(trial, side);
      if (row.length === 0 || ratio <= best) {
        best = ratio;
        areas = trial;
        row.push(list[j]);
        j++;
      } else {
        break;
      }
    }
    const rowSum = areas.reduce((s, a) => s + a, 0);
    if (rw >= rh) {
      const rwRow = rowSum / rh;
      let oy = ry;
      for (let k = 0; k < row.length; k++) {
        const hh = areas[k] / rwRow;
        out.push({ item: row[k].item, x: rx, y: oy, w: rwRow, h: hh });
        oy += hh;
      }
      rx += rwRow;
      rw -= rwRow;
    } else {
      const rhRow = rowSum / rw;
      let ox = rx;
      for (let k = 0; k < row.length; k++) {
        const ww = areas[k] / rhRow;
        out.push({ item: row[k].item, x: ox, y: ry, w: ww, h: rhRow });
        ox += ww;
      }
      ry += rhRow;
      rh -= rhRow;
    }
    idx += row.length;
  }
  return out;
}

function hatchPattern(ctx) {
  const p = document.createElement("canvas");
  p.width = p.height = 8;
  const c = p.getContext("2d");
  c.strokeStyle = "rgba(255,255,255,0.30)";
  c.lineWidth = 1.5;
  c.beginPath();
  c.moveTo(-2, 10); c.lineTo(10, -2);
  c.moveTo(2, 10); c.lineTo(10, 2);
  c.stroke();
  return ctx.createPattern(p, "repeat");
}

function truncateText(ctx, text, maxWidth) {
  if (ctx.measureText(text).width <= maxWidth) return text;
  let s = text;
  while (s.length > 1 && ctx.measureText(s + "…").width > maxWidth) s = s.slice(0, -1);
  return s + "…";
}

// createTreemap draws items ({label, value, unknown, payload}) into canvas and
// returns nothing; opts.onPick(payload) fires on click.
function createTreemap(canvas, items, opts = {}) {
  const wrap = canvas.parentElement;
  const aria = opts.aria || "目录面积图";
  canvas.setAttribute("role", "img");
  canvas.setAttribute("aria-label", aria);
  let rects = [];
  let hover = -1;
  let hatch = null;

  function draw() {
    if (wrap.clientWidth <= 0) return; // still detached in a DocumentFragment
    const dpr = window.devicePixelRatio || 1;
    const W = Math.max(320, wrap.clientWidth);
    const H = opts.height || Math.max(300, Math.min(520, Math.round(W * 0.42)));
    canvas.width = Math.round(W * dpr);
    canvas.height = Math.round(H * dpr);
    canvas.style.height = H + "px";
    const ctx = canvas.getContext("2d");
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, W, H);
    rects = squarify(items, 0, 0, W, H);
    if (!rects.length) {
      ctx.fillStyle = "#8b98a9";
      ctx.font = "14px " + UI_FONT;
      ctx.textAlign = "center";
      ctx.textBaseline = "middle";
      ctx.fillText(opts.emptyText || "没有可绘制的内容", W / 2, H / 2);
      return;
    }
    if (!hatch) hatch = hatchPattern(ctx);
    rects.forEach((r, i) => {
      const pad = 1.5;
      const x = r.x + pad, y = r.y + pad;
      const w = Math.max(0, r.w - pad * 2), h = Math.max(0, r.h - pad * 2);
      if (w <= 0 || h <= 0) return;
      ctx.fillStyle = palette[i % palette.length];
      ctx.globalAlpha = i === hover ? 1 : 0.82;
      ctx.fillRect(x, y, w, h);
      ctx.globalAlpha = 1;
      if (r.item.unknown) {
        ctx.save();
        ctx.beginPath();
        ctx.rect(x, y, w, h);
        ctx.clip();
        ctx.fillStyle = hatch;
        ctx.fillRect(x, y, w, h);
        ctx.restore();
      }
      if (i === hover) {
        ctx.strokeStyle = "#ffffff";
        ctx.lineWidth = 2;
        ctx.strokeRect(x + 1, y + 1, w - 2, h - 2);
      }
      const label = r.item.label;
      if (w > 240 && h > 150) {
        ctx.save();
        ctx.fillStyle = "rgba(10,12,15,0.9)";
        ctx.textAlign = "center";
        ctx.textBaseline = "middle";
        ctx.font = "700 19px " + UI_FONT;
        ctx.fillText(truncateText(ctx, label, w - 60), x + w / 2, y + h / 2 - 13);
        ctx.font = "600 14px " + UI_FONT;
        ctx.fillText(fmtBytes(r.item.value, !!r.item.signed), x + w / 2, y + h / 2 + 15);
        ctx.restore();
      } else if (w > 76 && h > 30) {
        ctx.save();
        ctx.beginPath();
        ctx.rect(x + 6, y + 4, w - 12, h - 8);
        ctx.clip();
        ctx.fillStyle = "rgba(10,12,15,0.88)";
        ctx.font = "600 12px " + UI_FONT;
        ctx.textAlign = "left";
        ctx.textBaseline = "top";
        ctx.fillText(truncateText(ctx, label, w - 14), x + 7, y + 6);
        if (h > 48) {
          ctx.font = "11px " + UI_FONT;
          ctx.fillText(fmtBytes(r.item.value, !!r.item.signed), x + 7, y + 22);
        }
        ctx.restore();
      }
    });
  }

  function hitTest(px, py) {
    for (let i = rects.length - 1; i >= 0; i--) {
      const r = rects[i];
      if (px >= r.x && px <= r.x + r.w && py >= r.y && py <= r.y + r.h) return i;
    }
    return -1;
  }

  canvas.addEventListener("mousemove", (ev) => {
    const box = canvas.getBoundingClientRect();
    const i = hitTest(ev.clientX - box.left, ev.clientY - box.top);
    if (i !== hover) {
      hover = i;
      requestAnimationFrame(draw);
    }
    if (i < 0) {
      hideTooltip();
      return;
    }
    const it = rects[i].item;
    fillTooltip(it.path || it.label, it.tip || fmtBytes(it.value, !!it.signed));
    moveTooltip(ev.clientX, ev.clientY);
  });

  canvas.addEventListener("mouseleave", () => {
    hover = -1;
    hideTooltip();
    requestAnimationFrame(draw);
  });

  canvas.addEventListener("click", (ev) => {
    const box = canvas.getBoundingClientRect();
    const i = hitTest(ev.clientX - box.left, ev.clientY - box.top);
    if (i >= 0 && opts.onPick) opts.onPick(rects[i].item);
  });

  if (opts.onPick) canvas.style.cursor = "pointer";
  registerRedraw(draw);
  if (typeof ResizeObserver === "function") {
    new ResizeObserver(() => {
      if (wrap.clientWidth > 0) draw();
    }).observe(wrap);
  }
  draw();
}

// ---------- sparkline ----------

function drawSparkline(canvas, points) {
  const wrap = canvas.parentElement;

  function draw() {
    if (wrap.clientWidth <= 0) return; // still detached in a DocumentFragment
    const dpr = window.devicePixelRatio || 1;
    const W = Math.max(240, wrap.clientWidth);
    const H = 120;
    canvas.width = Math.round(W * dpr);
    canvas.height = Math.round(H * dpr);
    const ctx = canvas.getContext("2d");
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, W, H);
    if (!points.length) return;
    const vals = points.map((p) => p.unique_alloc_bytes);
    let min = Math.min(...vals), max = Math.max(...vals);
    if (min === max) { min -= 1; max += 1; }
    const padPx = 12;
    const xAt = (i) => points.length < 2 ? W / 2 : padPx + (i * (W - padPx * 2)) / (points.length - 1);
    const yAt = (v) => H - padPx - ((v - min) * (H - padPx * 2)) / (max - min);

    ctx.beginPath();
    ctx.moveTo(xAt(0), yAt(vals[0]));
    for (let i = 1; i < vals.length; i++) ctx.lineTo(xAt(i), yAt(vals[i]));
    ctx.lineTo(xAt(vals.length - 1), H - 2);
    ctx.lineTo(xAt(0), H - 2);
    ctx.closePath();
    ctx.fillStyle = "rgba(240,163,60,0.14)";
    ctx.fill();

    ctx.beginPath();
    ctx.moveTo(xAt(0), yAt(vals[0]));
    for (let i = 1; i < vals.length; i++) ctx.lineTo(xAt(i), yAt(vals[i]));
    ctx.strokeStyle = "#f0a33c";
    ctx.lineWidth = 2;
    ctx.stroke();

    points.forEach((p, i) => {
      ctx.beginPath();
      ctx.arc(xAt(i), yAt(vals[i]), 3, 0, Math.PI * 2);
      ctx.fillStyle = "#f0a33c";
      ctx.fill();
    });

    canvas.onmousemove = (ev) => {
      const box = canvas.getBoundingClientRect();
      const px = ev.clientX - box.left;
      let best = 0, bestDist = Infinity;
      for (let i = 0; i < points.length; i++) {
        const d = Math.abs(xAt(i) - px);
        if (d < bestDist) { bestDist = d; best = i; }
      }
      const p = points[best];
      fillTooltip("#" + p.id + " " + fmtDate(p.started_at_ms),
        fmtBytes(p.unique_alloc_bytes) + " 唯一实占 · " + fmtCount(p.files) + " 文件");
      moveTooltip(ev.clientX, ev.clientY);
    };
    canvas.onmouseleave = hideTooltip;
  }

  registerRedraw(draw);
  if (typeof ResizeObserver === "function") {
    new ResizeObserver(() => {
      if (wrap.clientWidth > 0) draw();
    }).observe(wrap);
  }
  draw();
}

// ---------- diff rendering ----------

function growthTiles(D) {
  const dirs = (D.top_growth_dirs || []).filter((d) => d.delta_alloc > 0);
  const norm = (p) => (p || "").replace(/[\\/]+$/, "").toLowerCase();
  const index = new Map();
  dirs.forEach((d, i) => index.set(norm(d.path), i));
  const own = dirs.map((d) => d.delta_alloc);
  dirs.forEach((d, i) => {
    let p = parentPath(d.path);
    while (p) {
      const j = index.get(norm(p));
      if (j !== undefined) {
        own[j] -= d.delta_alloc;
        break;
      }
      const up = parentPath(p);
      if (!up || up === p) break;
      p = up;
    }
  });
  const tiles = [];
  dirs.forEach((d, i) => {
    if (own[i] <= 0) return;
    tiles.push({
      label: baseName(d.path),
      path: d.path,
      value: own[i],
      signed: true,
      unknown: !!d.unknown_after,
      tip: signedBytes(own[i]) + " 实占增长" + (d.files_after ? " · " + fmtCount(d.files_after) + " 文件" : ""),
      payload: { frn: d.frn },
    });
  });
  const sum = tiles.reduce((s, t) => s + t.value, 0);
  const other = (D.totals ? D.totals.delta_alloc : 0) - sum;
  if (other > 0) {
    tiles.push({ label: "其他目录", path: "未进入 Top 榜的其余目录", value: other, signed: true, tip: signedBytes(other) });
  }
  return tiles;
}

function parentPath(p) {
  if (!p) return "";
  const clean = p.replace(/[\\/]+$/, "");
  const i = clean.lastIndexOf("\\");
  if (i < 0) return "";
  const up = clean.slice(0, i);
  if (/^[A-Za-z]:$/.test(up)) return up + "\\";
  return up;
}

function baseName(p) {
  if (!p) return "";
  const clean = p.replace(/[\\/]+$/, "");
  const i = clean.lastIndexOf("\\");
  return i < 0 ? clean : clean.slice(i + 1) || clean;
}

function kpiRow(totals, from, to, check, ex) {
  const zone = el("div", "kpis");
  const add = (label, value, sub, cls) => {
    const card = el("div", "kpi");
    card.appendChild(el("div", "label", label));
    card.appendChild(el("div", "value" + (cls ? " " + cls : ""), value));
    if (sub) card.appendChild(el("div", "sub", sub));
    zone.appendChild(card);
  };
  add("净增长（实占）", signedBytes(totals.delta_alloc),
    "逻辑 " + signedBytes(totals.delta_bytes), signClass(totals.delta_alloc));
  add("文件数", (to.files - from.files >= 0 ? "+" : "") + fmtCount(to.files - from.files),
    fmtCount(from.files) + " → " + fmtCount(to.files));
  add("唯一实占", fmtBytes(to.unique_alloc_bytes),
    fmtBytes(from.unique_alloc_bytes) + " → " + fmtBytes(to.unique_alloc_bytes));
  add("变更事件",
    fmtCount(totals.files_added + totals.files_deleted + totals.files_grown + totals.files_shrunk),
    fmtCount(totals.files_added) + " 新增 · " + fmtCount(totals.files_deleted) + " 删除 · " +
    fmtCount(totals.files_grown) + " 变大 · " + fmtCount(totals.files_shrunk) + " 变小");
  if (check.skipped) {
    add("卷级校验", "SKIPPED", volumeNote(check), "zero");
  } else {
    add("卷级校验", check.ok ? "OK" : "WARN",
      "偏差 " + fmtPct(check.diff_pct) + " · 根 Δ " + signedBytes(check.root_delta_alloc),
      check.ok ? "ok" : "danger");
  }
  add("覆盖率", to.coverage_pct > 0 ? fmtPct(to.coverage_pct) : "—",
    check.unknown_dirs_after > 0 ? check.unknown_dirs_after + " 个目录不可读" : "无不可读目录",
    to.coverage_pct > 0 && to.coverage_pct < 95 ? "warn" : "");
  if (ex) {
    add("增长解释率", ex.growth_bytes > 0 ? fmtPct(ex.explained_pct) : "—",
      fmtBytes(ex.explained_bytes) + " / " + fmtBytes(ex.growth_bytes) + " 已解释 · " +
      ex.rules_loaded + " 条规则",
      ex.growth_bytes > 0 && ex.explained_pct < 70 ? "warn" : "ok");
  }
  return zone;
}

function volumeNote(check) {
  if (check.skipped) return "当前快照不适合做卷级校验（非卷根或缺少卷用量记录）。";
  if (check.ok) return "根目录增量与卷已用增量在容差内一致。";
  return "根目录增量与卷已用增量不一致：受保护或排除目录的增量可能未被计入。";
}

function diffBanners(D) {
  const check = D.volume_check;
  const to = D.to_snapshot;
  if (to.coverage_pct > 0 && to.coverage_pct < 95) {
    addBanner("warn", "覆盖率 " + fmtPct(to.coverage_pct),
      "低于 95%：受保护或不可读目录未计入，实际增量可能更高。");
  }
  if (check.skipped) {
    addBanner("info", "卷级校验已跳过", volumeNote(check));
  } else if (!check.ok) {
    addBanner("danger", "卷级校验未通过 · 偏差 " + fmtPct(check.diff_pct),
      volumeNote(check) + "（根 Δ " + signedBytes(check.root_delta_alloc) +
      "，卷 Δ " + signedBytes(check.disk_delta_alloc) + "）");
  }
  if (D.warnings && D.warnings.length) {
    addBanner("warn", "数据质量警告 " + D.warnings.length + " 条", "详见下方“校验与警告”。");
  }
  if (D.explanation && D.explanation.explained_pct < 70) {
    addBanner("warn", "增长解释率 " + fmtPct(D.explanation.explained_pct),
      "低于 70% 目标：" + fmtBytes(D.explanation.unexplained_bytes) + " 增长没有命中规则，主要是新文件类型或用户数据。");
  }
}

function alertBanners(rep) {
  for (const a of rep.alerts || []) {
    if (!a.triggered) continue;
    if (a.muted) {
      addBanner("info", "增长告警已静默",
        a.message + " · 静默至 " + fmtDate(a.muted_until_ms));
    } else {
      addBanner("danger", "增长告警", a.message);
    }
  }
}

function ruleBadge(rule) {
  if (!rule) return null;
  const b = el("span", "badge rule" + (rule.confidence === "low" ? " low" : ""),
    rule.category_label || rule.category);
  b.title = rule.name + " · " + rule.id + " · 置信度 " + rule.confidence;
  return b;
}

function fileRows(items, mode) {
  const rows = [];
  for (const c of items || []) {
    const delta = td(signedBytes(c.delta_alloc), "num " + signClass(c.delta_alloc));
    let path;
    if (mode === "moved" && (c.old_path || c.old_name)) {
      path = el("div", "path");
      const old = c.old_path || c.old_name;
      const cur = c.path || c.name;
      path.appendChild(document.createTextNode(old));
      path.appendChild(el("span", "dim", "  →  "));
      path.appendChild(el("span", null, cur));
    } else {
      path = td(c.path || c.name, "path");
    }
    const pathCell = path instanceof Node && path.tagName === "TD" ? path : tdNode(path, "path");
    const badge = ruleBadge(c.rule);
    if (badge) pathCell.appendChild(badge);
    const size = c.kind === "deleted"
      ? td(fmtBytes(c.alloc_before), "num")
      : td(fmtBytes(c.alloc_before) + " → " + fmtBytes(c.alloc_after), "num");
    const meta = td(c.mtime_after_ms ? fmtDate(c.mtime_after_ms) : (c.mtime_before_ms ? fmtDate(c.mtime_before_ms) : "—"), "dim");
    rows.push([delta, pathCell, size, meta]);
  }
  return rows;
}

function dirRows(items) {
  const rows = [];
  for (const d of items || []) {
    const pathCell = el("td", "path");
    pathCell.appendChild(document.createTextNode(d.path || d.name));
    if (d.unknown_after) pathCell.appendChild(el("span", "badge unknown", "含不可读"));
    const badge = ruleBadge(d.rule);
    if (badge) pathCell.appendChild(badge);
    rows.push([
      td(signedBytes(d.delta_alloc), "num " + signClass(d.delta_alloc)),
      pathCell,
      td(fmtBytes(d.total_alloc_before) + " → " + fmtBytes(d.total_alloc_after), "num"),
      td(fmtCount(d.files_after), "num"),
    ]);
  }
  return rows;
}

function checkPanel(D) {
  const p = panel("卷级校验与警告");
  const check = D.volume_check;
  const rows = [
    ["根目录实占 Δ", signedBytes(check.root_delta_alloc)],
    ["卷用量 Δ", check.skipped ? "—" : signedBytes(check.disk_delta_alloc)],
    ["偏差", check.skipped ? "—" : fmtPct(check.diff_pct)],
    ["不可读目录（to）", fmtCount(check.unknown_dirs_after)],
    ["结论", check.skipped ? "已跳过" : (check.ok ? "通过" : "未通过")],
  ].map(([k, v]) => [td(k, "dim"), td(v)]);
  p.appendChild(dataTable([{ label: "项目" }, { label: "值" }], rows));
  if (check.message) {
    const note = el("div", "notice", volumeNote(check));
    note.title = check.message;
    p.appendChild(note);
  }
  if (D.warnings && D.warnings.length) {
    const ul = el("ul");
    for (const w of D.warnings) ul.appendChild(el("li", null, w));
    p.appendChild(ul);
  }
  return p;
}

function explainPanel(ex) {
  const p = panel("增长解释", "按规则归类“新增 + 变大”的实占增长；规则只做标注，不会删除任何文件");
  if (!ex) {
    p.appendChild(el("div", "empty", "本次对比没有解释数据。"));
    return p;
  }
  p.appendChild(el("div", "notice",
    "已解释 " + fmtBytes(ex.explained_bytes) + " / " + fmtBytes(ex.growth_bytes) +
    "（" + (ex.growth_bytes > 0 ? fmtPct(ex.explained_pct) : "—") + "）· 未解释 " +
    fmtCount(ex.unexplained_total) + " 个文件 · " + fmtCount(ex.rules_loaded) + " 条规则"));

  const grid = el("div", "explain-grid");
  const left = el("div");
  left.appendChild(el("div", "subhead", "已解释增长 · 按类别"));
  if (!(ex.categories || []).length) {
    left.appendChild(el("div", "empty", "没有命中的增长"));
  } else {
    const max = Math.max(...ex.categories.map((c) => c.bytes));
    for (const c of ex.categories) {
      const row = el("div", "cat-row");
      const head = el("div", "cat-head");
      head.appendChild(el("span", "cat-name", c.label));
      head.appendChild(el("span", "cat-meta", fmtCount(c.files) + " 文件"));
      row.appendChild(head);
      row.appendChild(el("div", "cat-bytes", fmtBytes(c.bytes)));
      const bar = el("div", "cat-bar");
      const fill = el("i");
      fill.style.width = (max > 0 ? Math.max(2, Math.round(c.bytes / max * 100)) : 0) + "%";
      bar.appendChild(fill);
      row.appendChild(bar);
      left.appendChild(row);
    }
  }
  grid.appendChild(left);

  const right = el("div");
  right.appendChild(el("div", "subhead", "未解释增长 · Top" +
    (ex.unexplained_total > (ex.unexplained || []).length
      ? "（显示前 " + (ex.unexplained || []).length + " / " + fmtCount(ex.unexplained_total) + "）"
      : "")));
  if (!(ex.unexplained || []).length) {
    right.appendChild(el("div", "empty", "全部增长都命中了规则"));
  } else {
    right.appendChild(dataTable(
      [{ label: "路径" }, { label: "类型" }, { label: "实占 Δ", num: true }],
      ex.unexplained.map((u) => [
        td(u.path, "path"),
        td(u.kind === "added" ? "新增" : "变大", "dim"),
        td(signedBytes(u.delta_alloc), "num pos"),
      ])));
  }
  grid.appendChild(right);
  p.appendChild(grid);
  return p;
}

function renderDiff(container, D) {
  diffBanners(D);
  const head = el("div", "page-head");
  const h1 = el("h1", null, D.root);
  h1.appendChild(el("span", "chip " + signClass(D.totals.delta_alloc),
    signedBytes(D.totals.delta_alloc)));
  head.appendChild(h1);
  const meta = el("div", "meta");
  const spanDays = (D.to_snapshot.started_at_ms - D.from_snapshot.started_at_ms) / 86400000;
  meta.appendChild(el("b", null, "#" + D.from_snapshot.id + " " + fmtDate(D.from_snapshot.started_at_ms)));
  meta.appendChild(document.createTextNode("  →  "));
  meta.appendChild(el("b", null, "#" + D.to_snapshot.id + " " + fmtDate(D.to_snapshot.started_at_ms)));
  meta.appendChild(document.createTextNode(" · 跨度 " + spanDays.toFixed(1) + " 天"));
  head.appendChild(meta);
  container.appendChild(head);

  container.appendChild(kpiRow(D.totals, D.from_snapshot, D.to_snapshot, D.volume_check, D.explanation));

  // treemap of intrinsic directory growth
  const p = panel("目录增长 treemap", "面积 = 目录自身实占增长，已扣减上榜子目录的增量；点击色块可下钻");
  const wrap = el("div", "treemap-wrap");
  const canvas = el("canvas", "treemap");
  wrap.appendChild(canvas);
  p.appendChild(wrap);
  const legend = el("div", "legend");
  const sw = el("span");
  sw.appendChild(el("i", "swatch"));
  sw.appendChild(document.createTextNode("目录增长"));
  legend.appendChild(sw);
  const hw = el("span");
  hw.appendChild(el("i", "hatch-swatch"));
  hw.appendChild(document.createTextNode("含不可读目录"));
  legend.appendChild(hw);
  p.appendChild(legend);
  container.appendChild(p);
  createTreemap(canvas, growthTiles(D), {
    aria: "目录增长面积图",
    emptyText: "这个区间没有目录增长",
    onPick: (item) => {
      if (item.payload && item.payload.frn) {
        location.hash = "#/explorer?snapshot=" + D.to_snapshot.id + "&frn=" + item.payload.frn;
      }
    },
  });

  container.appendChild(explainPanel(D.explanation));

  const grid1 = el("div", "grid-2");
  const pg = panel("文件增长 Top", fmtCount((D.top_growth || []).length) + " 条");
  pg.appendChild(dataTable(
    [{ label: "实占 Δ", num: true }, { label: "路径" }, { label: "实占 前 → 后", num: true }, { label: "修改时间" }],
    fileRows(D.top_growth, "changed")));
  grid1.appendChild(pg);
  const pd = panel("文件删除 Top", fmtCount((D.top_deleted || []).length) + " 条");
  pd.appendChild(dataTable(
    [{ label: "实占 Δ", num: true }, { label: "路径" }, { label: "删除前", num: true }, { label: "修改时间" }],
    fileRows(D.top_deleted, "deleted")));
  grid1.appendChild(pd);
  container.appendChild(grid1);

  const grid2 = el("div", "grid-2");
  const pgd = panel("目录增长 Top", "move-adjusted 归因");
  pgd.appendChild(dataTable(
    [{ label: "实占 Δ", num: true }, { label: "目录" }, { label: "递归实占 前 → 后", num: true }, { label: "文件", num: true }],
    dirRows(D.top_growth_dirs)));
  grid2.appendChild(pgd);
  const psd = panel("目录缩减 Top", "纯移动不计入");
  psd.appendChild(dataTable(
    [{ label: "实占 Δ", num: true }, { label: "目录" }, { label: "递归实占 前 → 后", num: true }, { label: "文件", num: true }],
    dirRows(D.top_shrunk_dirs)));
  grid2.appendChild(psd);
  container.appendChild(grid2);

  if ((D.moves || []).length) {
    const pm = panel("移动 / 改名", fmtCount(D.moves.length) + " 条");
    pm.appendChild(dataTable(
      [{ label: "实占 Δ", num: true }, { label: "旧 → 新" }, { label: "实占 前 → 后", num: true }, { label: "修改时间" }],
      fileRows(D.moves, "moved")));
    container.appendChild(pm);
  }

  container.appendChild(checkPanel(D));
}

// ---------- views ----------

function pickDefaultSnapshot(snapshots) {
  const complete = snapshots.filter((s) => s.status === "complete");
  const volumeRoot = complete.find((s) => /^[A-Za-z]:\\?$/.test(s.root));
  return volumeRoot || complete[0] || null;
}

function snapshotLabel(s) {
  return "#" + s.id + " · " + fmtDate(s.started_at_ms) + " · " + s.root;
}

function fillSnapshotSelect(select, snapshots, selectedID) {
  select.textContent = "";
  for (const s of snapshots) {
    const opt = el("option", null, snapshotLabel(s));
    opt.value = String(s.id);
    if (s.id === selectedID) opt.selected = true;
    select.appendChild(opt);
  }
}

function sparkPanel(title, subtitle, points) {
  const p = panel(title, subtitle);
  if (points.length < 2) {
    p.appendChild(el("div", "empty", "至少需要两次完整卷根快照才能画趋势。"));
    return p;
  }
  const wrap = el("div", "sparkline-wrap");
  const canvas = el("canvas", "sparkline");
  wrap.appendChild(canvas);
  p.appendChild(wrap);
  const meta = el("div", "spark-meta");
  const vals = points.map((p) => p.unique_alloc_bytes);
  const lo = Math.min(...vals), hi = Math.max(...vals);
  meta.appendChild(el("span", null, fmtDate(points[0].started_at_ms)));
  meta.appendChild(el("span", null, "区间 " + fmtBytes(lo) + " → " + fmtBytes(hi)));
  meta.appendChild(el("span", null, fmtDate(points[points.length - 1].started_at_ms)));
  p.appendChild(meta);
  drawSparkline(canvas, points);
  return p;
}

async function viewOverview(root) {
  const [D, alertRep] = await Promise.all([
    api("diff"),
    api("alerts").catch(() => ({ alerts: [] })),
  ]);
  renderDiff(root, D);
  alertBanners(alertRep);
  try {
    const trend = await api("trend", { volume: D.volume_id, limit: 8 });
    root.appendChild(sparkPanel("趋势 · " + D.root, "最近 " + trend.points.length + " 次完整卷根快照的唯一实占",
      trend.points));
  } catch (err) {
    const p = panel("趋势");
    p.appendChild(el("div", "empty", "趋势加载失败：" + err.message));
    root.appendChild(p);
  }
}

async function viewExplorer(root, params) {
  const snapsAll = (await api("snapshots", { limit: 300 })).snapshots;
  const complete = snapsAll.filter((s) => s.status === "complete");
  if (!complete.length) {
    root.appendChild(el("div", "error-box", "还没有完整快照，先运行 fat snap。"));
    return;
  }
  const wanted = Number(params.get("snapshot") || 0);
  const snap = complete.find((s) => s.id === wanted) || pickDefaultSnapshot(snapsAll);
  const frn = Number(params.get("frn") || 0);
  const listing = await api("dir", { snapshot: snap.id, frn });

  const head = el("div", "page-head");
  const h1 = el("h1", null, listing.path || listing.dir.name);
  if (listing.dir.unknown) h1.appendChild(el("span", "badge unknown", "含不可读目录"));
  head.appendChild(h1);
  const meta = el("div", "meta");
  meta.appendChild(el("b", null, "#" + snap.id + " " + fmtDate(snap.started_at_ms)));
  meta.appendChild(document.createTextNode(" · " + snap.kind + " · 递归实占 " +
    fmtBytes(listing.dir.total_alloc_bytes) + " · 递归文件 " + fmtCount(listing.dir.total_files)));
  head.appendChild(meta);

  const controls = el("div", "controls");
  const sel = el("select");
  sel.setAttribute("aria-label", "选择快照");
  fillSnapshotSelect(sel, complete, snap.id);
  sel.addEventListener("change", () => {
    location.hash = "#/explorer?snapshot=" + sel.value;
  });
  const label = el("label", null, "快照");
  controls.appendChild(label);
  controls.appendChild(sel);
  head.appendChild(controls);
  root.appendChild(head);

  const crumb = el("div", "crumb");
  listing.breadcrumb.forEach((d, i) => {
    if (i > 0) crumb.appendChild(el("span", "sep", "›"));
    const isLast = i === listing.breadcrumb.length - 1;
    if (isLast) {
      crumb.appendChild(el("span", "current", d.name));
    } else {
      const a = el("a", null, d.name);
      a.href = "#/explorer?snapshot=" + snap.id + "&frn=" + d.frn;
      crumb.appendChild(a);
    }
  });
  if (frn && listing.dir.parent_frn) {
    const up = el("button", null, "↑ 上级");
    up.addEventListener("click", () => {
      location.hash = "#/explorer?snapshot=" + snap.id + "&frn=" + listing.dir.parent_frn;
    });
    crumb.appendChild(up);
  }
  root.appendChild(crumb);

  const p = panel("子目录 treemap", "面积 = 子目录递归实占；点击下钻");
  const wrap = el("div", "treemap-wrap");
  const canvas = el("canvas", "treemap");
  wrap.appendChild(canvas);
  p.appendChild(wrap);
  if (listing.dirs_truncated) {
    p.appendChild(el("div", "notice",
      "仅显示实占最大的 " + listing.dirs.length + " 个目录（共 " + fmtCount(listing.dirs_total) + " 个）。"));
  }
  root.appendChild(p);

  const tiles = listing.dirs.map((d) => ({
    label: d.name,
    path: (listing.path.replace(/\\$/, "") + "\\" + d.name),
    value: d.alloc_size,
    unknown: d.unknown,
    tip: fmtBytes(d.alloc_size) + " 实占 · " + fmtCount(d.files) + " 文件",
    payload: { frn: d.frn },
  }));
  createTreemap(canvas, tiles, {
    aria: "子目录实占面积图",
    emptyText: "这个目录没有子目录",
    onPick: (item) => {
      if (item.payload && item.payload.frn) {
        location.hash = "#/explorer?snapshot=" + snap.id + "&frn=" + item.payload.frn;
      }
    },
  });

  const grid = el("div", "grid-2");
  const pd = panel("子目录", fmtCount(listing.dirs_total) + " 个");
  pd.appendChild(dataTable(
    [{ label: "名称" }, { label: "递归实占", num: true }, { label: "递归逻辑", num: true }, { label: "文件", num: true }],
    listing.dirs.map((d) => {
      const cell = el("td", "path");
      const a = el("a", null, d.name);
      a.href = "#/explorer?snapshot=" + snap.id + "&frn=" + d.frn;
      cell.appendChild(a);
      if (d.unknown) cell.appendChild(el("span", "badge unknown", "含不可读"));
      return [cell, td(fmtBytes(d.alloc_size), "num"), td(fmtBytes(d.size), "num"), td(fmtCount(d.files), "num")];
    })));
  grid.appendChild(pd);

  const fileHint = listing.files_expired
    ? "文件明细已按保留策略清理，仅保留目录聚合"
    : fmtCount(listing.files_total) + " 个" +
      (listing.files_truncated ? "（显示前 " + listing.files.length + "）" : "");
  const pf = panel("文件", fileHint);
  pf.appendChild(dataTable(
    [{ label: "名称" }, { label: "实占", num: true }, { label: "逻辑", num: true }, { label: "修改时间" }],
    listing.files.map((f) => [td(f.name, "path"), td(fmtBytes(f.alloc_size), "num"),
      td(fmtBytes(f.size), "num"), td(f.mtime_ms ? fmtDate(f.mtime_ms) : "—", "dim")])));
  grid.appendChild(pf);
  root.appendChild(grid);
}

async function viewCompare(root, params) {
  const snapsAll = (await api("snapshots", { limit: 500 })).snapshots;
  const complete = snapsAll.filter((s) => s.status === "complete");
  const fromParam = params.get("from") || "";
  const toParam = params.get("to") || "";
  const D = await api("diff", { from: fromParam, to: toParam });

  const controls = el("div", "controls");
  const fromSel = el("select");
  fromSel.setAttribute("aria-label", "起始快照");
  const fromOpt = el("option", null, "自动（上一个 complete）");
  fromOpt.value = "";
  fromSel.appendChild(fromOpt);
  for (const s of complete) {
    const o = el("option", null, snapshotLabel(s));
    o.value = String(s.id);
    if (s.id === D.from_snapshot.id) o.selected = true;
    fromSel.appendChild(o);
  }
  const toSel = el("select");
  toSel.setAttribute("aria-label", "结束快照");
  const toOpt = el("option", null, "自动（最新 complete）");
  toOpt.value = "";
  toSel.appendChild(toOpt);
  for (const s of complete) {
    const o = el("option", null, snapshotLabel(s));
    o.value = String(s.id);
    if (s.id === D.to_snapshot.id) o.selected = true;
    toSel.appendChild(o);
  }
  const go = () => {
    const qs = new URLSearchParams();
    if (fromSel.value) qs.set("from", fromSel.value);
    if (toSel.value) qs.set("to", toSel.value);
    const q = qs.toString();
    location.hash = "#/compare" + (q ? "?" + q : "");
  };
  fromSel.addEventListener("change", go);
  toSel.addEventListener("change", go);
  controls.appendChild(el("label", null, "从"));
  controls.appendChild(fromSel);
  controls.appendChild(el("label", null, "到"));
  controls.appendChild(toSel);
  const swap = el("button", null, "交换");
  swap.addEventListener("click", () => {
    const a = fromSel.value, b = toSel.value;
    const qs = new URLSearchParams();
    if (b) qs.set("from", b);
    if (a) qs.set("to", a);
    const q = qs.toString();
    location.hash = "#/compare" + (q ? "?" + q : "");
  });
  controls.appendChild(swap);
  root.appendChild(controls);

  renderDiff(root, D);
}

async function viewTimeline(root) {
  const [st, snapsResp, dailyResp] = await Promise.all([
    api("status"),
    api("snapshots", { limit: 300 }),
    api("daily", { limit: 90 }).catch(() => ({ days: [] })),
  ]);
  const snaps = snapsResp.snapshots;
  const volumes = st.volumes || [];

  const trends = await Promise.all((volumes || []).map((v) =>
    api("trend", { volume: v.id, limit: 12 }).catch(() => ({ points: [] }))));
  volumes.forEach((v, i) => {
    const pts = trends[i].points;
    if (!pts.length) return;
    const rootLabel = pts[pts.length - 1].root || ("卷 " + v.id);
    root.appendChild(sparkPanel("趋势 · " + rootLabel,
      "卷 #" + v.id + (v.label ? " · " + v.label : "") + " · 最近 " + pts.length + " 次完整卷根快照",
      pts));
  });

  root.appendChild(dailyPanel(dailyResp.days || []));

  const p = panel("快照时间轴", fmtCount(snaps.length) + " 条记录");
  const rows = [];
  for (const s of snaps) {
    const statusCls = s.status === "complete" ? "ok" : s.status === "partial" ? "warn" : "danger";
    const ops = el("td", "num");
    if (s.status === "complete") {
      const a = el("a", null, "下钻");
      a.href = "#/explorer?snapshot=" + s.id;
      ops.appendChild(a);
      ops.appendChild(document.createTextNode(" · "));
      const c = el("a", null, "对比");
      c.href = "#/compare?to=" + s.id;
      ops.appendChild(c);
    }
    rows.push([
      td("#" + s.id),
      td(s.status, statusCls),
      td(s.kind),
      td(s.root, "path"),
      td(fmtDate(s.started_at_ms), "dim"),
      td(fmtDuration(s.duration_ms), "num"),
      td(fmtCount(s.files), "num"),
      td(fmtCount(s.dirs), "num"),
      td(fmtBytes(s.unique_alloc_bytes), "num"),
      td(fmtCount(s.errors), "num"),
      td(s.coverage_pct > 0 ? fmtPct(s.coverage_pct) : "—", "num"),
      ops,
    ]);
  }
  p.appendChild(dataTable([
    { label: "ID" }, { label: "状态" }, { label: "类型" }, { label: "根" },
    { label: "开始" }, { label: "耗时", num: true }, { label: "文件", num: true },
    { label: "目录", num: true }, { label: "唯一实占", num: true },
    { label: "错误", num: true }, { label: "覆盖率", num: true }, { label: "操作", num: true },
  ], rows));
  root.appendChild(p);
}

function dailyPanel(days) {
  const p = panel("日汇总", "每个本地日取最后一次完整卷根快照；基线是上一个有数据的日的最后一次快照，首日以当日首次快照为基线");
  if (!days.length) {
    p.appendChild(el("div", "empty", "还没有日汇总。运行 fat daily，或等待每日计划任务后生成。"));
    return p;
  }
  const rows = days.map((d) => {
    const rootCell = el("td", "path");
    rootCell.appendChild(document.createTextNode(d.root || ("卷 " + d.volume_id)));
    if (d.errors) rootCell.appendChild(el("span", "badge unknown", d.errors + " 错误"));
    if (d.coverage_pct > 0 && d.coverage_pct < 95) {
      rootCell.appendChild(el("span", "badge unknown", "覆盖 " + fmtPct(d.coverage_pct)));
    }
    const dirs = el("div", "daily-dirs");
    for (const t of (d.top_dirs || []).slice(0, 3)) {
      const line = el("div");
      line.appendChild(el("span", "dim", signedBytes(t.delta_bytes) + "  "));
      line.appendChild(document.createTextNode(t.path));
      dirs.appendChild(line);
    }
    if (!dirs.childNodes.length) dirs.appendChild(el("span", "dim", "—"));
    return [
      td(d.day),
      rootCell,
      td(signedBytes(d.delta_bytes), "num " + signClass(d.delta_bytes)),
      td(fmtBytes(d.growth_bytes), "num"),
      td(d.growth_bytes > 0 ? fmtPct(d.explained_pct) : "—",
        "num" + (d.growth_bytes > 0 && d.explained_pct < 70 ? " warn" : "")),
      tdNode(dirs, "path"),
    ];
  });
  p.appendChild(dataTable([
    { label: "日期" }, { label: "根" }, { label: "净增长", num: true },
    { label: "增长量", num: true }, { label: "解释率", num: true }, { label: "Top 目录" },
  ], rows));
  return p;
}

function alertsPanel(rep) {
  const list = rep.alerts || [];
  const fired = list.filter((a) => a.triggered).length;
  const p = panel("增长告警", list.length ? fired + " / " + list.length + " 个卷超阈值" : "");
  if (!list.length) {
    p.appendChild(el("div", "empty",
      "没有可评估的告警：需要至少一天的日汇总，且 config.yaml 的 alerts 阈值非零。"));
    return p;
  }
  const thLabel = (a) => {
    const parts = [];
    if (a.threshold_bytes > 0) parts.push(fmtBytes(a.threshold_bytes));
    if (a.threshold_pct > 0) parts.push(a.threshold_pct.toFixed(1) + "%");
    if (a.threshold_multiple > 0) parts.push(a.threshold_multiple.toFixed(1) + "×");
    return parts.join(" / ") || "—";
  };
  const baselineLabel = (a) => {
    if (!a.baseline_bytes || !(a.value_bytes > 0)) return "—";
    return fmtBytes(a.baseline_bytes) + " 中位 · " + (a.multiple || 0).toFixed(1) + "×";
  };
  const rows = list.map((a) => {
    let state;
    if (!a.triggered) state = el("span", "pos", "正常");
    else if (a.muted) state = el("span", "warn", "静默中");
    else state = el("span", "danger", "已触发");
    const when = a.muted ? "至 " + fmtDate(a.muted_until_ms)
      : (a.last_fired_at_ms ? fmtDate(a.last_fired_at_ms) : "—");
    return [
      td(a.root || ("卷 " + a.volume_id), "path"),
      td(a.day || "—", "dim"),
      tdNode(state),
      td(signedBytes(a.value_bytes), "num " + signClass(a.value_bytes)),
      td(fmtBytes(a.used_bytes), "num"),
      td(thLabel(a), "num"),
      td(baselineLabel(a), "num dim"),
      td(when, "dim"),
      td(fmtCount(a.hits), "num"),
    ];
  });
  p.appendChild(dataTable([
    { label: "根" }, { label: "日期" }, { label: "状态" }, { label: "实测净增长", num: true },
    { label: "已用", num: true }, { label: "阈值", num: true }, { label: "基准 / 倍数", num: true },
    { label: "最近提醒 / 静默至" }, { label: "命中", num: true },
  ], rows));
  p.appendChild(el("div", "notice",
    "比较对象是每日净增长（实占）。倍数阈值的基准是前 14 天正增长日的中位数，至少 3 个样本才评估。" +
    "静默只压制重复提醒，不会隐藏超阈值事实；服务端展示为只读预览，不写告警状态。"));
  return p;
}

function configPanel(cfg) {
  const p = panel("调度、保留与告警", "只读展示数据库同目录的 config.yaml");
  if (cfg.exists) {
    p.appendChild(el("div", "notice", "已加载 " + cfg.path));
  } else {
    p.appendChild(el("div", "notice",
      "未找到 " + cfg.path + "，显示内置默认值。运行 fat config init 生成配置文件。"));
  }
  const grid = el("div", "grid-2");

  const sched = cfg.schedule || {};
  const roots = sched.roots || [];
  const ps = el("div");
  ps.appendChild(el("h3", null, "计划任务"));
  ps.appendChild(dataTable(
    [{ label: "项目" }, { label: "值" }],
    [
      [td("扫描根", "dim"), td(roots.length ? roots.join("、") : "未配置（不会安装计划任务）",
        roots.length ? "path" : "warn")],
      [td("每日", "dim"), td(sched.daily_at || "—")],
      [td("每周", "dim"), td(sched.weekly_at || "—")],
      [td("电源", "dim"), td(sched.ac_only ? "仅交流电（用电池时等待）" : "不限电源",
        sched.ac_only ? "dim" : "warn")],
      [td("安装 / 查看", "dim"), td("fat schedule install · fat schedule status")],
    ]));
  grid.appendChild(ps);

  const ret = cfg.retention || {};
  const pr = el("div");
  pr.appendChild(el("h3", null, "保留（仅 FatTrace 自身历史）"));
  pr.appendChild(dataTable(
    [{ label: "项目" }, { label: "保留" }],
    [
      [td("每日快照", "dim"), td(ret.daily_snapshots_days + " 天")],
      [td("每周快照", "dim"), td(ret.weekly_snapshots_days + " 天")],
      [td("日快照文件", "dim"), td(ret.daily_file_days + " 天")],
      [td("周快照文件", "dim"), td(ret.weekly_file_weeks + " 周")],
      [td("每日汇总", "dim"), td(ret.daily_summary_days + " 天")],
      [td("文件事件", "dim"), td(ret.file_events_days + " 天")],
      [td("自动清理", "dim"), td(ret.auto_prune ? "开启（随维护任务）" : "关闭",
        ret.auto_prune ? "warn" : "dim")],
    ]));
  grid.appendChild(pr);

  const al = cfg.alerts || {};
  const pa = el("div");
  pa.appendChild(el("h3", null, "增长告警"));
  pa.appendChild(dataTable(
    [{ label: "项目" }, { label: "阈值" }],
    [
      [td("单日净增长", "dim"), td(al.growth_bytes > 0 ? fmtBytes(al.growth_bytes) : "关闭")],
      [td("占已用比例", "dim"), td(al.growth_pct > 0 ? al.growth_pct.toFixed(1) + "%" : "关闭")],
      [td("相对前 14 天正增长日中位数", "dim"),
        td(al.growth_multiple > 0 ? al.growth_multiple.toFixed(1) + "×" : "关闭")],
      [td("倍数起算下限", "dim"),
        td(al.growth_multiple > 0 ? fmtBytes(al.growth_multiple_floor_bytes) : "—")],
      [td("静默窗口", "dim"), td(al.silence_hours + " 小时")],
    ]));
  grid.appendChild(pa);

  p.appendChild(grid);
  return p;
}

async function viewRules(root) {
  const [data, cfg] = await Promise.all([api("rules"), api("config")]);
  const rules = data.rules || [];
  const p = panel("解释规则",
    fmtCount(rules.length) + " 条（内置 " + fmtCount(data.rules_builtin) +
    " · 用户 " + fmtCount(data.rules_user) + "）");
  p.appendChild(el("div", "notice",
    "规则只给增长贴标签，不执行删除或清理。用户规则放在数据库同目录的 rules.yaml，" +
    "同 ID 覆盖内置规则，重启 fat serve 后生效。"));
  if (data.user_path) {
    p.appendChild(el("div", "notice", "已加载用户规则：" + data.user_path));
  }
  const confLabel = { high: "高", medium: "中", low: "低" };
  const rows = rules.slice().sort((a, b) => {
    const ca = a.category_label || a.category, cb = b.category_label || b.category;
    return ca === cb ? a.id.localeCompare(b.id) : ca.localeCompare(cb);
  }).map((r) => {
    const patterns = el("div", "rule-patterns");
    for (const g of r.match.path_globs || []) patterns.appendChild(el("div", null, g));
    for (const g of r.match.name_globs || []) patterns.appendChild(el("div", null, "name: " + g));
    const nameCell = el("td", "path");
    nameCell.appendChild(el("div", null, r.name));
    nameCell.appendChild(el("div", "dim", r.id));
    return [
      td(r.category_label || r.category),
      nameCell,
      td(confLabel[r.confidence] || r.confidence, r.confidence === "low" ? "warn" : ""),
      td(r.source === "user" ? "用户" : "内置", r.source === "user" ? "pos" : "dim"),
      tdNode(patterns, "path"),
    ];
  });
  const wrap = el("div", "rules-table-wrap");
  wrap.appendChild(dataTable(
    [{ label: "类别" }, { label: "规则" }, { label: "置信度" }, { label: "来源" }, { label: "匹配" }],
    rows));
  p.appendChild(wrap);
  root.appendChild(p);

  root.appendChild(configPanel(cfg));

  const policy = panel("口径与边界");
  const box = el("div", "rules");
  const section = (title, text, list) => {
    box.appendChild(el("h3", null, title));
    if (text) box.appendChild(el("p", null, text));
    if (list) {
      const ul = el("ul");
      for (const item of list) ul.appendChild(el("li", null, item));
      box.appendChild(ul);
    }
  };
  section("只读边界", "本 UI 只展示数据，不提供删除、清理、执行建议或修改扫描配置的入口。所有处置动作由用户手动完成。");
  section("解释率口径", null, [
    "分母 = 新增 + 变大文件的实占增长；删除、缩小与 move 不计入。",
    "按文件累计，父子目录不会重复计数；解释率保留两位小数。",
    "最具体的匹配规则胜出（字面量更多者优先），用户规则与内置同分时用户优先。",
    "未解释增长按 Δ 降序保留前 50 个文件，界面显示总数。",
  ]);
  section("体积口径", null, [
    "分配大小（实占）为主口径，逻辑大小并存展示。",
    "硬链接按 FRN 去重；reparse point 不跟随。",
    "目录 totals 为递归口径；FRN 是同一快照内的稳定身份。",
  ]);
  section("变更分类优先级", "尺寸 > move > rename：文件同时变大并改名时只报 grown，但保留旧名/旧父用于展示。");
  section("目录归因", null, [
    "文件 move 在旧/新父链对冲移动前大小，增长留在目标目录。",
    "目录 move 只保留被移动子树的内部增量，纯移动不产生 Δ。",
    "TopDirs 隐藏与直接子目录 Δ 完全相同的祖先，只留最深所有者；treemap 按最近上榜祖先扣减。",
  ]);
  section("快照与基线", null, [
    "仅「卷根 + complete」更新基线 current_*。",
    "partial / failed 只写快照元数据与错误，不替换基线；深度受限的扫描也不替换。",
    "默认配对：最新 complete 卷根 + 同卷同根的上一个 complete。",
  ]);
  section("卷级校验", "|根 Δ − 卷 Δ| / max(|卷 Δ|, 1% × 已用) < 2% 记为通过；非卷根或缺卷用量时跳过，不算失败。");
  section("保留与告警", null, [
    "保留只作用于 FatTrace 自己的数据库历史，从不触碰被扫描的文件。",
    "每个卷根始终保留最新一次快照；手动快照与运行中的快照不会被清理。fat prune 默认 dry-run，仅 --apply 才删除。",
    "自动清理默认关闭，开启后随每周任务在生成周报之后执行。",
    "告警比较每日净增长；超阈值重复出现只提醒一次，静默窗口结束后才再次提醒。",
  ]);
  policy.appendChild(box);
  root.appendChild(policy);
}

function healthPanel(rep) {
  const p = panel("采集健康", "最近完整快照、失败次数与数据新鲜度");
  const rows = (rep.roots || []).map((h) => [
    td(h.root, "path"),
    td(h.level === "danger" ? "异常" : h.level === "warn" ? "注意" : "正常", h.level),
    td(h.last_complete ? "#" + h.last_complete.id + " · " + fmtDate(h.last_complete.started_at_ms) : "—"),
    td(h.last_failure ? "#" + h.last_failure.id + " " + h.last_failure.status +
      (h.last_failure_reason ? " · " + h.last_failure_reason : "") : "—"),
    td(h.consecutive_failures || 0, h.consecutive_failures ? "danger num" : "num"),
    td(h.reason || h.summary || "—"),
  ]);
  p.appendChild(dataTable(
    [{ label: "根" }, { label: "状态" }, { label: "最近完整" }, { label: "上次失败" }, { label: "连续失败", num: true }, { label: "说明" }],
    rows));
  return p;
}

async function viewStatus(root) {
  const [st, alertRep, healthRep] = await Promise.all([
    api("status"),
    api("alerts").catch((err) => ({ alerts: [], error: err.message })),
    api("health").catch((err) => ({ roots: [], level: "danger", error: err.message })),
  ]);
  if (healthRep.error) {
    const ph = panel("采集健康");
    ph.appendChild(el("div", "empty", "健康状态读取失败：" + healthRep.error));
    root.appendChild(ph);
  } else {
    root.appendChild(healthPanel(healthRep));
  }
  const grid = el("div", "status-grid");

  const pd = panel("数据库");
  const kv = el("dl", "kv");
  const addKV = (k, v) => { kv.appendChild(el("dt", null, k)); kv.appendChild(el("dd", null, v)); };
  addKV("路径", st.path);
  addKV("大小", fmtBytes(st.size_bytes + (st.wal_bytes || 0)) +
    (st.wal_bytes ? "（含 WAL " + fmtBytes(st.wal_bytes) + "）" : ""));
  addKV("Schema", "v" + st.schema_version);
  pd.appendChild(kv);
  grid.appendChild(pd);

  const pv = panel("卷", fmtCount((st.volumes || []).length) + " 个");
  pv.appendChild(dataTable(
    [{ label: "ID" }, { label: "序列号" }, { label: "文件系统" }, { label: "标签" }, { label: "最近出现", num: true }],
    (st.volumes || []).map((v) => [
      td(v.id),
      td("0x" + Number(v.serial).toString(16).toUpperCase().padStart(8, "0")),
      td(v.fs_type || "—"),
      td(v.label || "—"),
      td(fmtDate(v.last_seen_at_ms), "dim"),
    ])));
  grid.appendChild(pv);
  root.appendChild(grid);

  if (alertRep.error) {
    const pe = panel("增长告警");
    pe.appendChild(el("div", "empty", "告警评估失败：" + alertRep.error));
    root.appendChild(pe);
  } else {
    root.appendChild(alertsPanel(alertRep));
  }

  const pt = panel("表行数", fmtCount((st.tables || []).length) + " 张表");
  pt.appendChild(dataTable(
    [{ label: "表" }, { label: "行数", num: true }],
    (st.tables || []).map((t) => [td(t.name), td(fmtCount(t.rows), "num")])));
  root.appendChild(pt);

  const pr = panel("最近快照", "最新 5 条");
  pr.appendChild(dataTable(
    [{ label: "ID" }, { label: "状态" }, { label: "根" }, { label: "开始" }, { label: "文件", num: true }, { label: "唯一实占", num: true }],
    (st.recent_snapshots || []).map((s) => [
      td("#" + s.id), td(s.status, s.status === "complete" ? "ok" : "dim"),
      td(s.root, "path"), td(fmtDate(s.started_at_ms), "dim"),
      td(fmtCount(s.files), "num"), td(fmtBytes(s.unique_alloc_bytes), "num"),
    ])));
  pr.appendChild(el("div", "notice",
    "API 请求通过会话 token 鉴权，token 保存在本标签页 sessionStorage；刷新页面无需重新输入。"));
  root.appendChild(pr);
}

// ---------- router ----------

const views = {
  overview: viewOverview,
  explorer: viewExplorer,
  compare: viewCompare,
  timeline: viewTimeline,
  rules: viewRules,
  status: viewStatus,
};

function parseHash() {
  const raw = location.hash.replace(/^#\/?/, "");
  const i = raw.indexOf("?");
  const name = (i < 0 ? raw : raw.slice(0, i)) || "overview";
  const params = new URLSearchParams(i < 0 ? "" : raw.slice(i + 1));
  return { name, params };
}

function highlightNav(name) {
  for (const a of document.querySelectorAll("#nav a")) {
    a.classList.toggle("active", a.dataset.view === name);
  }
}

function errorBox(err) {
  const box = el("div", "error-box");
  box.appendChild(el("b", null, err.status === 401 ? "未授权" : "加载失败"));
  box.appendChild(el("div", null, err.message || String(err)));
  if (err.status === 401) {
    box.appendChild(el("div", "notice",
      "请使用 fat serve 启动时打印的带 #t=… 的 URL 打开页面；token 只对当前服务进程有效。"));
  }
  return box;
}

let renderSeq = 0;

async function checkHealthBanner(seq) {
  let rep;
  try {
    rep = await api("health");
  } catch {
    return;
  }
  if (seq !== renderSeq || !rep || rep.level === "ok") return;
  const bad = (rep.roots || []).filter((r) => r.level !== "ok");
  const detail = bad.map((r) => r.root + "：" + (r.reason || r.summary)).join("；");
  addBanner(rep.level === "danger" ? "danger" : "warn",
    rep.level === "danger" ? "数据采集异常" : "数据采集注意", detail);
}

async function render() {
  const seq = ++renderSeq;
  redraws.length = 0;
  clearBanners();
  hideTooltip();
  const view = document.getElementById("view");
  view.textContent = "";
  view.appendChild(el("div", "loading", "加载中…"));
  const { name, params } = parseHash();
  const handler = views[name] || views.overview;
  highlightNav(views[name] ? name : "overview");
  document.title = "FatTrace · " + (views[name] ? name : "overview");
  try {
    const frag = document.createDocumentFragment();
    await handler(frag, params);
    if (seq !== renderSeq) return; // a newer navigation won
    view.textContent = "";
    view.appendChild(frag);
    window.scrollTo(0, 0);
    checkHealthBanner(seq);
  } catch (err) {
    if (seq !== renderSeq) return;
    view.textContent = "";
    view.appendChild(errorBox(err));
  }
}

window.addEventListener("hashchange", render);
render();
