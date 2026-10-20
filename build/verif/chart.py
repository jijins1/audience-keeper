#!/usr/bin/env python3
"""Vérifie les ressources que le chart REND, pas seulement qu'il se rend.

`helm lint` et `helm template` réussissent sur un manifeste où une ressource a
disparu (deux documents fusionnés par un `-}}` de trop) ou où une propriété qui
tient la séance a glissé. Ce script lit le YAML produit et vérifie ce qui compte.
"""
import subprocess
import sys

import yaml

CHART = "charts/audience-keeper"
errors = []


def render(*sets, expect_fail=False):
    cmd = ["helm", "template", "ak", CHART]
    for s in sets:
        cmd += ["--set", s]
    res = subprocess.run(cmd, capture_output=True, text=True)
    if expect_fail:
        return res
    if res.returncode != 0:
        sys.exit(f"helm template a échoué : {res.stderr}")
    return [d for d in yaml.safe_load_all(res.stdout) if d]


def check(cond, msg):
    if not cond:
        errors.append(msg)


def by_kind(docs):
    out = {}
    for d in docs:
        out.setdefault(d["kind"], []).append(d)
    return out


# 1. Sans Secret de jetons, l'installation doit échouer — pas démarrer un pod
#    qui refusera de booter faute d'AK_ADMIN_TOKEN.
res = render(expect_fail=True)
check(res.returncode != 0 and "existingSecret" in res.stderr, "le chart s'installe sans auth.existingSecret")

# 2. Installation complète : chaque ressource attendue est là, une seule fois.
docs = render("auth.existingSecret=tok", "ingress.enabled=true", "ingress.host=quiz.example.org", "decks.configMap=cours")
kinds = by_kind(docs)
for kind in ("Deployment", "Service", "Ingress"):
    check(len(kinds.get(kind, [])) == 1, f"{kind} : {len(kinds.get(kind, []))} rendu(s), attendu 1")

dep = kinds["Deployment"][0]
spec = dep["spec"]
pod = spec["template"]["spec"]
ctr = pod["containers"][0]

# 3. La séance est en mémoire : un seul pod, jamais deux en même temps.
check(spec.get("replicas") == 1, "replicas != 1 : deux pods seraient deux salles de classe")
check(spec.get("strategy", {}).get("type") == "Recreate", "stratégie != Recreate : deux pods coexisteraient au déploiement")

# 4. Durcissement non négociable.
sc = ctr.get("securityContext", {})
check(sc.get("readOnlyRootFilesystem") is True, "racine non en lecture seule")
check(sc.get("allowPrivilegeEscalation") is False, "élévation de privilèges permise")
check(sc.get("capabilities", {}).get("drop") == ["ALL"], "capacités non retirées")
check(pod.get("securityContext", {}).get("runAsNonRoot") is True, "pod non contraint à non-root")
check(pod.get("automountServiceAccountToken") is False, "jeton de ServiceAccount monté")

# 5. Les jetons viennent d'un Secret, jamais en clair dans le manifeste.
env = {e["name"]: e for e in ctr["env"]}
for name in ("AK_ADMIN_TOKEN", "AK_MCP_TOKEN"):
    check("valueFrom" in env.get(name, {}) and "value" not in env.get(name, {}), f"{name} n'est pas lu depuis un Secret")

# 6. Le QR code doit pointer vers l'URL publique, et les diaporamas être montés.
check(env.get("AK_PUBLIC_URL", {}).get("value") == "https://quiz.example.org", "AK_PUBLIC_URL ne suit pas l'ingress")
check(env.get("AK_DECKS_DIR", {}).get("value") == "/decks", "AK_DECKS_DIR absent alors que decks.configMap est posé")
check(any(m["mountPath"] == "/decks" and m.get("readOnly") for m in ctr.get("volumeMounts", [])), "/decks non monté en lecture seule")

# 7. Le chart 0.0.N déploie l'image 0.0.N.
chart = yaml.safe_load(open(f"{CHART}/Chart.yaml"))
check(ctr["image"].endswith(":" + str(chart["appVersion"])), f"tag d'image {ctr['image']} != appVersion {chart['appVersion']}")

# 8. L'ingress route vers le Service rendu.
ing = kinds["Ingress"][0]
backend = ing["spec"]["rules"][0]["http"]["paths"][0]["backend"]["service"]["name"]
check(backend == kinds["Service"][0]["metadata"]["name"], "l'Ingress vise un Service qui n'existe pas")

# 9. Sans ingress ni diaporamas : rien de superflu.
minimal = by_kind(render("auth.existingSecret=tok"))
check("Ingress" not in minimal, "Ingress rendu alors que ingress.enabled=false")
check(not minimal["Deployment"][0]["spec"]["template"]["spec"].get("volumes"), "volume rendu sans diaporamas")

if errors:
    for e in errors:
        print(f"::error::{e}")
    sys.exit(1)
print("chart : toutes les vérifications passent")
