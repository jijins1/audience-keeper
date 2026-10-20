"use strict";

(() => {
  const $ = (s) => document.querySelector(s);
  const TYPE_LABEL = { text: "Texte libre", choice: "Choix", yesno: "Oui / Non", number: "Nombre", code: "Code" };
  const STATUS_LABEL = { proposed: "en attente", open: "ouverte", closed: "validée" };
  const GRADE_LABEL = { correct: "✓ correct", partial: "~ partiel", wrong: "✗ faux" };

  let ws = null;
  let backoff = 500;
  let tv = null;          // latest teacher view
  let viewQ = "";         // question pinned by the teacher ("" = follow the open one)
  let newType = "text";
  let suggestionsKey = "";

  // ---------- code answers ----------

  // The highlighter comes with the code editor bundle, fetched on the first
  // code question; until then code is shown as plain text.
  let editorState = "";
  function loadEditor() {
    if (window.AKEditor || editorState) return;
    editorState = "loading";
    const script = document.createElement("script");
    script.src = "/static/vendor/editor/ak-editor.js";
    script.onload = () => { editorState = "ready"; highlighted.clear(); if (tv) render(); };
    script.onerror = () => { editorState = "failed"; };
    document.head.append(script);
  }

  // Highlighting is cached per text: drafts change one student at a time.
  const highlighted = new Map();
  // The answer sits inside a method: drop the common indentation to read it.
  function dedent(code) {
    const lines = code.split("\n");
    const indents = lines.filter((l) => l.trim()).map((l) => l.match(/^[ \t]*/)[0].length);
    const n = indents.length ? Math.min(...indents) : 0;
    return n ? lines.map((l) => l.slice(Math.min(n, l.match(/^[ \t]*/)[0].length))).join("\n") : code;
  }

  function codeNodes(raw, q) {
    const text = dedent(raw);
    if (!window.AKEditor) { loadEditor(); return [text]; }
    const key = q.id + "\u0000" + text;
    let html = highlighted.get(key);
    if (!html) {
      // the template around the answer is enough to parse it; the provided
      // classes (up to 32 KB) would be parsed again on every keystroke
      const c = q.code || {};
      const before = (c.before || "").slice(-4000);
      const box = document.createElement("div");
      box.append(AKEditor.highlight(text, { language: c.language, before, after: c.after || "" }));
      html = box;
      if (highlighted.size > 400) highlighted.clear();
      highlighted.set(key, html);
    }
    return [...html.cloneNode(true).childNodes];
  }

  // Same as the server (code.go): layout and comments do not matter,
  // identifiers keep their case. Character classes mirror Go's unicode ones.
  const GO_SPACE = /[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]/;
  function codeKey(s) {
    let out = "", quote = "", esc = false, space = false, prev = "";
    const word = (c) => /[\p{L}\p{Nd}_$]/u.test(c);
    const rs = [...s];
    for (let i = 0; i < rs.length; i++) {
      const c = rs[i];
      if (!quote && c === "/" && (rs[i + 1] === "/" || rs[i + 1] === "*")) {
        if (rs[i + 1] === "/") { while (i < rs.length && rs[i] !== "\n") i++; }
        else { i += 2; while (i + 1 < rs.length && !(rs[i] === "*" && rs[i + 1] === "/")) i++; i = i + 1 < rs.length ? i + 1 : rs.length; }
        space = true;
        continue;
      }
      if (quote) {
        out += c;
        if (esc) esc = false; else if (c === "\\") esc = true; else if (c === quote) quote = "";
        prev = c;
        continue;
      }
      if (GO_SPACE.test(c)) { space = true; continue; }
      if (space && word(prev) && word(c)) out += " ";
      space = false;
      if (c === '"' || c === "'") quote = c;
      out += c;
      prev = c;
    }
    return out;
  }
  const textKey = (s) => s.toLowerCase().split(/\s+/).filter(Boolean).join(" ").replace(/[\p{P}\s]+$/u, "");
  const keyFor = (q) => (q.type === "code" ? codeKey : textKey);
  const firstLine = (code) => (code.split("\n").map((l) => l.trim()).find((l) => l && !/^[{}]$/.test(l)) || code).slice(0, 80);

  /** Small DOM builder: text is always set through textContent. */
  function h(tag, attrs, ...children) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === false || v == null) continue;
      if (k === "class") el.className = v;
      else if (k.startsWith("data-")) el.setAttribute(k, v);
      else if (k === "title") el.title = v;
      else el.setAttribute(k, v);
    }
    for (const c of children.flat()) {
      if (c == null || c === false) continue;
      el.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return el;
  }

  /**
   * Updates parent's children in place to match next, keeping existing
   * elements (and thus clicks, hover and scroll) alive across the ~4 Hz refresh.
   */
  function morph(parent, ...next) {
    const old = [...parent.childNodes];
    next.forEach((n, i) => {
      const o = old[i];
      if (!o) parent.append(n);
      else if (o.nodeType !== n.nodeType || o.nodeName !== n.nodeName) parent.replaceChild(n, o);
      else if (o.nodeType === Node.TEXT_NODE) { if (o.nodeValue !== n.nodeValue) o.nodeValue = n.nodeValue; }
      else { syncAttrs(o, n); morph(o, ...n.childNodes); }
    });
    for (let i = next.length; i < old.length; i++) old[i].remove();
  }

  // style goes through CSSOM: the CSP forbids setting the style attribute.
  function syncAttrs(o, n) {
    for (const a of [...o.attributes]) {
      if (n.hasAttribute(a.name)) continue;
      if (a.name === "style") o.style.cssText = "";
      else o.removeAttribute(a.name);
    }
    for (const a of n.attributes) {
      if (a.name === "style") { if (o.style.cssText !== n.style.cssText) o.style.cssText = n.style.cssText; }
      else if (o.getAttribute(a.name) !== a.value) o.setAttribute(a.name, a.value);
    }
  }

  function toast(msg) {
    const t = h("div", { class: "toast" }, msg);
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
      $("#dot").classList.add("on");
      if (viewQ) send({ t: "view", q: viewQ });
    };
    ws.onmessage = (ev) => {
      let m;
      try { m = JSON.parse(ev.data); } catch { return; }
      if (m.t === "teacher") { tv = m.d; render(); }
      else if (m.t === "error") toast(m.d.msg);
    };
    ws.onclose = (ev) => {
      $("#dot").classList.remove("on");
      // the handshake fails without a close code when the session cookie expired
      fetch("/admin/join-url").then((r) => { if (r.status === 401) location.reload(); }).catch(() => {});
      setTimeout(connect, backoff);
      backoff = Math.min(backoff * 2, 8000);
    };
  }

  function setView(q) {
    viewQ = q;
    send({ t: "view", q });
  }

  // ---------- helpers ----------

  const names = () => Object.fromEntries((tv.students || []).map((s) => [s.id, s.pseudo]));
  const secs = (ms) => (ms >= 60000 ? `${Math.floor(ms / 60000)} min ${Math.round((ms % 60000) / 1000)} s` : `${Math.round(ms / 1000)} s`);
  const time = (t) => new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });

  // ---------- render ----------

  function render() {
    $("#code").textContent = tv.code;
    const online = tv.students.filter((s) => s.connected).length;
    $("#count").textContent = `${online} connecté·e·s / ${tv.students.length}`;
    if (document.activeElement !== $("#aimode")) $("#aimode").value = tv.aiMode;
    $("#live").classList.toggle("hidden", !tv.live);
    if (tv.live) $("#live-label").textContent = `📽 Diffusé : ${tv.live.deck || "diaporama"} · slide ${tv.live.h + 1}${tv.live.v ? "." + tv.live.v : ""} `;
    renderQuestions();
    renderView();
    renderCards();
    renderSuggestions();
    renderAlerts();
  }

  function renderQuestions() {
    const list = $("#questions");
    const shown = tv.view ? tv.view.question.id : "";
    $("#no-questions").classList.toggle("hidden", tv.questions.length > 0);
    morph(list, ...[...tv.questions].reverse().map((q) => {
      const li = h("li", { class: q.id === shown ? "active" : "", "data-q": q.id },
        h("div", { class: "t" }, q.proposedBy === "claude" ? "🤖 " : "", q.title),
        h("div", { class: "muted" },
          h("span", { class: "status-" + q.status }, STATUS_LABEL[q.status]), " · ", TYPE_LABEL[q.type], " · ", `${q.answered} rép.`));
      if (q.status === "proposed") {
        li.append(h("div", { class: "acts" },
          h("button", { class: "small primary", "data-act": "open", "data-q": q.id }, "Ouvrir"),
          h("button", { class: "small danger", "data-act": "delete", "data-q": q.id }, "Supprimer")));
      }
      return li;
    }));
  }

  function renderView() {
    const d = tv.view;
    $("#empty").classList.toggle("hidden", !!d);
    $("#view").classList.toggle("hidden", !d);
    if (!d) return;
    const q = d.question;
    const s = d.stats;
    $("#v-title").textContent = q.title;
    $("#v-meta").textContent = `${TYPE_LABEL[q.type]} · ${STATUS_LABEL[q.status]}${q.proposedBy === "claude" ? " · proposée par Claude" : ""}`;
    renderProvidedCode(q);
    $("#v-close").classList.toggle("hidden", q.status !== "open");
    $("#v-open").classList.toggle("hidden", q.status !== "proposed");
    renderTimer();

    const kpis = [
      ["Réponses", `${s.answered} / ${s.participants}`, `${s.participationPct} %`],
      ["En train d'écrire", s.typing],
      ["Temps médian", s.answered ? secs(s.medianResponseMs) : "–"],
      ["Réussite", s.correctPct != null ? `${s.correctPct} %` : "–", s.correctPct != null ? `${s.correct} ✓ · ${s.partial} ~ · ${s.wrong} ✗` : ""],
      ["Ont collé", s.withPaste],
      ["Ajouts suspects", s.withBurst],
      ["Ont quitté la page", s.withBlur],
    ];
    if (s.numeric) kpis.push(["Min · médiane · max", `${s.numeric.min} · ${s.numeric.median} · ${s.numeric.max}`]);
    morph($("#kpis"), ...kpis.map(([label, value, sub]) =>
      h("div", { class: "kpi" }, h("span", { class: "muted" }, label), h("b", {}, value), sub ? h("span", { class: "muted" }, sub) : null)));

    let buckets = [];
    let markable = false;
    if (s.distribution) { buckets = s.distribution; markable = true; }
    else if (s.numeric) buckets = s.numeric.histogram;
    else if (s.textGroups) { buckets = s.textGroups; markable = true; }
    const maxCount = Math.max(1, ...buckets.map((b) => b.count));
    $("#correct-hint").textContent = markable ? "Clique sur une réponse pour la marquer comme correcte (ou la retirer)." : "";
    morph($("#bars"), ...buckets.map((b) => {
      const fill = h("div", { class: "fill" });
      fill.style.width = `${(100 * b.count) / maxCount}%`;
      const shown = q.type === "code" ? firstLine(b.label) : b.label;
      return h("div", { class: "bar" + (b.correct ? " correct" : ""), "data-label": markable ? b.label : null, title: markable ? (q.type === "code" ? b.label + "\n\nMarquer comme correct" : "Marquer comme correct") : "" },
        h("span", { class: "label" }, shown), h("div", { class: "track" }, fill), h("span", { class: "n" }, b.count));
    }));

    const words = s.words || [];
    const maxW = Math.max(1, ...words.map((w) => w.count));
    morph($("#words"), ...words.map((w) => {
      const el = h("span", { title: `${w.count}` }, w.word);
      el.style.fontSize = `${12 + (18 * w.count) / maxW}px`;
      el.style.opacity = String(0.55 + (0.45 * w.count) / maxW);
      return el;
    }));

    const nm = names();
    morph($("#groups"), ...(q.groups || []).map((g) =>
      h("div", { class: "group" },
        h("strong", {}, `🤖 ${g.label}`), ` · ${g.studentIds.length}`,
        g.comment ? h("div", { class: "muted" }, g.comment) : null,
        h("div", { class: "muted private" }, g.studentIds.map((id) => nm[id] || id).join(", ")))));
  }

  function renderProvidedCode(q) {
    const c = q.code;
    const box = $("#v-code");
    box.classList.toggle("hidden", !c);
    if (!c) return;
    const parts = [c.context, (c.before || "") + "    /* réponse */" + (c.after || "")].filter((x) => x && x.trim());
    const text = parts.join("\n\n");
    const pre = $("#v-code-text");
    if (pre.dataset.src !== text) {
      pre.dataset.src = text;
      pre.replaceChildren(window.AKEditor ? AKEditor.highlight(text, { language: c.language }) : text);
      if (!window.AKEditor) { pre.dataset.src = ""; loadEditor(); }
    }
  }

  function renderTimer() {
    const q = tv && tv.view && tv.view.question;
    if (!q || !q.openedAt) { $("#v-timer").textContent = ""; return; }
    const end = q.closedAt ? new Date(q.closedAt) : new Date();
    $("#v-timer").textContent = " · " + secs(end - new Date(q.openedAt));
  }

  function renderCards() {
    const d = tv.view;
    const answers = Object.fromEntries((d ? d.answers : []).map((a) => [a.studentId, a]));
    const flagged = new Set(tv.alerts.filter((a) => d && a.questionId === d.question.id).map((a) => a.studentId));
    const onlyFlagged = $("#only-flagged").checked;
    const cards = [];
    for (const st of tv.students) {
      const a = answers[st.id];
      const hasSignal = a && (a.pastes || a.bursts || a.blurs || a.hidden);
      if (onlyFlagged && !hasSignal && !flagged.has(st.id)) continue;
      cards.push(card(st, a, d && d.question, flagged.has(st.id)));
    }
    morph($("#cards"), ...cards);
  }

  function card(st, a, q, flagged) {
    const chips = [];
    let state = "rien";
    let text = "";
    if (a) {
      if (a.submitted) { state = a.autoSubmitted ? "envoyé (auto)" : "envoyé ✓"; text = a.value; }
      else if (a.draft) { state = "écrit…"; text = a.draft; }
      if (a.pastes) chips.push(h("span", { class: "chip bad", title: "Collages" }, `📋 ${a.pastes} · ${a.pastedChars} car.`));
      if (a.bursts) chips.push(h("span", { class: "chip warn", title: "Gros ajout sans frappe ni collage" }, `⚡ ${a.bursts}`));
      if (a.copies) chips.push(h("span", { class: "chip", title: "Copie / coupe" }, `✂ ${a.copies}`));
      if (a.blurs) chips.push(h("span", { class: "chip warn", title: "Sorties de page" }, `↗ ${a.blurs} · ${secs(a.blurMs)}`));
      if (a.hidden) chips.push(h("span", { class: "chip bad" }, "hors page"));
      if (a.aiGrade) chips.push(h("span", { class: "chip ai", title: "Évaluation proposée par Claude" }, `🤖 ${GRADE_LABEL[a.aiGrade]}`));
    }
    const outcome = q && a ? outcomeOf(q, a) : "";
    if (outcome) chips.push(h("span", { class: "chip " + (outcome === "correct" ? "ok" : outcome === "wrong" ? "bad" : "warn") }, GRADE_LABEL[outcome]));

    const grades = q && q.status !== "proposed"
      ? h("div", { class: "grades" },
        ...["correct", "partial", "wrong"].map((g) =>
          h("button", { class: "small", "data-act": "grade", "data-s": st.id, "data-g": g, "aria-pressed": String(a && a.teacherGrade === g), title: GRADE_LABEL[g] }, GRADE_LABEL[g].slice(0, 1))),
        h("span", { class: "spacer" }),
        h("button", { class: "small", "data-act": "message", "data-s": st.id, title: "Envoyer un message" }, "✉"))
      : h("div", { class: "grades" }, h("span", { class: "spacer" }), h("button", { class: "small", "data-act": "message", "data-s": st.id, title: "Envoyer un message" }, "✉"));

    return h("div", { class: "card" + (flagged ? " flag" : "") + (st.connected ? "" : " away") },
      h("div", { class: "who" }, h("span", { class: "status-dot" + (st.connected ? " on" : "") }), st.pseudo,
        h("span", { class: "spacer" }), h("span", { class: "muted" }, state)),
      h("div", { class: "text" + (q && q.type === "code" ? " code-text" : "") + (a && !a.submitted && a.draft ? " typing" : "") },
        q && q.type === "code" && text ? codeNodes(text, q) : text),
      chips.length ? h("div", { class: "row" }, chips) : null,
      a && a.aiNote ? h("div", { class: "note" }, `🤖 ${a.aiNote}`) : null,
      grades);
  }

  function outcomeOf(q, a) {
    if (a.teacherGrade) return a.teacherGrade;
    if (!a.submitted || !q.correct || !q.correct.length) return "";
    const norm = keyFor(q);
    return q.correct.some((c) => norm(c) === norm(a.value)) ? "correct" : "wrong";
  }

  function renderSuggestions() {
    const key = tv.suggestions.map((s) => s.id).join(",");
    $("#no-suggestions").classList.toggle("hidden", tv.suggestions.length > 0);
    if (key === suggestionsKey) return; // keep edits in progress
    suggestionsKey = key;
    const nm = names();
    $("#suggestions").replaceChildren(...tv.suggestions.map((s) => {
      const ta = h("textarea", { rows: "3", maxlength: "500", "data-edit": s.id });
      ta.value = s.text;
      return h("div", { class: "suggestion" },
        h("div", {}, h("strong", {}, `🤖 → ${nm[s.studentId] || s.studentId}`), h("span", { class: "muted" }, " · " + time(s.time))),
        ta,
        h("div", { class: "row" },
          h("button", { class: "small primary", "data-act": "approve", "data-id": s.id }, "Envoyer"),
          h("button", { class: "small", "data-act": "dismiss", "data-id": s.id }, "Ignorer")));
    }));
  }

  function renderAlerts() {
    const nm = names();
    const icon = { paste: "📋", burst: "⚡", hidden: "↗", inactive: "💤", stalled: "⏸", ai: "🤖" };
    morph($("#alerts"), ...[...tv.alerts].reverse().map((a) =>
      h("li", {}, h("span", { class: "muted" }, time(a.time)), " ", icon[a.kind] || "•", " ",
        h("strong", {}, nm[a.studentId] || a.studentId), " ", a.message, a.questionId ? h("span", { class: "muted" }, ` (${a.questionId})`) : null)));
  }

  // ---------- actions ----------

  function create(open) {
    const n = parseInt($("#new-choices").value, 10) || 4;
    const msg = { t: "create", title: $("#new-title").value, type: newType, choices: newType === "choice" ? n : 0, open };
    if (newType === "code") msg.code = { language: "java", context: $("#new-context").value, template: $("#new-template").value };
    send(msg);
    $("#new-title").value = "";
    if (open) setView("");
  }

  $("#types").addEventListener("click", (e) => {
    const b = e.target.closest("button[data-type]");
    if (!b) return;
    newType = b.dataset.type;
    for (const x of $("#types").children) x.setAttribute("aria-pressed", String(x === b));
    $("#choices-row").classList.toggle("hidden", newType !== "choice");
    $("#code-row").classList.toggle("hidden", newType !== "code");
  });
  $("#create-open").addEventListener("click", () => create(true));
  $("#create-only").addEventListener("click", () => create(false));
  $("#new-title").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) create(true);
  });

  $("#questions").addEventListener("click", (e) => {
    const act = e.target.closest("button[data-act]");
    if (act) {
      e.stopPropagation();
      if (act.dataset.act === "open") { send({ t: "open", q: act.dataset.q }); setView(""); }
      if (act.dataset.act === "delete" && confirm("Supprimer cette question ?")) send({ t: "delete", q: act.dataset.q });
      return;
    }
    const li = e.target.closest("li[data-q]");
    if (li) setView(li.dataset.q);
  });

  $("#v-close").addEventListener("click", () => tv.view && send({ t: "close", q: tv.view.question.id }));
  $("#v-open").addEventListener("click", () => { if (tv.view) { send({ t: "open", q: tv.view.question.id }); setView(""); } });

  $("#bars").addEventListener("click", (e) => {
    const bar = e.target.closest(".bar[data-label]");
    if (!bar || !tv.view) return;
    const q = tv.view.question;
    const label = bar.dataset.label;
    const norm = keyFor(q);
    const current = q.correct || [];
    const isSet = current.some((v) => norm(v) === norm(label));
    const values = isSet ? current.filter((v) => norm(v) !== norm(label)) : [...current, label];
    send({ t: "correct", q: q.id, values });
  });

  $("#cards").addEventListener("click", (e) => {
    const b = e.target.closest("button[data-act]");
    if (!b) return;
    if (b.dataset.act === "grade" && tv.view) {
      const already = b.getAttribute("aria-pressed") === "true";
      send({ t: "grade", q: tv.view.question.id, s: b.dataset.s, grade: already ? "" : b.dataset.g });
    }
    if (b.dataset.act === "message") {
      const text = prompt("Message à envoyer à cet étudiant :");
      if (text && text.trim()) send({ t: "message", s: b.dataset.s, text });
    }
  });
  $("#only-flagged").addEventListener("change", () => tv && renderCards());

  $("#suggestions").addEventListener("click", (e) => {
    const b = e.target.closest("button[data-act]");
    if (!b) return;
    const id = b.dataset.id;
    if (b.dataset.act === "approve") {
      const ta = document.querySelector(`textarea[data-edit="${CSS.escape(id)}"]`);
      send({ t: "approve", id, text: ta ? ta.value : "" });
    } else send({ t: "dismiss", id });
    suggestionsKey = ""; // force refresh on next frame
  });

  $("#live-off").addEventListener("click", () => send({ t: "slide-off" }));
  $("#aimode").addEventListener("change", (e) => send({ t: "aimode", mode: e.target.value }));
  $("#reset").addEventListener("click", () => {
    if (confirm("Démarrer une nouvelle séance ? Toutes les réponses seront effacées (pense à l'export CSV) et les étudiants devront rejoindre avec le nouveau code.")) {
      send({ t: "reset" });
      setView("");
    }
  });
  $("#projection").addEventListener("click", (e) => {
    const on = document.body.classList.toggle("projection");
    e.currentTarget.setAttribute("aria-pressed", String(on));
  });

  async function openQR() {
    try {
      const res = await fetch("/admin/join-url");
      const data = await res.json();
      $("#join-url").textContent = data.url;
      $("#qr-code").textContent = data.code;
      $("#qr-img").src = "/admin/qr.png?ts=" + Date.now();
      $("#qr").classList.remove("hidden");
    } catch {
      toast("Impossible de charger le QR code");
    }
  }
  $("#qr-btn").addEventListener("click", openQR);
  $("#qr-close").addEventListener("click", () => $("#qr").classList.add("hidden"));
  $("#qr").addEventListener("click", (e) => { if (e.target === $("#qr")) $("#qr").classList.add("hidden"); });

  setInterval(renderTimer, 1000);
  connect();
})();
