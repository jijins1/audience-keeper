// Java completion: members after a dot (resolved through the classes of the
// provided code and the JDK), otherwise variables, members of the enclosing
// class, class names and keywords.

import { analyse } from "./analysis.js";
import { exprType, scopeAt } from "./resolve.js";
import { isPrimitive, isUnknown, typeToString } from "./types.js";

const KEYWORDS = `abstract boolean break byte case catch char class continue default do double else enum extends
final finally float for if implements import instanceof int interface long new null private protected public
record return short static super switch this throw throws true false try var void while`.split(/\s+/);

const SILENT = new Set(["LineComment", "BlockComment", "StringLiteral", "TextBlock", "CharacterLiteral"]);
const RECEIVERS = new Set(["Identifier", "this", "super", "FieldAccess", "MethodInvocation", "ObjectCreationExpression",
  "ParenthesizedExpression", "ArrayAccess", "StringLiteral", "TextBlock", "CastExpression"]);

const params = (m) => m.params.map((p) => `${typeToString(p.type)} ${p.name}`).join(", ");

// Inserts name() and puts the cursor between the parentheses when the
// method takes arguments.
const call = (name, withArgs) => (view, _completion, from, to) => {
  const next = view.state.sliceDoc(to, to + 1);
  const insert = next === "(" ? name : name + "()";
  const cursor = from + name.length + (next === "(" || withArgs ? 1 : 2);
  view.dispatch({ changes: { from, to, insert }, selection: { anchor: cursor }, userEvent: "input.complete" });
};

function methodOptions(methods, boost = 0) {
  return methods.map((overloads) => {
    const m = overloads[0];
    const more = overloads.length > 1 ? ` (+${overloads.length - 1})` : "";
    return {
      label: m.name, type: "method", boost,
      detail: `(${params(m)}) : ${typeToString(m.ret)}${more}`,
      info: overloads.length > 1 ? overloads.map((o) => `${o.name}(${params(o)}) : ${typeToString(o.ret)}`).join("\n") : undefined,
      apply: call(m.name, overloads.some((o) => o.params.length)),
    };
  });
}

function fieldOptions(fields, boost = 0) {
  return fields.map((f) => ({
    label: f.name, boost,
    type: f.constant ? "enum" : f.isStatic ? "constant" : "property",
    detail: typeToString(f.type),
  }));
}

function memberOptions(model, type, staticOnly) {
  const { fields, methods } = model.members(type, staticOnly);
  // the type's own members first, Object's last
  const rank = (owner) => (owner === "Object" ? -2 : owner === type.name ? 1 : 0);
  return [
    ...fields.map((f) => fieldOptions([f], rank(f.owner))[0]),
    ...methods.map((ms) => methodOptions([ms], rank(ms[0].owner))[0]),
  ];
}

function classOptions(model, afterNew) {
  return model.classNames().map((name) => {
    const cls = model.cls(name);
    const user = cls.origin === "user";
    return {
      label: name, type: cls.kind === "interface" ? "interface" : cls.kind === "enum" ? "enum" : "class",
      detail: user ? "code fourni" : "java", boost: user ? 2 : -1,
      apply: afterNew && cls.kind === "class" ? call(name, cls.ctors.some((c) => c.params.length)) : undefined,
    };
  });
}

/** The expression just before a dot: the receiver of a member access. */
function receiverBefore(tree, dotPos) {
  let n = tree.resolveInner(dotPos, -1);
  if (n.to !== dotPos) return null;
  while (n.parent && n.parent.to === dotPos && n.parent.from <= n.from && !/Statement|Block|Body|Declaration|Program/.test(n.parent.name)) {
    n = n.parent;
  }
  return RECEIVERS.has(n.name) ? n : null;
}

export function javaCompletion(context) {
  const { state, pos } = context;
  const a = analyse(state);
  if (SILENT.has(a.tree.resolveInner(pos, -1).name)) return null;
  const word = context.matchBefore(/[\w$]*/);
  if (/^\d/.test(word.text)) return null;
  const before = state.sliceDoc(Math.max(0, word.from - 120), word.from);
  const dot = /\.\s*$/.exec(before);
  if (dot) {
    const dotPos = word.from - dot[0].length;
    const recv = receiverBefore(a.tree, dotPos);
    if (!recv) return null;
    const r = exprType(recv, a.src, scopeAt(a.tree, a.src, dotPos, a.model), a.model);
    if (!r || isUnknown(r.type) || isPrimitive(r.type)) return null;
    return { from: word.from, options: memberOptions(a.model, r.type, r.isClass), validFor: /^[\w$]*$/ };
  }
  if (word.from === word.to && !context.explicit) return null;
  const afterNew = /\bnew\s+$/.test(before);
  if (afterNew) return { from: word.from, options: classOptions(a.model, true), validFor: /^[\w$]*$/ };

  const scope = scopeAt(a.tree, a.src, pos, a.model);
  const options = scope.names()
    .filter((n) => n !== word.text)
    .map((n) => ({ label: n, type: "variable", detail: typeToString(scope.lookup(n)), boost: 3 }));
  if (scope.thisType) {
    const { fields, methods } = a.model.members(scope.thisType, scope.isStaticContext);
    options.push(...fieldOptions(fields, 1), ...methodOptions(methods.filter((ms) => ms[0].owner !== "Object"), 1));
  }
  options.push(...classOptions(a.model, false), ...KEYWORDS.map((k) => ({ label: k, type: "keyword", boost: -2 })));
  return { from: word.from, options, validFor: /^[\w$]*$/ };
}
