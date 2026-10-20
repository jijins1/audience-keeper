// Java checks run in the browser while the student types:
//  - syntax errors (always), reported inside the answer even when the parser
//    only notices them in the provided code that follows;
//  - names that do not exist (method, field, variable, type), only where the
//    whole picture is known: a class of the provided code whose supertypes
//    are all known. JDK classes are partial, so they are never blamed.

import { analyse, closest } from "./analysis.js";
import { JDK_NAMES } from "./jdk.js";
import { text } from "./model.js";
import { exprType, methodCall, scopeAt } from "./resolve.js";
import { typeToString } from "./types.js";

const MAX_SYNTAX = 5;
const MAX_NAMES = 20;

// Identifiers that are not variable reads.
const NOT_A_READ = new Set(["MethodName", "MarkerAnnotation", "Annotation", "LabeledStatement", "BreakStatement",
  "ContinueStatement", "ImportDeclaration", "ScopedIdentifier", "PackageDeclaration", "SwitchLabel", "ElementValuePair"]);
const PACKAGES = new Set(["java", "javax", "jakarta", "org", "com", "net"]);

function fix(name) {
  return name ? [{ name: `Remplacer par ${name}`, apply: (view, from, to) => view.dispatch({ changes: { from, to, insert: name } }) }] : undefined;
}

const KEYWORDS = ["return", "while", "for", "if", "else", "new", "public", "private", "protected", "static", "final",
  "void", "class", "int", "double", "boolean", "String", "this", "null", "true", "false", "break", "continue", "switch", "case"];

function hint(name, candidates) {
  const c = name.length < 3 ? null : closest(name, candidates);
  return { suggestion: c, msg: c ? ` Tu voulais dire « ${c} » ?` : "" };
}

