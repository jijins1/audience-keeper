"use strict";

/*
 * Presenter view (laptop, second screen) and remote control (phone).
 *
 * It never talks to the deck directly: it sends commands to the server, which
 * relays them to the deck window on the projector. A phone on 4G works as well
 * as a laptop on the classroom wifi.
 */
(() => {
  const $ = (s) => document.querySelector(s);
  const TIMER_KEY = "ak_presenter_start";
  const STATUS_LABEL = { proposed: "en attente", open: "ouverte", closed: "validée" };

  let ws = null;
  let backoff = 500;
  let tv = null;        // teacher frame: questions and stats
  let pf = null;        // presenter frame: live slide, notes, next slide
  let viewed = "";
  let current = null;
  let next = null;

  const store = {
    get(k) { try { return localStorage.getItem(k); } catch { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch { /* private mode */ } },
  };

  function toast(msg) {
    const t = document.createElement("div");
    t.className = "toast";
    t.textContent = msg;
    document.body.append(t);
    setTimeout(() => t.remove(), 3500);
  }

  function send(msg) {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
    else toast("Connexion perdue, nouvelle tentative…");
  }

  function connect() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}/admin/ws`);
    ws.onopen = () => {
      backoff = 500;
      viewed = "";
      $("#dot").classList.add("on");
      send({ t: "presenter" });
    };
    ws.onmessage = (ev) => {
      let m;
      try { m = JSON.parse(ev.data); } catch { return; }
      if (m.t === "presenter") { pf = m.d; renderSlides(); renderQuiz(); }
      else if (m.t === "teacher") { tv = m.d; renderQuiz(); }
      else if (m.t === "error") toast(m.d.msg);
    };
    ws.onclose = () => {
      $("#dot").classList.remove("on");
      fetch("/admin/join-url").then((r) => { if (r.status === 401) location.href = "/admin?next=/admin/presenter"; }).catch(() => {});
      setTimeout(connect, backoff);
      backoff = Math.min(backoff * 2, 8000);
    };
  }

  // ---------- slides and notes ----------

  function renderSlides() {
    const live = pf && pf.live;
    $("#no-deck").classList.toggle("hidden", !!live);
    if (!live) {
      $("#deck-title").textContent = "Aucun diaporama";
      $("#position").textContent = "";
      $("#notes").replaceChildren();
      current && current.render("", -1, []);
      next && next.render("", -1, []);
      return;
    }
    const s = pf.slide;
    const i = pf.info;
    $("#deck-title").textContent = s.deck || "Diaporama";
    const frag = i.fragments ? ` · étape ${s.fragment + 2}/${i.fragments + 1}` : "";
    $("#position").textContent = `${i.number} / ${i.total}${frag}`;
    current = current || AKSlideView.create($("#current"));
    next = next || AKSlideView.create($("#next"));
    current.render(s.html, s.fragment, s.themes);
    next.render(i.nextHtml, -1, s.themes);
    $("#next").classList.toggle("hidden", !i.nextHtml);
    $("#next-end").classList.toggle("hidden", !!i.nextHtml);
    $("#notes").replaceChildren(AKSlideView.sanitizeFragment(i.notes || ""));
  }

  // ---------- question ----------

  /** The question to control: the current slide's, else the one open from the dashboard. */
  function activeQuestion() {
    if (!tv) return null;
    const key = pf && pf.live && pf.info.questionKey;
    if (key) return tv.questions.find((q) => q.key === key) || { pending: true };
    return tv.questions.find((q) => q.id === tv.currentId) || null;
  }

  function renderQuiz() {
    const q = activeQuestion();
    $("#quiz").classList.toggle("hidden", !q);
    if (!q) return;
    if (q.pending) {
      $("#q-title").textContent = "Question de cette slide";
      $("#q-status").textContent = "ouverture…";
      $("#q-count").textContent = "";
      $("#q-bars").replaceChildren();
      $("#q-open").classList.add("hidden");
      $("#q-close").classList.add("hidden");
      return;
    }
    if (viewed !== q.id) {
      viewed = q.id;
      send({ t: "view", q: q.id });
    }
    $("#q-title").textContent = q.title;
    $("#q-status").textContent = STATUS_LABEL[q.status] || q.status;
    $("#q-open").classList.toggle("hidden", q.status !== "proposed");
    $("#q-close").classList.toggle("hidden", q.status !== "open");
    const d = tv.view && tv.view.question.id === q.id ? tv.view : null;
    if (!d) return;
    const s = d.stats;
    const count = $("#q-count");
    count.replaceChildren();
    const b = document.createElement("b");
    b.textContent = `${s.answered} / ${s.participants}`;
    count.append(b, " réponses");
    if (s.typing) count.append(` · ${s.typing} écrivent`);
    if (s.correctPct != null) count.append(` · ${s.correctPct} % de réussite`);
    renderBars(s, q.type);
  }

  // A code answer is long: its first meaningful line stands for it.
  const firstLine = (code) => (code.split("\n").map((l) => l.trim()).find((l) => l && !/^[{}]$/.test(l)) || code).slice(0, 80);

  function renderBars(s, type) {
    let buckets = s.distribution || (s.numeric && s.numeric.histogram) || (s.textGroups || []).slice(0, 5);
    if (type === "code") buckets = buckets.map((b) => ({ ...b, label: firstLine(b.label) }));
    const max = Math.max(1, ...buckets.map((x) => x.count));
    $("#q-bars").replaceChildren(...buckets.map((x) => {
      const row = document.createElement("div");
      row.className = "bar" + (x.correct ? " correct" : "");
      const label = document.createElement("span");
      label.className = "label";
      label.textContent = x.label;
      const track = document.createElement("div");
      track.className = "track";
      const fill = document.createElement("div");
      fill.className = "fill";
      fill.style.width = `${(100 * x.count) / max}%`;
      track.append(fill);
      const n = document.createElement("span");
      n.className = "n";
      n.textContent = x.count;
      row.append(label, track, n);
      return row;
    }));
  }

  // ---------- controls ----------

  const nav = (action) => send({ t: "nav", action });

  function closeQuestion() {
    const q = activeQuestion();
    if (q && !q.pending && q.status === "open") send({ t: "close", q: q.id });
  }

  $("#nav-next").addEventListener("click", () => nav("next"));
  $("#nav-prev").addEventListener("click", () => nav("prev"));
  $("#nav-pause").addEventListener("click", () => nav("pause"));
  $("#q-close").addEventListener("click", closeQuestion);
  $("#q-open").addEventListener("click", () => { const q = activeQuestion(); if (q && !q.pending) send({ t: "open", q: q.id }); });

  // Keyboard, including presentation clickers (they send PageUp / PageDown).
  document.addEventListener("keydown", (e) => {
    if (e.target.closest("input, textarea")) return;
    const k = e.key;
    if (["ArrowRight", "ArrowDown", "PageDown", " ", "n"].includes(k)) { e.preventDefault(); nav("next"); }
    else if (["ArrowLeft", "ArrowUp", "PageUp", "p"].includes(k)) { e.preventDefault(); nav("prev"); }
    else if (k === "Home") nav("first");
    else if (k === "End") nav("last");
    else if (k === "b" || k === ".") nav("pause");
    else if (k === "v") closeQuestion();
    else if (k === "t") resetTimer();
  });

  // ---------- timer and clock ----------

  let start = Number(store.get(TIMER_KEY)) || Date.now();
  store.set(TIMER_KEY, String(start));
  function resetTimer() { start = Date.now(); store.set(TIMER_KEY, String(start)); tick(); }
  const pad = (n) => String(n).padStart(2, "0");
  function tick() {
    const s = Math.floor((Date.now() - start) / 1000);
    const h = Math.floor(s / 3600);
    $("#timer").textContent = (h ? `${h}:` : "") + `${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`;
    $("#clock").textContent = new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  $("#timer-reset").addEventListener("click", resetTimer);
  setInterval(tick, 1000);
  tick();

  // ---------- phone pairing ----------

  function showPair() {
    $("#pair-img").src = "/admin/pair.png?ts=" + Date.now();
    $("#pair-overlay").classList.remove("hidden");
  }
  $("#pair").addEventListener("click", showPair);
  $("#pair-new").addEventListener("click", showPair);
  $("#pair-close").addEventListener("click", () => $("#pair-overlay").classList.add("hidden"));

  // Keep a phone screen awake while it is used as a remote.
  async function keepAwake() {
    try {
      if ("wakeLock" in navigator && document.visibilityState === "visible") await navigator.wakeLock.request("screen");
    } catch { /* not supported or refused: the remote still works */ }
  }
  document.addEventListener("visibilitychange", keepAwake);
  keepAwake();

  connect();
})();
