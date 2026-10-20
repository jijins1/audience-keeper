// What is visible at a position (locals, parameters, enclosing class) and
// the static type of an expression, as far as it can be worked out without a
// compiler. Anything uncertain resolves to null: callers then stay quiet.

import { child, children, paramsOf, text, TYPE_NODES, typeOf } from "./model.js";
import { parseType, ref, UNKNOWN } from "./types.js";

const CLASS_DECLS = new Set(["ClassDeclaration", "InterfaceDeclaration", "EnumDeclaration", "RecordDeclaration"]);
const BLOCKS = new Set(["Block", "ConstructorBody", "SwitchBlock", "SwitchBlockStatementGroup"]);
const PUNCT = /^[()[\]{};,.:]$/;
const MAX_DEPTH = 12;

/**
 * Scope at pos: variables (nearest declaration wins), the enclosing class and
 * the type variables in scope.
 */
export function scopeAt(tree, src, pos, model, depth = 0) {
  const vars = new Map();
  const typeParams = new Set();
  let thisClass = null;
  let isStaticContext = false;
  const add = (name, type, lazy) => { if (name && !vars.has(name)) vars.set(name, { type, lazy }); };

  for (let n = tree.resolveInner(pos, -1); n; n = n.parent) {
    if (BLOCKS.has(n.name)) {
      for (const c of children(n).reverse()) {
        if (c.from >= pos) continue;
        if (c.name === "LocalVariableDeclaration") addLocals(src, c, add);
      }
    } else if (n.name === "ForStatement") {
      const spec = child(n, "ForSpec");
      if (spec) for (const c of spec.getChildren("LocalVariableDeclaration")) addLocals(src, c, add);
    } else if (n.name === "EnhancedForStatement") {
      const spec = child(n, "ForSpec");
      if (spec) {
        const def = child(spec, "Definition");
        const declared = typeOf(src, spec);
        const iterable = children(spec).filter((c) => !TYPE_NODES.has(c.name) && !PUNCT.test(c.name) && c.name !== "Definition" && c.name !== "Modifiers").pop();
        add(text(src, def), declared, declared ? null : { kind: "element", node: iterable });
      }
    } else if (n.name === "CatchClause") {
      const p = child(n, "CatchFormalParameter");
      if (p) {
        const types = child(p, "CatchType");
        const names = types ? types.getChildren("TypeName") : [];
        add(text(src, child(p, "Definition")), names.length === 1 ? parseType(text(src, names[0])) : ref("Exception"));
      }
    } else if (n.name === "LambdaExpression") {
      const typed = child(n, "FormalParameters");
      if (typed) for (const p of paramsOf(src, typed)) add(p.name, p.type || UNKNOWN);
      const inferred = child(n, "InferredParameters");
      for (const d of inferred ? inferred.getChildren("Definition") : n.getChildren("Definition")) add(text(src, d), UNKNOWN);
    } else if (n.name === "MethodDeclaration" || n.name === "ConstructorDeclaration") {
      for (const p of paramsOf(src, child(n, "FormalParameters"))) add(p.name, p.type);
      const tp = child(n, "TypeParameters");
      if (tp) for (const p of tp.getChildren("TypeParameter")) typeParams.add(text(src, child(p, "Definition")));
      if (!thisClass && /\bstatic\b/.test(text(src, child(n, "Modifiers")))) isStaticContext = true;
    } else if (CLASS_DECLS.has(n.name)) {
      const tp = child(n, "TypeParameters");
      if (tp) for (const p of tp.getChildren("TypeParameter")) typeParams.add(text(src, child(p, "Definition")));
      if (!thisClass) thisClass = text(src, child(n, "Definition"));
    } else if (n.name === "ClassBody" && n.parent && n.parent.name === "ObjectCreationExpression" && !thisClass) {
      const t = typeOf(src, n.parent);
      thisClass = t ? t.name : null; // anonymous class
    }
  }

  const scope = {
    thisClass, typeParams, isStaticContext,
    thisType: thisClass ? ref(thisClass) : null,
    has: (name) => vars.has(name),
    names: () => [...vars.keys()],
    /** Type of a variable, or undefined when it is not a variable in scope. */
    lookup(name) {
      const v = vars.get(name);
      if (!v) return undefined;
      if (v.type) return v.type;
      if (!v.lazy || depth > MAX_DEPTH) return UNKNOWN;
      // var x = ... and for (var x : xs): infer from the expression
      const inner = scopeAt(tree, src, v.lazy.node.from, model, depth + 1);
      const r = exprType(v.lazy.node, src, inner, model, depth + 1);
      v.type = (r && (v.lazy.kind === "element" ? elementType(r.type, model) : r.type)) || UNKNOWN;
      return v.type;
    },
  };
  return scope;
}

function addLocals(src, decl, add) {
  const t = typeOf(src, decl);
  for (const d of decl.getChildren("VariableDeclarator")) {
    const name = text(src, child(d, "Definition"));
    if (t) add(name, { ...t, dims: t.dims + d.getChildren("Dimension").length });
    else {
      const init = children(d).find((c) => c.name !== "Definition" && c.name !== "AssignOp" && c.name !== "Dimension");
      add(name, init ? null : UNKNOWN, init ? { kind: "value", node: init } : null);
    }
  }
}

