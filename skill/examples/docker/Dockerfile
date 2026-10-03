# syntax=docker/dockerfile:1
#
# Receita multi-stage para um serviço Go que usa go-loghub-ident.
#
#   docker build -t meu-servico .
#   docker run --hostname meu-servico-01 \
#     -v meu-servico-ident:/data \
#     -e WORKSPACE=production \
#     meu-servico
#
# O volume NOMEADO guarda machine_id e agent_uuid entre recriações do
# container; --hostname fixa o Hostname (sem ele, o Docker usa o ID do
# container, que muda a cada recriação).

# Use a mesma versão de Go do go.mod do projeto: a imagem oficial roda com
# GOTOOLCHAIN=local e não baixa uma toolchain mais nova sozinha.
ARG GO_VERSION=1.22

# ------------------------------------------------------------------------------
# Etapa 1: build estático, compilado na arquitetura do host para a do alvo
# (docker buildx build --platform linux/amd64,linux/arm64 ...)
# ------------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS builder
ARG TARGETOS TARGETARCH

WORKDIR /src

# Cache de dependências
COPY go.mod go.sum ./
RUN go mod download

# Cópia do código-fonte e compilação estática
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath -ldflags="-w -s" \
    -o /out/app .

# Ponto de montagem do DATADIR. A imagem distroless não tem /data, e o
# diretório que VOLUME criaria sozinho pertence ao root: o usuário nonroot não
# conseguiria gravar machine_id e agent_uuid (código de saída 113).
RUN mkdir -p /out/data

# ------------------------------------------------------------------------------
# Etapa 2: imagem final mínima (distroless, usuário nonroot UID/GID 65532)
# ------------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/app /app

# /data pertence ao usuário nonroot. Um volume nomeado novo herda esse dono
# na primeira montagem. Bind mount (-v /caminho/do/host:/data) não herda: dê o
# diretório do host ao UID 65532 (chown 65532:65532) ou use um volume nomeado.
COPY --from=builder --chown=65532:65532 /out/data /data
VOLUME ["/data"]

# AGENT_NAME explícito: sem ele o nome vem de argv[0], que aqui é "app" — o
# mesmo para todo serviço construído com esta receita. WORKSPACE é do
# ambiente, não da imagem: defina-o no docker run ou no manifesto.
ENV DATADIR=/data \
    AGENT_NAME=meu-servico

USER 65532:65532

ENTRYPOINT ["/app"]
