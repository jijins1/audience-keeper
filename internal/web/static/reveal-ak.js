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
 *   <section data-ak-question="code" data-ak-language="java" data-ak-context="Commande, Client">
 *     <h2>Écris total()</h2>
 *     <script type="text/x-java" data-ak-template>
 *   class Solution extends Commande {
 *       @@
 *   }
 *     </script>
 *   </section>
 *     A code question: students get an editor with highlighting, completion and
 *     checks. The template (optional, never shown on the slide) is read-only
 *     code around the answer: one @@ line marks where students write, two @@
 *     lines enclose starter code. data-ak-context lists snippets shown above,
 *     read-only, typically the classes explained on earlier slides:
 *       <pre><code data-ak-snippet="Commande">class Commande { ... }</code></pre>
 *     ("*" takes every snippet of the deck). Hidden extra context:
 *     <script type="text/x-java" data-ak-context>...</script> in the slide.
 *
 *   Code from files rather than from the slides:
 *     data-ak-context-src="code/Ligne.java, https://github.com/o/r/blob/<commit>/src/Commande.java#L5-L40"
 *     on the question slide, and <code data-ak-src="..."> to show a file on a
 *     slide. Files of the course (same site) or public GitHub, pinned to a
 *     full commit hash so the code cannot change under the course (permalink:
 *     key "y" on GitHub). #L10-L40 keeps lines 10 to 40. The deck page fetches
 *     them; the server never does.
 *
 *   <section data-ak-join> ... </section>   shows the QR code and join code.
 *
 *   Key V validates the current question; key P opens the presenter view
 *   (notes, next slide, question controls), which also works on a phone as a
 *   remote control.
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

  const TYPES = ["text", "choice", "yesno", "number", "code"];
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
      if (m.t === "teacher") { tv = m.d; applyPendingValidate(); refresh(); }
      else if (m.t === "nav") navigate(m.d.action);
      else if (m.t === "error" && current) { current.problems = [...current.problems, m.d.msg].slice(-3); refresh(); }
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
    const heading = slide.querySelector("h1, h2, h3, h4, h5, h6");
    return heading ? heading.textContent.trim().slice(0, 300) : "";
  }

  // ---------- code questions ----------

  // Text of a snippet as written, even once reveal's highlight plugin has
  // turned it into spans (or a table, with line numbers).
  function codeText(el) {
    const code = el.matches("pre") && el.querySelector("code") ? el.querySelector("code") : el;
    const cells = code.querySelectorAll(".hljs-ln-code");
    const raw = cells.length ? [...cells].map((c) => c.textContent).join("\n") : code.textContent;
    return raw.replace(/^\n+|\s+$/g, "");
  }

  // ---------- code from files ----------

  const COMMIT = /^[0-9a-f]{40}$/;
  const MAX_SOURCE = 64 * 1024;
  const sources = new Map();    // spec -> Promise<string>
  const displayed = [];         // pending loads of <code data-ak-src>

  /** Where to fetch a source, and which lines to keep. Throws a readable error. */
  function sourceOf(spec) {
    const hash = spec.indexOf("#");
    const href = hash < 0 ? spec : spec.slice(0, hash);
    const lines = /^L(\d+)(?:-L(\d+))?$/.exec(hash < 0 ? "" : spec.slice(hash + 1));
    const range = lines ? [Number(lines[1]), Number(lines[2] || lines[1])] : null;
    let u;
    try { u = new URL(href, location.href); } catch { throw new Error(`adresse invalide : ${spec}`); }
    if (u.origin === location.origin) return { url: u.href, range, credentials: "same-origin" };
    let parts;
    if (u.protocol === "https:" && u.hostname === "github.com") {
      const m = /^\/([^/]+)\/([^/]+)\/blob\/([^/]+)\/(.+)$/.exec(u.pathname);
      if (!m) throw new Error(`lien GitHub vers un fichier attendu (…/blob/<commit>/chemin) : ${spec}`);
      parts = { commit: m[3], url: `https://raw.githubusercontent.com/${m[1]}/${m[2]}/${m[3]}/${m[4]}` };
    } else if (u.protocol === "https:" && u.hostname === "raw.githubusercontent.com") {
      parts = { commit: u.pathname.split("/")[3] || "", url: u.origin + u.pathname };
    } else {
      throw new Error(`seuls les fichiers du cours et GitHub public sont acceptés : ${spec}`);
    }
    if (!COMMIT.test(parts.commit)) {
      throw new Error(`lien GitHub non figé : utilise un permalien avec le commit complet (touche « y » sur GitHub) : ${spec}`);
    }
    return { url: parts.url, range, credentials: "omit" };
  }

  function loadSource(spec) {
    if (!sources.has(spec)) {
      sources.set(spec, (async () => {
        const src = sourceOf(spec);
        const res = await fetch(src.url, { credentials: src.credentials, signal: AbortSignal.timeout(10000) });
        if (!res.ok) throw new Error(`${spec} : ${res.status === 404 ? "introuvable" : "erreur " + res.status}`);
        let text = await res.text();
        if (text.length > MAX_SOURCE) throw new Error(`${spec} : fichier trop gros`);
        text = text.replace(/\r\n/g, "\n");
        if (src.range) text = text.split("\n").slice(src.range[0] - 1, src.range[1]).join("\n");
        return text.replace(/^\n+|\s+$/g, "");
      })());
      sources.get(spec).catch(() => {}); // reported where it is used
    }
    return sources.get(spec);
  }

  const specs = (attr) => (attr || "").split(",").map((x) => x.trim()).filter(Boolean);

  // <code data-ak-src>: show a file on a slide (and reuse it as a snippet).
  function loadDisplayed() {
    const hl = deck.getPlugin && deck.getPlugin("highlight");
    for (const el of document.querySelectorAll("[data-ak-src]")) {
      const spec = el.dataset.akSrc.trim();
      displayed.push(loadSource(spec).then((text) => {
        el.textContent = text;
        if (hl && hl.hljs && el.matches("code")) hl.hljs.highlightElement(el);
      }).catch((e) => {
        el.textContent = `// ${e.message}`;
      }).finally(() => {
        if (deck.getCurrentSlide() && deck.getCurrentSlide().contains(el)) { deck.layout(); mirrorSlide(); }
      }));
    }
    // prefetch: opening a question must not wait for the network
    for (const slide of document.querySelectorAll("[data-ak-context-src]")) specs(slide.dataset.akContextSrc).forEach(loadSource);
  }

  async function codeOf(slide) {
    await Promise.allSettled(displayed);
    const problems = [];
    const snippets = new Map();
    for (const el of document.querySelectorAll("[data-ak-snippet]")) {
      const name = el.dataset.akSnippet.trim();
      if (name && !snippets.has(name)) snippets.set(name, codeText(el));
    }
    const wanted = (slide.dataset.akContext || "").split(/[\s,]+/).filter(Boolean);
    const names = wanted.includes("*") ? [...snippets.keys()] : wanted;
    const missing = names.filter((n) => !snippets.has(n));
    if (missing.length) problems.push(`extrait introuvable : ${missing.join(", ")} (data-ak-snippet)`);
    const parts = names.filter((n) => snippets.has(n)).map((n) => snippets.get(n));
    for (const r of await Promise.allSettled(specs(slide.dataset.akContextSrc).map(loadSource))) {
      if (r.status === "fulfilled") parts.push(r.value);
      else problems.push(r.reason.message);
    }
    for (const el of slide.querySelectorAll("script[data-ak-context]")) parts.push(codeText(el));
    const tpl = slide.querySelector("script[data-ak-template]");
    return {
      code: { language: slide.dataset.akLanguage || "java", context: parts.join("\n\n"), template: tpl ? tpl.textContent.replace(/^\n+|\s+$/g, "") : "" },
      problems,
    };
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
    const here = { slide, key: keyOf(slide), problems: [] };
    current = here;
    clearTimeout(openTimer);
    openTimer = setTimeout(async () => {
      if (current !== here) return;
      const msg = { t: "create", key: here.key, title: titleOf(slide), type, choices: parseInt(slide.dataset.akChoices, 10) || 0, open: true };
      if (type === "code") {
        const c = await codeOf(slide);
        if (current !== here) return; // left the slide while files were loading
        msg.code = c.code;
        here.problems = c.problems;
      }
      send(msg);
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

  const MAX_NOTES = 60 * 1024;
  const bytes = (s) => new TextEncoder().encode(s).length;

  function shareable(slide) {
    if (!slide) return "";
    if (slide.hasAttribute("data-ak-private")) return '<section><h2>⏸</h2></section>';
    const html = snapshot(slide);
    return bytes(html) > MAX_HTML ? "<section><p>(slide trop lourde pour être diffusée)</p></section>" : html;
  }

  // Speaker notes: reveal accepts <aside class="notes"> or data-notes. They go
  // to the presenter page only — never to students.
  function notesOf(slide) {
    const aside = slide.querySelector(":scope > aside.notes");
    const notes = aside ? aside.innerHTML.trim() : (slide.getAttribute("data-notes") || "");
    return bytes(notes) > MAX_NOTES ? "(notes trop longues)" : notes;
  }

  function mirrorSlide() {
    if (!mirror || !deck) return;
    const slide = deck.getCurrentSlide();
    if (!slide) return;
    const i = deck.getIndices();
    const all = deck.getSlides();
    const pos = all.indexOf(slide);
    const msg = {
      t: "slide", deck: document.title.slice(0, 200), html: shareable(slide),
      h: i.h, v: i.v || 0, fragment: i.f == null ? -1 : i.f, themes: themes().slice(0, 8),
      // teacher-only: the server keeps these out of every student view
      notes: notesOf(slide), nextHtml: shareable(all[pos + 1]),
      number: pos + 1, total: all.length, fragments: slide.querySelectorAll(".fragment").length,
      questionKey: isQuestion(slide) ? keyOf(slide) : "",
    };
    const key = JSON.stringify(msg);
    if (key === lastMirror) return;
    lastMirror = key;
    send(msg);
  }

  // Remote control from the presenter page (laptop or phone).
  function navigate(action) {
    if (!deck) return;
    switch (action) {
      case "next": deck.next(); break;
      case "prev": deck.prev(); break;
      case "first": deck.slide(0, 0); break;
      case "last": deck.slide(deck.getHorizontalSlides().length - 1, 0); break;
      case "pause": deck.togglePause(); break;
    }
  }

  // V right after a question opened can come before the server's update: it
  // is then kept, and applied once the question shows as open.
  let pendingValidate = "";

  function validateCurrent() {
    if (!current) return;
    const q = byKey(current.key);
    if (q && q.status === "open") send({ t: "close", q: q.id });
    else if (!q || q.status === "proposed") pendingValidate = current.key;
  }

  function applyPendingValidate() {
    if (!pendingValidate) return;
    if (!current || current.key !== pendingValidate) { pendingValidate = ""; return; }
    const q = byKey(pendingValidate);
    if (q && q.status === "open") send({ t: "close", q: q.id });
    if (q && q.status !== "proposed") pendingValidate = "";
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
      const problems = current && current.slide === slide ? current.problems.map((x) => h("div", "ak-warn", "⚠ " + x)) : [];
      p.replaceChildren(h("div", "ak-muted", "Ouverture de la question…"), ...problems);
      return;
    }
    const s = detail.stats;
    const head = h("div", "ak-head",
      h("strong", "", `${s.answered} / ${s.participants}`), " réponses",
      s.typing ? h("span", "ak-muted", ` · ${s.typing} en train d'écrire`) : null,
      q.status === "open" ? h("span", "ak-muted", " · V pour valider") : h("span", "ak-done", " · validée"),
      s.correctPct != null && q.status === "closed" ? h("span", "ak-ok", ` · ${s.correctPct} % de réussite`) : null);
    const showResults = q.status === "closed" || slide.hasAttribute("data-ak-live-results");
    const problems = current && current.slide === slide && current.problems.length
      ? h("div", "ak-warn", current.problems.map((x) => h("div", "", "⚠ " + x))) : null;
    p.replaceChildren(...[head, problems, showResults ? results(s, q.type) : null].filter(Boolean)); // null would print "null"
    relayout(p);
  }

  // reveal centres slides from their height: re-run its layout when ours changes
  function relayout(el) {
    const height = String(el.offsetHeight);
    if (el.dataset.akHeight === height) return;
    el.dataset.akHeight = height;
    deck.layout();
  }

  // A code answer is long: its first meaningful line stands for it.
  const firstLine = (code) => (code.split("\n").map((l) => l.trim()).find((l) => l && !/^[{}]$/.test(l)) || code).slice(0, 80);

  function results(s, type) {
    let buckets = [];
    if (s.distribution) buckets = s.distribution;
    else if (s.numeric) buckets = s.numeric.histogram;
    else if (s.textGroups) buckets = s.textGroups.slice(0, 6);
    if (type === "code") buckets = buckets.map((b) => ({ ...b, label: firstLine(b.label) }));
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
    deck.on("ready", (e) => { loadDisplayed(); onSlide(e.currentSlide, null); mirrorSlide(); });
    deck.on("fragmentshown", mirrorSlide);
    deck.on("fragmenthidden", mirrorSlide);
    window.addEventListener("pagehide", () => { if (mirror) send({ t: "slide-off" }); });
    deck.addKeyBinding({ keyCode: 86, key: "V", description: "Valider la question (Audience Keeper)" }, validateCurrent);
    // P : la vue présentateur (notes, suivante, questionnaire) dans une seconde fenêtre.
    deck.addKeyBinding({ keyCode: 80, key: "P", description: "Vue présentateur (Audience Keeper)" },
      () => window.open("/admin/presenter", "ak-presenter", "popup,width=1280,height=800"));
  }

  root.AudienceKeeper = { id: "audience-keeper", init };
})(window);
