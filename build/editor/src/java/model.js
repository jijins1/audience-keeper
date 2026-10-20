// Classes, fields and methods declared in a Java syntax tree, plus lookups
// that follow inheritance and generics (List<Ligne>.get() returns a Ligne).

import { parser } from "@lezer/java";
import { JDK_SOURCE } from "./jdk.js";
import { parseType, ref, simpleName, subst, UNKNOWN } from "./types.js";

const DECLS = { ClassDeclaration: "class", InterfaceDeclaration: "interface", EnumDeclaration: "enum", RecordDeclaration: "record" };
const BODIES = new Set(["ClassBody", "InterfaceBody", "EnumBody", "RecordBody"]);
export const TYPE_NODES = new Set(["TypeName", "GenericType", "ArrayType", "PrimitiveType", "ScopedTypeName", "void", "var"]);

export const children = (node) => {
  const out = [];
  for (let c = node.firstChild; c; c = c.nextSibling) out.push(c);
  return out;
};
export const child = (node, name) => node.getChild(name);
export const text = (src, node) => (node ? src.slice(node.from, node.to) : "");

export function typeOf(src, node) {
  const t = children(node).find((c) => TYPE_NODES.has(c.name));
  return t ? parseType(text(src, t)) : null;
}

const hasModifier = (src, node, word) => {
  const m = child(node, "Modifiers");
  return !!m && new RegExp(`\\b${word}\\b`).test(text(src, m));
};

const typeParamsOf = (src, node) => {
  const tp = child(node, "TypeParameters");
  return tp ? tp.getChildren("TypeParameter").map((p) => text(src, child(p, "Definition"))) : [];
};

const dims = (node) => node.getChildren("Dimension").length;

/** Parameters of a method, constructor or lambda. */
export function paramsOf(src, formal) {
  const out = [];
  if (!formal) return out;
  for (const p of children(formal)) {
    if (p.name === "FormalParameter") {
      const t = typeOf(src, p);
      out.push({ name: text(src, child(p, "Definition")), type: t && { ...t, dims: t.dims + dims(p) } });
    } else if (p.name === "SpreadParameter") {
      const t = typeOf(src, p);
      const d = child(p, "VariableDeclarator");
      out.push({ name: text(src, d && child(d, "Definition")), type: t && { ...t, dims: t.dims + 1 } });
    }
  }
  return out;
}

function addMethod(info, m) {
  const list = info.methods.get(m.name) || [];
  list.push(m);
  info.methods.set(m.name, list);
}

function readMember(src, info, m) {
  const isStatic = hasModifier(src, m, "static");
  switch (m.name) {
    case "FieldDeclaration":
    case "ConstantDeclaration": {
      const t = typeOf(src, m);
      const constant = info.kind === "interface" || m.name === "ConstantDeclaration";
      for (const d of m.getChildren("VariableDeclarator")) {
        const name = text(src, child(d, "Definition"));
        info.fields.set(name, { name, type: t && { ...t, dims: t.dims + dims(d) }, isStatic: isStatic || constant, from: d.from });
      }
      break;
    }
    case "MethodDeclaration":
      addMethod(info, {
        name: text(src, child(m, "Definition")),
        ret: typeOf(src, m),
        params: paramsOf(src, child(m, "FormalParameters")),
        typeParams: typeParamsOf(src, m),
        isStatic,
        from: m.from,
      });
      break;
    case "ConstructorDeclaration":
      info.ctors.push({ params: paramsOf(src, child(m, "FormalParameters")) });
      break;
    case "EnumConstant": {
      const name = text(src, child(m, "Definition"));
      info.fields.set(name, { name, type: ref(info.name), isStatic: true, constant: true, from: m.from });
      break;
    }
    case "EnumBodyDeclarations":
      for (const c of children(m)) readMember(src, info, c);
      break;
  }
}

function readClass(src, node, origin) {
  const kind = DECLS[node.name];
  const name = text(src, child(node, "Definition"));
  const info = {
    name, kind, origin, from: node.from, to: node.to,
    typeParams: typeParamsOf(src, node),
    superclass: null, interfaces: [],
    fields: new Map(), methods: new Map(), ctors: [],
  };
  const sup = child(node, "Superclass");
  if (sup) info.superclass = typeOf(src, sup);
  for (const listParent of [child(node, "SuperInterfaces"), child(node, "ExtendsInterfaces")]) {
    const list = listParent && child(listParent, "InterfaceTypeList");
    if (!list) continue;
    for (const t of children(list)) if (TYPE_NODES.has(t.name)) info.interfaces.push(parseType(text(src, t)));
  }
  if (!info.superclass && name !== "Object") info.superclass = kind === "enum" ? ref("Enum", [ref(name)]) : ref("Object");
  if (kind === "enum") {
    addMethod(info, { name: "values", ret: ref(name, [], 1), params: [], typeParams: [], isStatic: true });
    addMethod(info, { name: "valueOf", ret: ref(name), params: [{ name: "name", type: ref("String") }], typeParams: [], isStatic: true });
  }
  const header = child(node, "RecordHeader");
  if (header) {
    for (const c of header.getChildren("RecordComponent")) {
      const cname = text(src, child(c, "Definition"));
      const t = typeOf(src, c);
      info.fields.set(cname, { name: cname, type: t, isStatic: false, from: c.from });
      addMethod(info, { name: cname, ret: t, params: [], typeParams: [], isStatic: false });
    }
  }
  const body = children(node).find((c) => BODIES.has(c.name));
  if (body) for (const m of children(body)) readMember(src, info, m);
  return info;
}

