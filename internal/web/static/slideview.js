"use strict";

/*
 * Renders one mirrored reveal.js slide in a box, read-only. Shared by the
 * student page and the presenter view.
 *
 * The slide HTML comes from the teacher's deck: it is parsed inertly and
 * stripped of anything active, and both pages forbid inline scripts (CSP).
 */
(function (root) {
  const themes = new Set();

  function sanitize(html) {
    const doc = new DOMParser().parseFromString(`<body>${html}</body>`, "text/html");
    const section = doc.body.querySelector("section");
    if (!section) return null;
    section.querySelectorAll("script, meta, base, link, object, embed, form").forEach((n) => n.remove());
    for (const el of [section, ...section.querySelectorAll("*")]) {
      for (const a of [...el.attributes]) {
        const v = a.value.trim().toLowerCase();
        if (a.name.startsWith("on") || v.startsWith("javascript:") || (a.name === "src" && v.startsWith("data:text/html"))) el.removeAttribute(a.name);
      }
    }
    return document.importNode(section, true);
  }

  /** Same cleaning for speaker notes, returned as a fragment of child nodes. */
  function sanitizeFragment(html) {
    const section = sanitize(`<section>${html}</section>`);
    const frag = document.createDocumentFragment();
    if (section) frag.append(...section.childNodes);
    return frag;
  }

  function loadThemes(list) {
    for (const href of list || []) {
      if (themes.has(href) || href.endsWith("reset.css")) continue; // reset.css is global
      themes.add(href);
      const link = document.createElement("link");
      link.rel = "stylesheet";
      link.href = href;
      document.head.append(link);
    }
  }

  /**
   * @param {HTMLElement} box element containing `.reveal > .slides`
   * @returns {{ render(html: string, fragment: number, themes: string[]): void }}
   */
  function create(box) {
    let deck = null;
    let ready = null;
    let last = null;
    return {
      render(html, fragment, themeList) {
        if (typeof Reveal === "undefined") return;
        const key = html + "\u0000" + fragment;
        if (key === last) return;
        last = key;
        loadThemes(themeList);
        const section = html ? sanitize(html) : null;
        box.querySelector(".slides").replaceChildren(...(section ? [section] : []));
        if (!section) return;
        if (!deck) {
          deck = new Reveal(box.querySelector(".reveal"), {
            embedded: true, controls: false, progress: false, keyboard: false, touch: false, overview: false,
            help: false, hash: false, history: false, mouseWheel: false, transition: "none", backgroundTransition: "none",
            center: true, fragmentInURL: false, previewLinks: false, jumpToSlide: false, slideNumber: false,
            // reveal 5 switches narrow screens (< 435 px) to its scroll view, which breaks on a one-slide deck
            view: null, scrollActivationWidth: null,
          });
          ready = deck.initialize();
        }
        ready.then(() => { deck.sync(); deck.layout(); deck.slide(0, 0, fragment); });
      },
    };
  }

  root.AKSlideView = { create, sanitizeFragment };
})(window);
