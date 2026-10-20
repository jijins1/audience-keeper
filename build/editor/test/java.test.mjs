import { test } from "node:test";
import assert from "node:assert/strict";
import { EditorState } from "@codemirror/state";
import { CompletionContext } from "@codemirror/autocomplete";
import { java } from "@codemirror/lang-java";
import { javaCompletion } from "../src/java/complete.js";
import { javaDiagnostics } from "../src/java/lint.js";

const CONTEXT = `import java.util.*;

class Client {
  private String nom;
  public String getNom() { return nom; }
}

class Ligne {
  private double prix;
  private int quantite;
  public double prix() { return prix; }
  public int getQuantite() { return quantite; }
}

class Commande {
  protected List<Ligne> lignes = new ArrayList<>();
  protected Client client;
  public Client getClient() { return client; }
  public void ajouter(Ligne l) { lignes.add(l); }
}
`;

// Template: the answer goes in a method of a subclass of Commande.
const BEFORE = CONTEXT + "\nclass Solution extends Commande {\n  double total() {\n";
const AFTER = "\n  }\n}\n";

function setup(answer) {
  const at = answer.indexOf("|");
  const code = answer.replace("|", "");
  const state = EditorState.create({ doc: BEFORE + code + AFTER, extensions: [java()] });
  return { state, pos: BEFORE.length + (at < 0 ? code.length : at), zone: { from: BEFORE.length, to: BEFORE.length + code.length } };
}

function labels(answer, explicit = false) {
  const { state, pos } = setup(answer);
  const r = javaCompletion(new CompletionContext(state, pos, explicit));
  return r ? r.options.map((o) => o.label) : [];
}

function diags(answer) {
  const { state, zone } = setup(answer);
  return javaDiagnostics(state, zone);
}

test("members of a variable declared with a provided class", () => {
  const l = labels("    Client c = getClient();\n    c.|");
  assert.ok(l.includes("getNom"), l.join());
  assert.ok(l.includes("nom"));
  assert.ok(!l.includes("prix"));
});

test("chained calls use return types", () => {
  assert.ok(labels("    getClient().|").includes("getNom"));
  assert.ok(labels("    this.getClient().get|").includes("getNom"));
});

test("generics: for-each over List<Ligne> and list.get()", () => {
  assert.ok(labels("    for (Ligne l : lignes) { l.| }").includes("prix"));
  assert.ok(labels("    for (var l : lignes) { l.| }").includes("getQuantite"));
  assert.ok(labels("    lignes.get(0).|").includes("prix"));
  assert.ok(labels("    lignes.|").includes("stream"), "JDK List members");
});

test("var is inferred from its initialiser", () => {
  assert.ok(labels("    var x = new Ligne();\n    x.|").includes("prix"));
});

test("inherited members through this and bare names", () => {
  const l = labels("    this.|");
  assert.ok(l.includes("lignes") && l.includes("ajouter") && l.includes("getClient"));
  const bare = labels("    lig|");
  assert.ok(bare.includes("lignes"));
  assert.ok(bare.includes("Ligne"), "class names");
});

test("JDK statics and literals", () => {
  assert.ok(labels("    System.out.|").includes("println"));
  assert.ok(labels("    Math.|").includes("max"));
  assert.ok(!labels("    Math.|").includes("equals"), "only static members on a class name");
  assert.ok(labels('    "abc".|').includes("toUpperCase"));
});

test("no completion in strings, comments or unknown receivers", () => {
  assert.deepEqual(labels('    String s = "a.|";'), []);
  assert.deepEqual(labels("    // c.|"), []);
  assert.deepEqual(labels("    inconnu.|"), []);
  assert.deepEqual(labels("    lignes.forEach(l -> l.|);"), []); // lambda parameter types are not inferred
});

test("a valid answer has no diagnostics", () => {
  const d = diags("    double t = 0;\n    for (Ligne l : lignes) {\n      t += l.prix() * l.getQuantite();\n    }\n    return t;");
  assert.deepEqual(d, []);
});

test("syntax errors stay inside the answer", () => {
  const { zone } = setup("    return 0");
  const d = diags("    return 0");
  assert.ok(d.length >= 1 && d.every((x) => x.severity === "error"));
  assert.ok(d.every((x) => x.from >= zone.from && x.to <= zone.to), JSON.stringify(d));

  const open = diags("    if (true) {\n      return 0;");
  assert.ok(open.some((x) => /Bloc non terminé/.test(x.message)), JSON.stringify(open));
});

test("unknown members of provided classes, with a suggestion", () => {
  const d = diags("    for (Ligne l : lignes) { return l.prx(); }\n    return 0;");
  assert.equal(d.length, 1, JSON.stringify(d));
  assert.match(d[0].message, /« prx » n'existe pas dans Ligne\. Tu voulais dire « prix » \?/);
  assert.equal(d[0].actions.length, 1);

  const f = diags("    return getClient().nom2.length();");
  assert.match(f[0].message, /« nom2 » n'existe pas dans Client/);

  const paren = diags("    return lignes.get(0).prix;");
  assert.equal(paren.length, 0, "prix is also a field of Ligne");
  const call = diags("    getClient().getNom;\n    return 0;");
  assert.match(call[0].message, /il manque les parenthèses/);

  const arity = diags("    ajouter();\n    return 0;");
  assert.match(arity[0].message, /attend 1 paramètre/);
});

test("JDK classes are never blamed (they are partial)", () => {
  assert.deepEqual(diags("    lignes.methodeInconnue();\n    return 0;"), []);
  assert.deepEqual(diags("    String s = \"\";\n    s.foo();\n    return 0;"), []);
});

test("undeclared variables and unknown types", () => {
  const v = diags("    totl = 0;\n    return 0;");
  assert.equal(v.length, 1, JSON.stringify(v));
  assert.match(v[0].message, /« totl » n'est pas déclaré/);

  const t = diags("    Comande c = null;\n    return 0;");
  assert.match(t[0].message, /Type inconnu : « Comande ».*Tu voulais dire « Commande » \?/);

  assert.deepEqual(diags("    Scanner sc = null;\n    BigDecimal b = null;\n    Map<String, Integer> m = new HashMap<>();\n    return 0;"), []);
  assert.deepEqual(diags("    lignes.forEach(l -> System.out.println(l));\n    return 0;"), []);
});

test("syntax messages point at the likely cause", () => {
  assert.match(diags("    return 0")[0].message, /il manque sans doute un « ; »/);
  const typo = diags("    retrun 0;");
  assert.equal(typo.length, 1, JSON.stringify(typo));
  assert.match(typo[0].message, /« retrun » : tu voulais dire « return » \?/);
  const { zone } = setup("    retrun 0;");
  assert.equal(typo[0].from - zone.from, 4, "points at the misspelt word");
  assert.equal(typo[0].actions.length, 1);
});

test("no typo hint on one or two letter names", () => {
  const d = diags("    for (Ligne l : lignes) t += l.prix();\n    return 0;");
  assert.ok(d.length && d.every((x) => !/Tu voulais dire/.test(x.message)), JSON.stringify(d));
});

test("one brace too many is named as such", () => {
  const d = diags("    for (Ligne l : lignes) {\n    }\n    }\n    return 0;");
  assert.ok(d.some((x) => /de trop/.test(x.message)), JSON.stringify(d));
});
