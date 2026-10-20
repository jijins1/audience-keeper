// Code editor of Audience Keeper, built on CodeMirror 6 and exposed as
// window.AKEditor (the pages load no module system).
//
// The document is: provided code (read-only, folded when long), the template
// before the answer (read-only), the answer (editable), the template after it
// (read-only). Only the answer is read back with getValue().

import { EditorState, StateField, Compartment, RangeSetBuilder } from "@codemirror/state";
import { EditorView, keymap, lineNumbers, drawSelection, highlightActiveLine, highlightSpecialChars, Decoration } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { indentOnInput, bracketMatching, syntaxHighlighting, foldGutter, foldKeymap, indentUnit, foldEffect, ensureSyntaxTree } from "@codemirror/language";
import { autocompletion, closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete";
import { linter, lintGutter } from "@codemirror/lint";
import { classHighlighter, highlightTree } from "@lezer/highlight";
import { java, javaLanguage } from "@codemirror/lang-java";
import { javaCompletion } from "./java/complete.js";
import { javaDiagnostics } from "./java/lint.js";

const LANGUAGES = {
  java: { support: java, parser: javaLanguage.parser, complete: javaCompletion, check: javaDiagnostics, indent: "    " },
};

const FOLD_CONTEXT_LINES = 12;

// Edits the editor makes on the student's behalf: they are not a sudden
// insertion of foreign text. Paste and drop are never in this list.
const ASSISTED = ["input.complete", "input.indent", "indent", "undo", "redo", "move", "input.comment"];

function insertedBy(tr) {
  // Enter adds the newline plus the indentation of the next line
  const assisted = ASSISTED.some((e) => tr.isUserEvent(e)) || tr.isUserEvent("input") && !tr.isUserEvent("input.type") &&
    !tr.isUserEvent("input.paste") && !tr.isUserEvent("input.drop");
  if (!assisted) return 0;
  let n = 0;
  tr.changes.iterChanges((_fa, _ta, _fb, _tb, text) => { n += text.length; });
  return n;
}

// ---------- answer zone ----------

const zoneField = StateField.define({
  create: () => ({ from: 0, to: 0 }),
  update(z, tr) {
    // text typed at either edge belongs to the answer
    return tr.docChanged ? { from: tr.changes.mapPos(z.from, -1), to: tr.changes.mapPos(z.to, 1) } : z;
  },
});

const zone = (state) => state.field(zoneField);

// Same limit as the server (runes there, UTF-16 units here: close enough).
const MAX_ANSWER = 10000;

// Edits outside the answer are dropped whole: provided code cannot change.
// So are edits that would make the answer longer than the server accepts.
const guard = EditorState.transactionFilter.of((tr) => {
  if (!tr.docChanged) return tr;
  const z = zone(tr.startState);
  let inside = true;
  tr.changes.iterChangedRanges((from, to) => { if (from < z.from || to > z.to) inside = false; });
  if (!inside) return [];
  const after = tr.changes.mapPos(z.to, 1) - tr.changes.mapPos(z.from, -1);
  return after > MAX_ANSWER && after > z.to - z.from ? [] : tr;
});

const lockedLine = Decoration.line({ class: "cm-ak-locked" });
const answerLine = Decoration.line({ class: "cm-ak-answer" });

function lineDecorations(state) {
  const z = zone(state);
  const b = new RangeSetBuilder();
  for (let i = 1; i <= state.doc.lines; i++) {
    const line = state.doc.line(i);
    b.add(line.from, line.from, line.to < z.from || line.from > z.to ? lockedLine : answerLine);
  }
  return b.finish();
}

const lines = StateField.define({
  create: lineDecorations,
  update: (deco, tr) => (tr.docChanged ? lineDecorations(tr.state) : deco),
  provide: (f) => EditorView.decorations.from(f),
});

function selectAnswer(view) {
  const z = zone(view.state);
  view.dispatch({ selection: { anchor: z.from, head: z.to } });
  return true;
}

// ---------- look ----------

const theme = EditorView.theme({
  "&": { fontSize: "14px", border: "1px solid var(--line)", borderRadius: "8px", background: "var(--panel)", color: "var(--ink)", maxHeight: "65vh" },
  "&.cm-focused": { outline: "2px solid var(--accent)", outlineOffset: "-1px" },
  ".cm-scroller": { fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace", lineHeight: "1.5", overflow: "auto" },
  ".cm-content": { minHeight: "9em", caretColor: "var(--ink)" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--ink)" },
  ".cm-gutters": { background: "var(--chip)", color: "var(--muted)", border: "none", borderRadius: "8px 0 0 8px" },
  ".cm-activeLine": { background: "color-mix(in srgb, var(--accent) 7%, transparent)" },
  ".cm-ak-locked": { background: "color-mix(in srgb, var(--chip) 75%, transparent)", color: "var(--muted)" },
  ".cm-ak-answer": { boxShadow: "inset 3px 0 0 var(--accent)" },
  "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground": { background: "color-mix(in srgb, var(--accent) 28%, transparent)" },
  ".cm-foldPlaceholder": { background: "var(--chip)", border: "1px solid var(--line)", color: "var(--muted)", padding: "0 6px" },
  ".cm-tooltip": { background: "var(--panel)", color: "var(--ink)", border: "1px solid var(--line)", borderRadius: "8px" },
  ".cm-tooltip-autocomplete > ul": { fontFamily: "ui-monospace, Menlo, Consolas, monospace", maxHeight: "14em" },
  ".cm-tooltip-autocomplete > ul > li[aria-selected]": { background: "var(--accent)", color: "var(--accent-ink)" },
  ".cm-completionDetail": { opacity: ".7", marginLeft: "1em", fontStyle: "normal" },
  ".cm-diagnostic": { fontFamily: "system-ui, sans-serif", whiteSpace: "pre-wrap" },
  ".cm-diagnosticAction": { background: "var(--accent)", color: "var(--accent-ink)", borderRadius: "6px" },
});

// ---------- editor ----------

/** Folds the classes of the provided code when it is long. */
function foldContext(view, contextEnd) {
  if (!contextEnd || view.state.doc.lineAt(contextEnd).number <= FOLD_CONTEXT_LINES) return;
  const tree = ensureSyntaxTree(view.state, contextEnd, 500);
  if (!tree) return;
  const effects = [];
  for (let n = tree.topNode.firstChild; n; n = n.nextSibling) {
    if (n.to > contextEnd) break;
    const body = n.getChild("ClassBody") || n.getChild("InterfaceBody") || n.getChild("EnumBody") || n.getChild("RecordBody");
    if (body && body.to - body.from > 2) effects.push(foldEffect.of({ from: body.from + 1, to: body.to - 1 }));
  }
  if (effects.length) view.dispatch({ effects });
}

/**
 * create(parent, { language, context, before, starter, after, value, onChange, onSubmit, readOnly })
 * value (a draft) replaces the starter code when it is not empty.
 */
function create(parent, opts = {}) {
  const lang = LANGUAGES[opts.language] || LANGUAGES.java;
  const context = opts.context ? opts.context.replace(/\n*$/, "\n\n") : "";
  const head = context + (opts.before || "");
  const answer = opts.value ? opts.value : opts.starter || "";
  const doc = head + answer + (opts.after || "");
  const readOnly = new Compartment();
  // phones: wrap long lines rather than scroll sideways
  const narrow = typeof matchMedia === "function" && matchMedia("(max-width: 600px)").matches;
  const lock = (ro) => (ro ? [EditorState.readOnly.of(true), EditorView.editable.of(false)] : []);

  const state = EditorState.create({
    doc,
    selection: { anchor: head.length + answer.length },
    extensions: [
      zoneField.init(() => ({ from: head.length, to: head.length + answer.length })),
      guard, lines, theme,
      lineNumbers(), foldGutter(), lintGutter(), highlightSpecialChars(), history(), drawSelection(),
      highlightActiveLine(), indentOnInput(), bracketMatching(), closeBrackets(),
      indentUnit.of(lang.indent), EditorState.tabSize.of(4),
      lang.support(), syntaxHighlighting(classHighlighter),
      autocompletion({ override: [lang.complete], icons: true }),
      linter((view) => lang.check(view.state, zone(view.state)), { delay: 500 }),
      keymap.of([
        { key: "Mod-Enter", run: () => { if (opts.onSubmit) opts.onSubmit(); return true; } },
        { key: "Mod-a", run: selectAnswer },
        ...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, ...foldKeymap, indentWithTab,
      ]),
      narrow ? EditorView.lineWrapping : [],
      EditorView.contentAttributes.of({ autocapitalize: "off", autocorrect: "off", spellcheck: "false", "aria-label": "Éditeur de code" }),
      EditorView.updateListener.of((u) => {
        if (!u.docChanged || !opts.onChange) return;
        const assisted = u.transactions.reduce((n, tr) => n + insertedBy(tr), 0);
        opts.onChange(u.state.sliceDoc(zone(u.state).from, zone(u.state).to), assisted);
      }),
      readOnly.of(lock(opts.readOnly)),
    ],
  });
  const view = new EditorView({ state, parent });
  foldContext(view, context.length);
  view.dispatch({ effects: EditorView.scrollIntoView(head.length, { y: "center" }) });

  return {
    view,
    getValue: () => view.state.sliceDoc(zone(view.state).from, zone(view.state).to),
    /** Characters currently selected (copy / cut signals). */
    selectionLength: () => view.state.selection.ranges.reduce((n, r) => n + r.to - r.from, 0),
    setReadOnly: (ro) => view.dispatch({ effects: readOnly.reconfigure(lock(ro)) }),
    focus: () => view.focus(),
    destroy: () => view.destroy(),
  };
}

/**
 * Syntax-highlighted copy of a piece of code, as DOM nodes (spans with
 * tok-* classes, text only: safe for student-written code). before/after are
 * the code around it, used for parsing only.
 */
function highlight(code, opts = {}) {
  const lang = LANGUAGES[opts.language] || LANGUAGES.java;
  const before = opts.before || "";
  const full = before + code + (opts.after || "");
  const out = document.createDocumentFragment();
  const start = before.length;
  const end = start + code.length;
  let pos = start;
  highlightTree(lang.parser.parse(full), classHighlighter, (from, to, cls) => {
    if (from > pos) out.append(full.slice(pos, from));
    const span = document.createElement("span");
    span.className = cls;
    span.textContent = full.slice(from, to);
    out.append(span);
    pos = to;
  }, start, end);
  if (pos < end) out.append(full.slice(pos, end));
  return out;
}

// viewOf(element): the CodeMirror view an element belongs to (tests, debugging).
const viewOf = (el) => EditorView.findFromDOM(el);

window.AKEditor = { create, highlight, viewOf, languages: Object.keys(LANGUAGES) };
