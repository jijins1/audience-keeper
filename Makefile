# Les cibles que la CI appelle sont celles d'ici : ce qui est publié doit être
# ce que `make` construit en local, sinon les deux dérivent sans que personne
# le voie.

.PHONY: test
test: ## Tests unitaires et d'intégration HTTP/WebSocket/MCP (sans Docker)
	go test -race ./...

.PHONY: lint
lint: ## golangci-lint, version figée comme en CI
	golangci-lint run ./...

# ---------------------------------------------------------------------------
# Image.
#
# GHCR parce que `origin` est GitHub : la CI y publie avec le GITHUB_TOKEN du
# run, qui expire avec lui. Aucun identifiant de longue durée à stocker.
#
# `REGISTRY_OWNER` est surchargeable et la CI le prend de
# `github.repository_owner` : un fork publie chez lui.
# ---------------------------------------------------------------------------
REGISTRY ?= ghcr.io
REGISTRY_OWNER ?= jijins1
IMAGE_NAME ?= audience-keeper
IMAGE_TAG ?= dev

# Les plateformes PUBLIÉES. `image` reste un build natif mono-plateforme : c'est
# le seul qu'un `docker build` classique sait charger dans le démon local.
PLATFORMS ?= linux/amd64,linux/arm64

IMAGE := $(REGISTRY)/$(REGISTRY_OWNER)/$(IMAGE_NAME)

.PHONY: image
image: ## Construit l'image en local, sans rien pousser
	docker build --build-arg VERSION=$(IMAGE_TAG) -t $(IMAGE):$(IMAGE_TAG) .

# Multi-arch : la séance peut tourner sur un nœud arm64 (Raspberry Pi, Mac
# Apple Silicon). Une image multi-plateforme est une liste de manifestes que le
# démon local ne sait pas retenir sous un tag : buildx l'assemble et la pousse
# en une étape (exige `docker login` et un builder buildx).
.PHONY: image-push
image-push: ## Construit et pousse l'image pour $(PLATFORMS)
	docker buildx build --platform $(PLATFORMS) --build-arg VERSION=$(IMAGE_TAG) \
	  -t $(IMAGE):$(IMAGE_TAG) --push .

# LE CHART ET L'IMAGE PORTENT LA MÊME VERSION.
#
# `appVersion` est le tag d'image par défaut : un chart tiré en 0.0.N déploie
# forcément l'image 0.0.N. Et la version est IMMUABLE : un tag mouvant republié
# n'est jamais retiré par un nœud qui l'a en cache (`IfNotPresent`), et le
# rollout repartirait sur l'ancien binaire sans rien signaler.
CHART_VERSION ?= 0.0.0-dev

.PHONY: chart-push
chart-push: ## Publie le chart OCI en $(CHART_VERSION) (exige `helm registry login`)
	mkdir -p dist/
	helm package charts/audience-keeper \
	  --version $(CHART_VERSION) --app-version $(CHART_VERSION) --destination dist/
	helm push dist/audience-keeper-$(CHART_VERSION).tgz oci://$(REGISTRY)/$(REGISTRY_OWNER)/charts

# Le binaire, pour qui fait cours depuis son laptop sans Docker.
TARGETS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: dist
dist: ## Compile le binaire pour $(TARGETS) dans dist/
	@mkdir -p dist
	@for target in $(TARGETS); do \
	  os="$${target%/*}"; arch="$${target#*/}"; \
	  out="dist/audience-keeper_$${os}_$${arch}"; \
	  [ "$$os" = "windows" ] && out="$$out.exe"; \
	  echo "→ $$target"; \
	  CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" go build -trimpath \
	    -ldflags "-s -w -X main.version=$(IMAGE_TAG)" -o "$$out" ./cmd/audience-keeper || exit 1; \
	done

.PHONY: help
help: ## Liste les cibles disponibles
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