// Opened minus closed ( [ { in the answer, ignoring strings and comments.
function balance(code) {
  const stripped = code.replace(/"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'|\/\/.*|\/\*[\s\S]*?\*\//g, "");
  const count = (re) => (stripped.match(re) || []).length;
  return count(/[({[]/g) - count(/[)}\]]/g);
}

// "retrun 0;": the parser stops on 0, the typo is the word before it.
function keywordTypo(src, from) {
  const start = Math.max(0, from - 40);
  const m = /([A-Za-z]+)\s*$/.exec(src.slice(start, from));
  if (!m || KEYWORDS.includes(m[1])) return null;
  const k = m[1].length >= 3 ? closest(m[1], KEYWORDS) : null;
  return k ? { word: m[1], keyword: k, from: start + m.index, to: start + m.index + m[1].length } : null;
}

function syntaxErrors(tree, src, zone, out) {
  const seen = new Set();
  tree.iterate({
    enter(n) {
      if (!n.type.isError || out.length >= MAX_SYNTAX) return;
      let { from, to } = n;
      let message;
      const typo = from >= zone.from && from <= zone.to ? keywordTypo(src, from) : null;
      if (typo && typo.from >= zone.from) {
        if (seen.has(typo.from)) return;
        seen.add(typo.from);
        out.push({ from: typo.from, to: typo.to, severity: "error", source: "syntaxe",
          message: `« ${typo.word} » : tu voulais dire « ${typo.keyword} » ?`, actions: fix(typo.keyword) });
        return;
      }
      if (from >= zone.to && zone.to < src.length) {
        const b = balance(src.slice(zone.from, zone.to));
        message = b > 0 ? "Bloc non terminé : il manque sans doute une « } » ou une « ) » dans ta réponse."
          : b < 0 ? "Il y a une « } » ou une « ) » de trop dans ta réponse."
            : "Ta réponse n'est pas terminée : il manque sans doute un « ; » à la fin.";
        from = to = zone.to;
      } else if (from < zone.from) {
        from = to = zone.from;
        message = "Erreur de syntaxe au début de ta réponse.";
      } else {
        to = Math.min(to, zone.to);
        const bad = src.slice(from, to).trim();
        message = balance(src.slice(zone.from, from)) < 0 ? "Il y a une « } » ou une « ) » de trop avant cet endroit."
          : bad ? `Syntaxe incorrecte : « ${bad.slice(0, 30)} » n'est pas attendu ici.` : "Il manque quelque chose ici (un « ; », une parenthèse, une accolade… ?)";
      }
      if (seen.has(from)) return;
      seen.add(from);
      out.push({ from, to, severity: "error", message, source: "syntaxe" });
    },
  });
}

function imports(tree, src) {
  const names = new Set();
  let wildcardElsewhere = false;
  let staticImport = false;
  for (const d of tree.topNode.getChildren("ImportDeclaration")) {
    const t = text(src, d);
    if (/\bstatic\b/.test(t)) staticImport = true;
    const m = /([\w$]+)\s*;\s*$/.exec(t);
    if (m) names.add(m[1]);
    else if (!/java\.(util|lang|io|util\.function|util\.stream|time)\.\*/.test(t)) wildcardElsewhere = true;
  }
  return { names, wildcardElsewhere, staticImport };
}

// Name warnings are noise next to a syntax error: the parser's recovery
// produces odd nodes there.
function statementOf(node) {
  for (let n = node; n; n = n.parent) {
    if (/Statement$|^LocalVariableDeclaration$|^FieldDeclaration$/.test(n.name)) return n;
  }
  return node;
}

function nameChecks(a, zone, out, errors) {
  const { tree, src } = a;
  const imp = imports(tree, src);
  const report = (node, message, suggestion) => {
    const st = statementOf(node);
    if (errors.some((e) => e.from >= st.from && e.from <= st.to)) return;
    if (out.length < MAX_NAMES) out.push({ from: node.from, to: node.to, severity: "warning", message, source: "noms", actions: fix(suggestion) });
  };
  tree.iterate({
    from: zone.from, to: zone.to,
    enter(n) {
      if (n.from < zone.from || n.to > zone.to) return;
      const node = n.node;
      switch (n.name) {
        case "MethodInvocation": checkCall(a, node, report); break;
        case "FieldAccess": checkField(a, node, report); break;
        case "Identifier": if (!imp.staticImport) checkIdentifier(a, node, report); break;
        case "TypeName": if (!imp.wildcardElsewhere) checkType(a, node, imp.names, report); break;
      }
    },
  });
}

function checkCall(a, node, report) {
  const { tree, src, model } = a;
  const scope = scopeAt(tree, src, node.from, model);
  const mc = methodCall(node, src, scope, model);
  if (!mc || !model.complete(mc.recv.type)) return;
  const where = typeToString(mc.recv.type);
  if (!mc.all.length) {
    const { methods } = model.members(mc.recv.type, false);
    const h = hint(mc.name, methods.map((ms) => ms[0].name));
    report(mc.nameNode, `La méthode « ${mc.name} » n'existe pas dans ${where}.${h.msg}`, h.suggestion);
  } else if (!mc.fits) {
    const counts = [...new Set(mc.all.map((m) => m.params.length))].join(" ou ");
    report(mc.nameNode, `« ${mc.name} » attend ${counts} paramètre(s), pas ${mc.arity}.`);
  }
}

function checkField(a, node, report) {
  const { tree, src, model } = a;
  const last = node.lastChild;
  if (!last || last.name !== "Identifier") return;
  const r = exprType(node.firstChild, src, scopeAt(tree, src, node.from, model), model);
  if (!r || !model.complete(r.type)) return;
  const name = text(src, last);
  if (model.field(r.type, name) || (r.isClass && model.cls(name))) return;
  const { fields, methods } = model.members(r.type, false);
  const h = hint(name, [...fields.map((f) => f.name), ...methods.map((ms) => ms[0].name)]);
  const isMethod = methods.some((ms) => ms[0].name === name);
  report(last, isMethod ? `« ${name} » est une méthode : il manque les parenthèses « () ».`
    : `« ${name} » n'existe pas dans ${typeToString(r.type)}.${h.msg}`, isMethod ? null : h.suggestion);
}

function checkIdentifier(a, node, report) {
  const { tree, src, model } = a;
  const parent = node.parent;
  if (!parent || NOT_A_READ.has(parent.name)) return;
  if (parent.name === "FieldAccess" && parent.firstChild !== node) return; // the member, checked by checkField
  for (let p = parent; p; p = p.parent) if (p.name === "SwitchLabel") return; // enum constants
  const name = text(src, node);
  if (parent.name === "FieldAccess" && PACKAGES.has(name)) return;
  const scope = scopeAt(tree, src, node.from, model);
  if (!scope.thisType || !model.complete(scope.thisType)) return;
  if (scope.lookup(name) !== undefined || model.field(scope.thisType, name) || model.cls(name) || JDK_NAMES.has(name)) return;
  const { fields } = model.members(scope.thisType, false);
  const h = hint(name, [...scope.names(), ...fields.map((f) => f.name)]);
  report(node, `« ${name} » n'est pas déclaré.${h.msg}`, h.suggestion);
}

function checkType(a, node, imported, report) {
  const { tree, src, model } = a;
  if (node.parent && node.parent.name === "ScopedTypeName") return;
  const name = text(src, node);
  if (name.length === 1 || model.cls(name) || JDK_NAMES.has(name) || imported.has(name)) return;
  const scope = scopeAt(tree, src, node.from, model);
  if (scope.typeParams.has(name)) return;
  const h = hint(name, model.classNames());
  report(node, `Type inconnu : « ${name} » (ni dans le code fourni, ni dans les classes Java courantes).${h.msg}`, h.suggestion);
}

/** Diagnostics for the answer zone { from, to } of the document. */
export function javaDiagnostics(state, zone) {
  const a = analyse(state);
  const out = [];
  syntaxErrors(a.tree, a.src, zone, out);
  const names = [];
  nameChecks(a, zone, names, out);
  return out.concat(names);
}