/** Element type of an array or an Iterable (for-each loops). */
export function elementType(t, model) {
  if (!t) return null;
  if (t.dims) return { ...t, dims: t.dims - 1 };
  for (const h of model.hierarchy(t)) {
    if (h.name === "Iterable") return h.map.get("T") || null;
  }
  return null;
}

const LITERALS = {
  StringLiteral: "String", TextBlock: "String", IntegerLiteral: "int", FloatingPointLiteral: "double",
  CharacterLiteral: "char", BooleanLiteral: "boolean", true: "boolean", false: "boolean",
};

const operands = (node) => children(node).filter((c) => !PUNCT.test(c.name) && !/Op$/.test(c.name) && c.name !== "⚠");

/**
 * Type of an expression node: { type, isClass } where isClass means the
 * expression names a class (Math, Commande) rather than a value.
 */
export function exprType(node, src, scope, model, depth = 0) {
  if (!node || depth > MAX_DEPTH) return null;
  const sub = (n) => exprType(n, src, scope, model, depth + 1);
  const value = (t) => (t ? { type: t, isClass: false } : null);
  if (LITERALS[node.name]) return value(ref(LITERALS[node.name]));
  switch (node.name) {
    case "Identifier": {
      const name = text(src, node);
      const v = scope.lookup(name);
      if (v !== undefined) return value(v);
      if (scope.thisType) {
        const f = model.field(scope.thisType, name);
        if (f) return value(f.type);
      }
      return model.cls(name) ? { type: ref(name), isClass: true } : null;
    }
    case "this":
      return value(scope.thisType);
    case "super": {
      const cls = scope.thisClass && model.cls(scope.thisClass);
      return value(cls && cls.superclass);
    }
    case "FieldAccess": {
      const [recv, last] = [node.firstChild, node.lastChild];
      const r = sub(recv);
      if (!r || last.name !== "Identifier") return null;
      const name = text(src, last);
      const f = model.field(r.type, name);
      if (f) return value(f.type);
      return r.isClass && model.cls(name) ? { type: ref(name), isClass: true } : null; // Map.Entry
    }
    case "MethodInvocation": {
      const m = methodCall(node, src, scope, model, depth);
      return m && m.methods.length ? value(m.methods[0].ret) : null;
    }
    case "ObjectCreationExpression": {
      if (!child(node, "new")) return sub(node.firstChild); // "x." being typed parses this way
      return value(typeOf(src, node));
    }
    case "ArrayCreationExpression": {
      const t = typeOf(src, node);
      return value(t && { ...t, dims: t.dims + node.getChildren("Dimension").length });
    }
    case "ParenthesizedExpression":
      return sub(operands(node)[0]);
    case "CastExpression":
      return value(typeOf(src, node));
    case "ArrayAccess": {
      const r = sub(node.firstChild);
      return r && r.type.dims ? value({ ...r.type, dims: r.type.dims - 1 }) : null;
    }
    case "TernaryExpression":
      return sub(operands(node)[1]);
    case "AssignmentExpression":
    case "UpdateExpression":
      return sub(node.firstChild);
    case "InstanceofExpression":
      return value(ref("boolean"));
    case "UnaryExpression": {
      const op = text(src, node.firstChild);
      return op === "!" ? value(ref("boolean")) : sub(operands(node)[0]);
    }
    case "BinaryExpression": {
      const op = children(node).find((c) => /Op$/.test(c.name));
      const opText = op ? text(src, op) : "";
      if (/^(==|!=|<|>|<=|>=|&&|\|\|)$/.test(opText)) return value(ref("boolean"));
      const [l, r] = operands(node).map(sub);
      if (opText === "+" && [l, r].some((x) => x && x.type.name === "String" && !x.type.dims)) return value(ref("String"));
      return l;
    }
    case "ClassLiteral":
      return value(ref("Class"));
    default:
      return null;
  }
}

/**
 * Resolves a method call: its receiver type and the candidate methods.
 * Returns null when the receiver cannot be typed.
 */
export function methodCall(node, src, scope, model, depth = 0) {
  const nameNode = child(node, "MethodName");
  if (!nameNode) return null;
  const name = text(src, nameNode);
  const dotted = children(node).some((c) => c.name === ".");
  let recv;
  if (dotted) {
    recv = exprType(node.firstChild, src, scope, model, depth + 1);
  } else {
    recv = scope.thisType ? { type: scope.thisType, isClass: false } : null;
  }
  if (!recv) return null;
  const args = child(node, "ArgumentList");
  const arity = args ? operands(args).length : 0;
  const all = model.methods(recv.type, name);
  const varargs = (m) => m.params.length && m.params.at(-1).type && m.params.at(-1).type.dims && arity >= m.params.length - 1;
  const fit = all.filter((m) => m.params.length === arity || varargs(m));
  return { recv, name, nameNode, arity, all, methods: fit.length ? fit : all, fits: fit.length > 0 };
}
