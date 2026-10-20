/*
 * Audience Keeper plugin for reveal.js.
 *
 *   <section data-ak-question="choice" data-ak-choices="4"> ... </section>
 *     Arriving on the slide opens the question for students, leaving it
 *     validates it; results then appear in the slide. Types: text (default),
 *     choice, yesno, number. Title: data-ak-title, else the slide's first heading.
 *     Options: data-ak-id (stable key, else slide position), data-ak-keep-open
 *     (do not validate when leaving), data-ak-live-results (show bars while open).
 *
 *   <section data-ak-join> ... </section>   shows the QR code and join code.
 *
 *   Key V validates the current question.
 *
 * The current slide (and only it) is mirrored live on student screens, without
 * speaker notes, scripts or this plugin's panels. Students cannot navigate.
 * Opt out with Reveal.initialize({ audienceKeeper: { mirror: false } }) or
 * per slide with data-ak-private.
 *
 * Usage: Reveal.initialize({ plugins: [ AudienceKeeper ] });
 * The deck must be served by Audience Keeper (/deck/...) with the teacher logged in.
 */
(function (root) {
  "use strict";

  const TYPES = ["text", "choice", "yesno", "number"];
  // Paging quickly through the deck must not open (and close) every question
  // on the way: a question opens only once its slide stays on screen.
  const OPEN_DELAY_MS = 600;
  let openTimer = null;
  let deck = null;
  let ws = null;
  let backoff = 500;
  let tv = null;            // latest teacher frame
  let current = null;       // { slide, key } for the question slide on screen
  let viewed = "";          // question id we asked the server to detail
  let badge = null;

  function h(tag, cls, ...children) {
    const el = document.createElement(tag);
    if (cls) el.className = cls;
    for (const c of children.flat()) {
      if (c == null || c === false) continue;
      el.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return el;
  }

  function send(msg) {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
  }

  // ---------- connection ----------

  function connect() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}/admin/ws`);
    ws.onopen = () => {
      backoff = 500;
      setBadge(true);
      viewed = "";
      lastMirror = "";
      if (deck) { onSlide(deck.getCurrentSlide(), null); mirrorSlide(); } // resync after a reconnect
    };
    ws.onmessage = (ev) => {
      let m;
      try { m = JSON.parse(ev.data); } catch { return; }
      if (m.t === "teacher") { tv = m.d; refresh(); }
    };
    ws.onclose = () => {
      setBadge(false);
      setTimeout(connect, backoff);
      backoff = Math.min(backoff * 2, 8000);
    };
  }

  function setBadge(ok) {
    if (!badge) {
      badge = h("a", "ak-badge");
      badge.href = "/admin";
      badge.target = "_blank";
      document.body.append(badge);
    }
    badge.classList.toggle("ok", ok);
    badge.textContent = ok ? "● Audience Keeper" : "● Audience Keeper : non connecté (se connecter)";
  }

  // ---------- slides ----------

  const isQuestion = (s) => s && s.hasAttribute("data-ak-question");

  function keyOf(slide) {
    if (slide.dataset.akId) return slide.dataset.akId;
    if (slide.id) return "slide:" + slide.id;
    const i = deck.getIndices(slide);
    return `slide:${i.h}.${i.v || 0}`;
  }

  function titleOf(slide) {
    if (slide.dataset.akTitle) return slide.dataset.akTitle;
    const heading = slide.querySelector("h1, h2, h3");
    return heading ? heading.textContent.trim().slice(0, 300) : "";
  }

  function byKey(key) {
    return tv ? tv.questions.find((q) => q.key === key) : null;
  }

  function onSlide(slide, previous) {
    if (isQuestion(previous) && !previous.hasAttribute("data-ak-keep-open")) {
      const q = byKey(keyOf(previous));
      if (q && q.status === "open") send({ t: "close", q: q.id });
    }
    current = null;
    if (!slide) return;
    if (slide.hasAttribute("data-ak-join")) renderJoin(slide);
    if (!isQuestion(slide)) return;

    const type = TYPES.includes(slide.dataset.akQuestion) ? slide.dataset.akQuestion : "text";
    const here = { slide, key: keyOf(slide) };
    current = here;
    clearTimeout(openTimer);
    openTimer = setTimeout(() => {
      if (current !== here) return;
      send({ t: "create", key: here.key, title: titleOf(slide), type, choices: parseInt(slide.dataset.akChoices, 10) || 0, open: true });
    }, byKey(here.key) ? 0 : OPEN_DELAY_MS);
    refresh();
  }

  // ---------- mirroring ----------

  const MAX_HTML = 250 * 1024;
  let mirror = true;
  let lastMirror = "";

  function absolutise(el, attr) {
    const v = el.getAttribute(attr);
    if (!v || v.startsWith("data:") || v.startsWith("#")) return;
    try {
      const u = new URL(v, location.href);
      el.setAttribute(attr, u.origin === location.origin ? u.pathname + u.search + u.hash : u.href);
    } catch { /* leave as is */ }
  }

  function snapshot(slide) {
    const clone = slide.cloneNode(true);
    clone.querySelectorAll("aside.notes, script, .ak-panel, .ak-qr, .ak-url, meta, base, link").forEach((n) => n.remove());
    for (const el of [clone, ...clone.querySelectorAll("*")]) {
      for (const a of [...el.attributes]) {
        if (a.name.startsWith("on") || a.name === "data-notes") el.removeAttribute(a.name);
      }
      for (const attr of ["src", "href", "poster", "data-src", "data-background-image", "data-background-video", "data-background-iframe"]) {
        if (el.hasAttribute(attr)) absolutise(el, attr);
      }
    }
    // a vertical stack's parent is not needed: students get the visible slide only
    clone.classList.remove("present", "past", "future", "stack");
    clone.removeAttribute("hidden");
    clone.removeAttribute("aria-hidden");
    clone.removeAttribute("style");
    return clone.outerHTML;
  }

  function themes() {
    return [...document.querySelectorAll('link[rel="stylesheet"]')]
      .map((l) => l.getAttribute("href"))
      .filter((href) => href && !href.endsWith("reveal-ak.css") && !href.endsWith("reset.css")) // reset.css is global
      .map((href) => { try { const u = new URL(href, location.href); return u.origin === location.origin ? u.pathname : u.href; } catch { return ""; } })
      .filter(Boolean);
  }

  function mirrorSlide() {
    if (!mirror || !deck) return;
    const slide = deck.getCurrentSlide();
    if (!slide) return;
    const i = deck.getIndices();
    let html = slide.hasAttribute("data-ak-private")
      ? '<section><h2>⏸</h2></section>'
      : snapshot(slide);
    if (new TextEncoder().encode(html).length > MAX_HTML) html = "<section><p>(slide trop lourde pour être diffusée)</p></section>";
    const msg = { t: "slide", deck: document.title.slice(0, 200), html, h: i.h, v: i.v || 0, fragment: i.f == null ? -1 : i.f, themes: themes().slice(0, 8) };
    const key = JSON.stringify(msg);
    if (key === lastMirror) return;
    lastMirror = key;
    send(msg);
  }

  function validateCurrent() {
    const q = current && byKey(current.key);
    if (q && q.status === "open") send({ t: "close", q: q.id });
  }

  function refresh() {
    if (!current) return;
    const q = byKey(current.key);
    if (q && viewed !== q.id) {
      viewed = q.id;
      send({ t: "view", q: q.id });
    }
    const detail = q && tv.view && tv.view.question.id === q.id ? tv.view : null;
    renderPanel(current.slide, q, detail);
  }

  // ---------- rendering ----------

  function panelOf(slide) {
    let p = slide.querySelector(":scope > .ak-panel");
    if (!p) {
      p = h("div", "ak-panel");
      slide.append(p);
    }
    return p;
  }

  function renderPanel(slide, q, detail) {
    const p = panelOf(slide);
    if (!q || !detail) {
      p.replaceChildren(h("div", "ak-muted", "Ouverture de la question…"));
      return;
    }
    const s = detail.stats;
    const head = h("div", "ak-head",
      h("strong", "", `${s.answered} / ${s.participants}`), " réponses",
      s.typing ? h("span", "ak-muted", ` · ${s.typing} en train d'écrire`) : null,
      q.status === "open" ? h("span", "ak-muted", " · V pour valider") : h("span", "ak-done", " · validée"),
      s.correctPct != null && q.status === "closed" ? h("span", "ak-ok", ` · ${s.correctPct} % de réussite`) : null);
    const showResults = q.status === "closed" || slide.hasAttribute("data-ak-live-results");
    p.replaceChildren(head, showResults ? results(s) : null);
    relayout(p);
  }

  // reveal centres slides from their height: re-run its layout when ours changes
  function relayout(el) {
    const height = String(el.offsetHeight);
    if (el.dataset.akHeight === height) return;
    el.dataset.akHeight = height;
    deck.layout();
  }

  function results(s) {
    let buckets = [];
    if (s.distribution) buckets = s.distribution;
    else if (s.numeric) buckets = s.numeric.histogram;
    else if (s.textGroups) buckets = s.textGroups.slice(0, 6);
    const max = Math.max(1, ...buckets.map((b) => b.count));
    const bars = h("div", "ak-bars", buckets.map((b) => {
      const fill = h("div", "ak-fill");
      fill.style.width = `${(100 * b.count) / max}%`;
      return h("div", "ak-bar" + (b.correct ? " ak-correct" : ""),
        h("span", "ak-label", b.label), h("div", "ak-track", fill), h("span", "ak-n", b.count));
    }));
    const extra = s.numeric
      ? h("div", "ak-muted", `min ${s.numeric.min} · médiane ${s.numeric.median} · max ${s.numeric.max}`)
      : s.words && s.words.length && !s.distribution
        ? h("div", "ak-words", s.words.slice(0, 12).map((w) => h("span", "", w.word)))
        : null;
    return h("div", "", bars, extra);
  }

  async function renderJoin(slide) {
    const box = slide.querySelector(":scope > .ak-join") || h("div", "ak-join");
    slide.append(box);
    try {
      const res = await fetch("/admin/join-url");
      if (!res.ok) throw new Error(String(res.status));
      const data = await res.json();
      const img = h("img", "ak-qr");
      img.alt = "QR code pour rejoindre";
      img.addEventListener("load", () => relayout(box), { once: true });
      img.src = "/admin/qr.png?ts=" + Date.now();
      box.replaceChildren(img, h("div", "ak-code", data.code), h("div", "ak-url", data.url));
    } catch {
      box.replaceChildren(h("div", "ak-muted", "Connecte-toi sur /admin pour afficher le QR code."));
    }
    relayout(box);
  }

  function injectCSS() {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = "/static/reveal-ak.css";
    document.head.append(link);
  }

  function init(reveal) {
    deck = reveal;
    // reveal's speaker view embeds the deck in iframes: only the main window drives questions
    if (window.self !== window.top) return;
    const cfg = deck.getConfig().audienceKeeper || {};
    mirror = cfg.mirror !== false;
    injectCSS();
    connect();
    deck.on("slidechanged", (e) => { onSlide(e.currentSlide, e.previousSlide); mirrorSlide(); });
    deck.on("ready", (e) => { onSlide(e.currentSlide, null); mirrorSlide(); });
    deck.on("fragmentshown", mirrorSlide);
    deck.on("fragmenthidden", mirrorSlide);
    window.addEventListener("pagehide", () => { if (mirror) send({ t: "slide-off" }); });
    deck.addKeyBinding({ keyCode: 86, key: "V", description: "Valider la question (Audience Keeper)" }, validateCurrent);
  }

  root.AudienceKeeper = { id: "audience-keeper", init };
})(window);