/** Every class, interface, enum and record of a tree, nested ones included. */
export function indexTree(tree, src, origin) {
  const out = new Map();
  tree.iterate({
    enter(n) {
      if (!DECLS[n.name]) return;
      const info = readClass(src, n.node, origin);
      if (info.name && !out.has(info.name)) out.set(info.name, info);
    },
  });
  return out;
}

let jdk = null;
export function jdkIndex() {
  if (!jdk) jdk = indexTree(parser.parse(JDK_SOURCE), JDK_SOURCE, "jdk");
  return jdk;
}

/** Lookups over the user's classes (doc) layered on top of the JDK ones. */
export class Model {
  constructor(user) {
    this.user = user;
    this.jdk = jdkIndex();
  }

  cls(name) {
    if (!name) return null;
    const n = simpleName(name);
    return this.user.get(n) || this.jdk.get(n) || null;
  }

  /** The class and its supertypes, each with its type variables bound. */
  *hierarchy(t) {
    const seen = new Set();
    const queue = [t];
    while (queue.length) {
      const cur = queue.shift();
      if (!cur || seen.has(cur.name)) continue;
      seen.add(cur.name);
      const cls = this.cls(cur.name);
      if (!cls) { yield { cls: null, name: cur.name, map: new Map() }; continue; }
      const map = new Map(cls.typeParams.map((p, i) => [p, cur.args[i] || ref("Object")]));
      yield { cls, name: cur.name, map };
      queue.push(subst(cls.superclass, map), ...cls.interfaces.map((x) => subst(x, map)));
    }
  }

  /** True when every supertype is declared by the user (or is Object):
   *  then a missing member really is missing. */
  complete(t) {
    if (!t || t.dims) return false;
    for (const h of this.hierarchy(t)) {
      if (!h.cls) return false;
      if (h.cls.origin !== "user" && h.name !== "Object") return false;
    }
    return true;
  }

  field(t, name) {
    if (!t) return null;
    if (t.dims) return name === "length" ? { name, type: ref("int"), isStatic: false } : null;
    for (const h of this.hierarchy(t)) {
      const f = h.cls && h.cls.fields.get(name);
      if (f) return { ...f, type: subst(f.type, h.map), owner: h.cls.name };
    }
    return null;
  }

  methods(t, name) {
    const out = [];
    if (!t || t.dims) return out;
    for (const h of this.hierarchy(t)) {
      for (const m of (h.cls && h.cls.methods.get(name)) || []) out.push(bind(m, h));
    }
    return out;
  }

  /** Fields and methods reachable from a type, for completion. */
  members(t, staticOnly) {
    const fields = new Map();
    const methods = new Map();
    if (!t) return { fields: [], methods: [] };
    if (t.dims) return { fields: [{ name: "length", type: ref("int") }], methods: [] };
    for (const h of this.hierarchy(t)) {
      if (!h.cls) continue;
      for (const f of h.cls.fields.values()) {
        if (staticOnly && !f.isStatic) continue;
        if (!fields.has(f.name)) fields.set(f.name, { ...f, type: subst(f.type, h.map), owner: h.cls.name });
      }
      for (const [name, list] of h.cls.methods) {
        for (const m of list) {
          if (staticOnly && !m.isStatic) continue;
          const all = methods.get(name) || [];
          if (!all.some((x) => x.params.length === m.params.length)) all.push(bind(m, h));
          methods.set(name, all);
        }
      }
    }
    return { fields: [...fields.values()], methods: [...methods.values()] };
  }

  classNames() {
    return [...new Set([...this.user.keys(), ...this.jdk.keys()])];
  }
}

// A method seen through a parameterised type: List<Ligne>.get() returns Ligne.
// Its own type variables (<T> T of(...)) cannot be inferred here.
function bind(m, h) {
  const map = new Map(h.map);
  for (const p of m.typeParams) map.set(p, UNKNOWN);
  return {
    ...m,
    owner: h.cls.name,
    ret: subst(m.ret, map),
    params: m.params.map((p) => ({ ...p, type: subst(p.type, map) })),
  };
}
