// Type references as written in Java source: List<Ligne>[] is
// { name: "List", args: [{ name: "Ligne", args: [], dims: 0 }], dims: 1 }.
// "?" is a type we could not work out: it has no members.

export const UNKNOWN = Object.freeze({ name: "?", args: [], dims: 0 });
export const PRIMITIVES = new Set(["int", "long", "short", "byte", "char", "boolean", "float", "double", "void"]);

export const ref = (name, args = [], dims = 0) => ({ name, args, dims });

// Map.Entry and java.util.List are looked up by their simple name.
export const simpleName = (name) => name.slice(name.lastIndexOf(".") + 1);

export function parseType(text) {
  const toks = text.replace(/@[\w$.]+(\([^)]*\))?/g, " ").match(/\.\.\.|[\w$.]+|[<>,[\]?&]/g) || [];
  let i = 0;
  function one() {
    const t = toks[i++];
    if (t === undefined) return UNKNOWN;
    if (t === "?") {
      if (toks[i] === "extends") { i++; return one(); }
      if (toks[i] === "super") { i++; one(); }
      return ref("Object");
    }
    const out = ref(simpleName(t));
    if (toks[i] === "<") {
      i++;
      while (i < toks.length && toks[i] !== ">") {
        out.args.push(one());
        if (toks[i] === "," || toks[i] === "&") i++;
      }
      i++;
    }
    while (toks[i] === "[" && toks[i + 1] === "]") { out.dims++; i += 2; }
    if (toks[i] === "...") { out.dims++; i++; }
    return out;
  }
  if (toks[0] === "var") return null; // inferred from the initializer
  return one();
}

export function typeToString(t) {
  if (!t) return "?";
  const args = t.args.length ? `<${t.args.map(typeToString).join(", ")}>` : "";
  return t.name + args + "[]".repeat(t.dims);
}

/** Replaces type variables (E, K, V...) by their arguments. */
export function subst(t, map) {
  if (!t || !map.size) return t;
  const m = map.get(t.name);
  if (m && !t.args.length) return { ...m, dims: m.dims + t.dims };
  return t.args.length ? { ...t, args: t.args.map((a) => subst(a, map)) } : t;
}

export const isPrimitive = (t) => t && t.dims === 0 && PRIMITIVES.has(t.name);
export const isUnknown = (t) => !t || t.name === "?";
