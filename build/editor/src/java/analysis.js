// One analysis per syntax tree, shared by completion and checks.

import { ensureSyntaxTree, syntaxTree } from "@codemirror/language";
import { indexTree, Model } from "./model.js";

const cache = new WeakMap();

export function analyse(state) {
  const tree = ensureSyntaxTree(state, state.doc.length, 200) || syntaxTree(state);
  let a = cache.get(tree);
  if (!a) {
    const src = state.doc.toString();
    a = { tree, src, model: new Model(indexTree(tree, src, "user")) };
    cache.set(tree, a);
  }
  return a;
}

/** Closest name, for "did you mean" hints on typos. */
export function closest(name, candidates) {
  let best = null;
  let bestD = Math.min(3, Math.ceil(name.length / 3) + 1);
  const low = name.toLowerCase();
  for (const c of new Set(candidates)) {
    if (c === name) continue;
    const d = c.toLowerCase() === low ? 0 : distance(low, c.toLowerCase());
    if (d < bestD) { best = c; bestD = d; }
  }
  return best;
}

function distance(a, b) {
  if (Math.abs(a.length - b.length) > 3) return 99;
  let prev = Array.from({ length: b.length + 1 }, (_, i) => i);
  for (let i = 1; i <= a.length; i++) {
    const cur = [i];
    for (let j = 1; j <= b.length; j++) {
      cur[j] = Math.min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1));
    }
    prev = cur;
  }
  return prev[b.length];
}
