"use strict";

(() => {
  const $ = (s) => document.querySelector(s);
  const DRAFT_THROTTLE_MS = 250;
  const PASTE_EXCERPT = 500;
  const TOKEN_KEY = "ak_token";

  const store = {
    get(k) { try { return localStorage.getItem(k); } catch { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch { /* private mode */ } },
    del(k) { try { localStorage.removeItem(k); } catch { /* private mode */ } },
  };

  let token = store.get(TOKEN_KEY);
  let ws = null;
  let backoff = 500;
  let reconnectTimer = null;
  let currentQ = null;   // id of the question whose input is rendered
  let state = null;
  let draftTimer = null;
  let lastDraft = "";
  let selected = "";     // for choice questions
  const seenMessages = new Set();
  let messagesLoaded = false; // no flash animation for messages present at load

  // ---------- views ----------

  function show(id) {
    for (const s of ["join", "wait", "question"]) $("#" + s).classList.toggle("hidden", s !== id);
  }

  function toast(msg) {
    const t = document.createElement("div");
    t.className = "toast";
    t.textContent = msg;
    document.body.append(t);
    setTimeout(() => t.remove(), 3500);
  }

  function showJoin(error) {
    show("join");
    renderSlide(null);
    $("#me").classList.add("hidden");
    const params = new URLSearchParams(location.search);
    if (params.get("code") && !$("#code").value) $("#code").value = params.get("code");
    const err = $("#join-error");
    err.textContent = error || "";
    err.classList.toggle("hidden", !error);
  }

  // ---------- network ----------

  function send(msg) {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
  }

  function connect() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}/ws`);
    ws.onopen = () => {
      backoff = 500;
      send({ t: "hello", token });
      $("#dot").classList.add("on");
    };
    ws.onmessage = (ev) => {
      let m;
      try { m = JSON.parse(ev.data); } catch { return; }
      if (m.t === "state") render(m.d);
      else if (m.t === "ended") end();
      else if (m.t === "error") toast(m.d.msg);
    };
    ws.onclose = () => {
      $("#dot").classList.remove("on");
      if (!token) return;
      clearTimeout(reconnectTimer);
      reconnectTimer = setTimeout(connect, backoff);
      backoff = Math.min(backoff * 2, 8000);
    };
  }

  function end() {
    clearTimeout(reconnectTimer);
    token = null;
    store.del(TOKEN_KEY);
    currentQ = null;
    if (ws) ws.close();
    showJoin("La séance est terminée ou a été réinitialisée. Tu peux la rejoindre à nouveau.");
  }

  async function join(ev) {
    ev.preventDefault();
    const button = $("#join-form button");
    if (button.disabled) return;
    button.disabled = true;
    try { await doJoin(); } finally { button.disabled = false; }
  }

  async function doJoin() {
    const body = { code: $("#code").value.trim(), pseudo: $("#pseudo").value.trim() };
    try {
      const res = await fetch("/api/join", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      const data = await res.json();
      if (!res.ok) { showJoin(data.error || "Impossible de rejoindre"); return; }
      token = data.token;
      store.set(TOKEN_KEY, token);
      connect();
    } catch {
      showJoin("Serveur injoignable, vérifie ta connexion.");
    }
  }

  // ---------- live slides ----------

  let slideView = null;

  function renderSlide(sl) {
    const box = $("#slide-box");
    box.classList.toggle("hidden", !sl);
    document.body.classList.toggle("live", !!sl);
    if (!sl) return;
    slideView = slideView || AKSlideView.create(box);
    slideView.render(sl.html, sl.fragment, sl.themes);
  }

  // ---------- rendering ----------

  function render(s) {
    state = s;
    $("#me").classList.remove("hidden");
    $("#me-name").textContent = s.pseudo;
    renderMessages(s.messages);
    renderSlide(s.slide);
    if (!s.question) {
      currentQ = null;
      show(s.slide ? "" : "wait"); // the live slide is enough while there is no question
      return;
    }
    show("question");
    $("#q-title").classList.toggle("hidden", !!s.slide); // already on the mirrored slide
    if (s.question.id !== currentQ) {
      currentQ = s.question.id;
      selected = "";
      lastDraft = s.draft || "";
      $("#q-title").textContent = s.question.title;
      buildInput(s.question, s.draft || "");
      // already away when the question opened: report it for this question too
      if (document.hidden) signal("blur");
    }
    const done = s.submitted;
    $("#submit").classList.toggle("hidden", done);
    const status = $("#q-status");
    status.className = done ? "sent" : "muted";
    status.textContent = done ? "Réponse envoyée ✓  " + (s.value || "") : "";
    for (const el of $("#q-input").querySelectorAll("textarea, input, button")) el.disabled = done;
  }

  function buildInput(q, draft) {
    const box = $("#q-input");
    box.replaceChildren();
    if (q.type === "choice" || q.type === "yesno") {
      const grid = document.createElement("div");
      grid.className = "choices";
      for (const c of q.choices) {
        const b = document.createElement("button");
        b.type = "button";
        b.textContent = c;
        b.setAttribute("aria-pressed", String(c === draft));
        b.addEventListener("click", () => {
          selected = c;
          for (const x of grid.children) x.setAttribute("aria-pressed", String(x === b));
          send({ t: "draft", q: currentQ, text: c });
        });
        grid.append(b);
      }
      if (draft) selected = draft;
      box.append(grid);
      return;
    }
    const field = document.createElement(q.type === "number" ? "input" : "textarea");
    if (q.type === "number") {
      field.inputMode = "decimal";
      field.placeholder = "Un nombre";
    } else {
      field.placeholder = "Ta réponse…";
      field.maxLength = 5000;
    }
    field.id = "answer";
    field.value = draft;
    field.autocomplete = "off";
    field.spellcheck = false;
    wireSignals(field);
    box.append(field);
    field.focus();
  }

  function wireSignals(field) {
    // Throttle, not debounce: continuous typing must still reach the server in
    // small steps, otherwise it looks like a sudden insertion (burst).
    field.addEventListener("input", () => {
      if (draftTimer) return;
      draftTimer = setTimeout(() => { draftTimer = null; flushDraft(); }, DRAFT_THROTTLE_MS);
    });
    field.addEventListener("paste", (e) => {
      const text = (e.clipboardData && e.clipboardData.getData("text")) || "";
      send({ t: "event", q: currentQ, kind: "paste", chars: text.length, text: text.slice(0, PASTE_EXCERPT) });
    });
    for (const kind of ["copy", "cut"]) {
      field.addEventListener(kind, () => {
        const n = Math.max(0, (field.selectionEnd || 0) - (field.selectionStart || 0));
        send({ t: "event", q: currentQ, kind, chars: n });
      });
    }
  }

  function flushDraft() {
    const field = $("#answer");
    if (!field || !currentQ) return;
    if (field.value === lastDraft) return;
    lastDraft = field.value;
    send({ t: "draft", q: currentQ, text: field.value });
  }

  function submit() {
    if (!state || !state.question) return;
    const q = state.question;
    let value;
    if (q.type === "choice" || q.type === "yesno") value = selected;
    else { flushDraft(); value = $("#answer").value; }
    if (!value || !value.trim()) { toast("Réponse vide"); return; }
    send({ t: "submit", q: q.id, value });
  }

  function renderMessages(list) {
    const box = $("#messages");
    box.replaceChildren();
    for (const m of [...list].reverse()) {
      const el = document.createElement("div");
      el.className = "panel message";
      if (!seenMessages.has(m.id)) {
        if (messagesLoaded) el.classList.add("fresh");
        seenMessages.add(m.id);
      }
      const from = document.createElement("div");
      from.className = "from";
      from.textContent = "💬 Message · " + new Date(m.time).toLocaleTimeString();
      const text = document.createElement("div");
      text.textContent = m.text;
      el.append(from, text);
      box.append(el);
    }
    messagesLoaded = true;
  }

  // ---------- page signals ----------

  function signal(kind) {
    if (currentQ && state && !state.submitted) send({ t: "event", q: currentQ, kind });
  }
  document.addEventListener("visibilitychange", () => signal(document.hidden ? "blur" : "focus"));
  window.addEventListener("blur", () => signal("blur"));
  window.addEventListener("focus", () => signal("focus"));

  // ---------- boot ----------

  $("#join-form").addEventListener("submit", join);
  $("#submit").addEventListener("click", submit);
  if (token) connect(); else showJoin();
})();
